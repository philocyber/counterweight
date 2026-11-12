// Package scanner runs deterministic supplementary scanners (dependency
// advisories and committed secrets) over a cloned repository and turns their
// JSON output into leads.
//
// Scanners are a supplement to the model's own review, never its basis: the
// package knows nothing about tasks or agents. A lead is an unverified pointer
// to a place in the code; it is not a finding and carries no severity. Secret
// leads never carry the secret: parsers read only the rule id, the file and the
// line, and ignore every field that could hold matched text.
package scanner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Scanner keys. They are also the binary names.
const (
	OSV      = "osv-scanner"
	Gitleaks = "gitleaks"
)

// Lead kinds.
const (
	KindSecret   = "secret"
	KindAdvisory = "advisory"
)

// Run limits: a scanner that exceeds either is stopped and reported as failed.
const (
	OutputLimit  = 32 << 20 // bytes of JSON read from a scanner's stdout
	stderrLimit  = 16 << 10
	maxLeadField = 300
)

// Lead is one unverified pointer produced by a scanner.
type Lead struct {
	Scanner      string
	Kind         string
	RuleID       string // rule id (secret) or advisory id (advisory)
	Title        string
	File         string // slash-separated, relative to the repository root
	Line         int    // 0 when unknown
	Package      string
	Version      string
	FixedVersion string
	Aliases      []string
	Severity     float64 // scanner score where one is given; used only to order delivery
	Detail       string
	Fingerprint  string
}

// Spec describes how to run and read one scanner.
type Spec struct {
	Key         string
	Kind        string
	Purpose     string
	ReleasesURL string // the project page to download the release binary from
	Timeout     time.Duration
	VersionArgs []string
	// Args builds the fixed argument list; root is the repository clone and
	// workDir a private empty directory holding ConfigFiles.
	Args func(root, workDir string) []string
	// ConfigFiles are written into workDir before the run, so the scanner uses
	// our configuration instead of one shipped inside the untrusted repository.
	ConfigFiles map[string]string
	// Parse converts the scanner's stdout into leads. root makes file paths relative.
	Parse func(root string, out []byte) ([]Lead, error)
	// OKExit lists exit codes that still carry valid output (for example "findings present").
	OKExit []int
	// EmptyExit lists exit codes meaning "nothing to scan" (no leads, not a failure).
	EmptyExit []int
}

// Specs returns the supported scanners in run order.
func Specs() []Spec {
	return []Spec{osvSpec(), gitleaksSpec()}
}

// Find returns the spec for a scanner key.
func Find(key string) (Spec, bool) {
	for _, s := range Specs() {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}

// Resolution is where a scanner binary was found.
type Resolution struct {
	Path   string
	Source string // "configured", "managed" or "path"
}

// Resolve finds a scanner binary: an explicit configured path first, then the
// managed tool directory (managedPath, where the installer places binaries),
// then PATH (lookPath, exec.LookPath when nil). ok is false when none exists.
func Resolve(configured, managedPath, key string, lookPath func(string) (string, error)) (res Resolution, ok bool) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if isFile(configured) {
		return Resolution{configured, "configured"}, true
	}
	if isFile(managedPath) {
		return Resolution{managedPath, "managed"}, true
	}
	if p, err := lookPath(key); err == nil && p != "" {
		return Resolution{p, "path"}, true
	}
	return Resolution{}, false
}

func isFile(p string) bool {
	if strings.TrimSpace(p) == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// Result is what one scanner run produced.
type Result struct {
	Leads   []Lead
	Version string
}

// Run executes one scanner over root as a subprocess: no shell, the spec's fixed
// argument list, a timeout, and a cap on the output it may produce. env is the
// complete environment of the subprocess.
func Run(ctx context.Context, spec Spec, bin, root string, env []string) (Result, error) {
	res := Result{Version: Version(ctx, spec, bin, env)}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// The subprocess runs in a private empty directory, so no ignore file or
	// configuration of the surrounding process or of the repository applies.
	workDir, err := os.MkdirTemp("", "cw-scan-")
	if err != nil {
		return res, fmt.Errorf("could not create a work directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	for name, content := range spec.ConfigFiles {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o600); err != nil {
			return res, fmt.Errorf("could not write scanner configuration: %w", err)
		}
	}

	var stdout, stderr capBuffer
	stdout.limit, stderr.limit = OutputLimit, stderrLimit
	cmd := exec.CommandContext(ctx, bin, spec.Args(root, workDir)...)
	cmd.Dir = workDir
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	if stdout.exceeded {
		return res, fmt.Errorf("output exceeded %d MiB", OutputLimit>>20)
	}
	if ctx.Err() == context.DeadlineExceeded {
		return res, fmt.Errorf("timed out after %s", timeout)
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return res, fmt.Errorf("could not start: %w", err)
		}
		code := exit.ExitCode()
		if contains(spec.EmptyExit, code) {
			return res, nil
		}
		if !contains(spec.OKExit, code) {
			return res, fmt.Errorf("exited with status %d: %s", code, firstLine(stderr.String()))
		}
	}
	leads, perr := spec.Parse(root, stdout.Bytes())
	if perr != nil {
		return res, fmt.Errorf("unreadable output: %w", perr)
	}
	res.Leads = leads
	return res, nil
}

// Version asks the binary for its version, best effort (empty when unknown).
func Version(ctx context.Context, spec Spec, bin string, env []string) string {
	if len(spec.VersionArgs) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, spec.VersionArgs...)
	cmd.Env = env
	var out capBuffer
	out.limit = 4 << 10
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 2 * time.Second
	_ = cmd.Run()
	return clip(firstLine(out.String()), 80)
}

// BaseEnv is the minimal environment scanners run with: enough to locate their
// own configuration and reach the network through a proxy, nothing else.
func BaseEnv() []string {
	keep := []string{"PATH", "HOME", "USERPROFILE", "TMPDIR", "TEMP", "TMP", "SYSTEMROOT", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"}
	var env []string
	for _, k := range keep {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// capBuffer keeps at most limit bytes and records whether more arrived.
type capBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); len(p) > room {
		if room > 0 {
			c.buf.Write(p[:room])
		}
		c.exceeded = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *capBuffer) Bytes() []byte  { return c.buf.Bytes() }
func (c *capBuffer) String() string { return c.buf.String() }

func contains(codes []int, c int) bool {
	for _, v := range codes {
		if v == c {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return clip(l, 200)
		}
	}
	return ""
}

// clip shortens s to at most n runes, dropping control characters.
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "?"))
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > n {
		return string([]rune(s)[:n]) + "..."
	}
	return s
}

// relPath makes p relative to root with forward slashes; an absolute path
// outside root is reduced to its base name so the server's directory layout
// never leaks.
func relPath(root, p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if p == "" {
		return ""
	}
	root = strings.TrimRight(filepath.ToSlash(root), "/")
	if root != "" && len(p) > len(root) && p[len(root)] == '/' && strings.EqualFold(p[:len(root)], root) {
		return path.Clean(p[len(root)+1:])
	}
	if strings.HasPrefix(p, "/") || len(p) > 1 && p[1] == ':' {
		return path.Base(p)
	}
	return path.Clean(p)
}

// fingerprint is a stable identity for a lead across runs: a hash of what the
// scanner pointed at. It never involves matched text, so it is safe to show.
func fingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}
