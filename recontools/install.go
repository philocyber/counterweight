package recontools

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/locale"
)

// maxBinarySize guards against a malformed/oversized archive exhausting disk.
// These tools are a few MB each; 128 MiB is generous headroom.
const maxBinarySize = 128 << 20

// Phase is the install stage, used directly in the SSE phase field.
type Phase string

const (
	PhaseDownload Phase = "downloading"
	PhaseVerify   Phase = "verifying"
	PhaseExtract  Phase = "extracting"
	PhaseDone     Phase = "done"
	PhaseFailed   Phase = "failed"
)

// Progress reports install stages to the caller/UI. pct is meaningful only
// during download (0-100); other phases use -1.
type Progress func(ph Phase, pct int, msg string)

// BinDir is where installed recon-tool binaries live, rooted under Counterweight's
// own data directory (not the install's PATH) so an uninstall/data wipe takes
// them with it and multiple Counterweight instances on one host don't collide.
func BinDir(dataDir string) string {
	return filepath.Join(dataDir, "tools", "bin")
}

// BinPath is the managed install location for one tool.
func BinPath(dataDir, key string) string {
	name := key
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(BinDir(dataDir), name)
}

// assetOSToken maps Go's GOOS to the goreleaser archive-name token ProjectDiscovery
// uses for all four cataloged tools (confirmed against each repo's .goreleaser.yml).
func assetOSToken(goos string) string {
	if goos == "darwin" {
		return "macOS"
	}
	return goos
}

// assetName builds the expected release archive name for one tool/platform,
// e.g. "subfinder_2.6.3_linux_amd64.zip".
func assetName(key, version, goos, goarch string) string {
	return fmt.Sprintf("%s_%s_%s_%s.zip", key, strings.TrimPrefix(version, "v"), assetOSToken(goos), goarch)
}

// Install downloads, verifies, and installs one recon tool's binary for the
// current platform. On success it returns the binary's self-reported version
// string (best-effort; empty if it couldn't be parsed — not a failure on its own).
func Install(ctx context.Context, c *http.Client, spec ToolSpec, dataDir string, prog Progress) (string, error) {
	if prog == nil {
		prog = func(Phase, int, string) {}
	}
	binDir := BinDir(dataDir)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", locale.Errorf("Create tools directory: %w", err)
	}

	prog(PhaseDownload, 0, "Checking latest release…")
	rel, err := FetchLatest(ctx, c, spec.Repo)
	if err != nil {
		return "", err
	}

	name := assetName(spec.Key, rel.TagName, runtime.GOOS, runtime.GOARCH)
	asset, ok := rel.FindAsset(name)
	if !ok {
		return "", locale.Errorf("Release %s has no package for %s/%s (missing %s)", rel.TagName, runtime.GOOS, runtime.GOARCH, name)
	}

	sumsAsset, ok := rel.FindChecksums()
	if !ok {
		return "", locale.NewError("Release is missing a checksum manifest; refusing to install an unverified binary")
	}
	prog(PhaseDownload, 0, "Fetching checksum manifest…")
	sums, err := fetchSums(ctx, c, sumsAsset.URL)
	if err != nil {
		return "", err
	}
	want, ok := lookupSum(sums, name)
	if !ok {
		return "", locale.Errorf("Checksum manifest does not list %s; refusing to install an unverified binary", name)
	}

	// Keep temp files inside the destination directory so the final rename is
	// atomic on one filesystem.
	zipPath := filepath.Join(binDir, "."+spec.Key+".download.part")
	tmpBin := filepath.Join(binDir, "."+spec.Key+".new.part")
	defer func() {
		_ = os.Remove(zipPath)
		_ = os.Remove(tmpBin)
	}()

	prog(PhaseDownload, 0, fmt.Sprintf("Downloading %s…", name))
	got, err := download(ctx, c, asset, zipPath, prog)
	if err != nil {
		return "", err
	}

	prog(PhaseVerify, -1, "Verifying SHA256…")
	if !strings.EqualFold(got, want) {
		return "", locale.Errorf("SHA256 mismatch for %s: expected %s, got %s (download corrupted or tampered with)", name, short(want), short(got))
	}

	prog(PhaseExtract, -1, "Extracting…")
	binName := spec.Key
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	if err := extractBinary(zipPath, tmpBin, binName); err != nil {
		return "", err
	}
	if err := smokeTest(tmpBin); err != nil {
		return "", locale.Errorf("Downloaded binary does not run on this system: %w", err)
	}
	version := probeVersion(tmpBin)

	dst := BinPath(dataDir, spec.Key)
	if err := os.Rename(tmpBin, dst); err != nil {
		return "", locale.Errorf("Install binary: %w", err)
	}
	return version, nil
}

// fetchSums downloads and parses a sha256sum-style checksum manifest.
func fetchSums(ctx context.Context, c *http.Client, assetURL string) (map[string]string, error) {
	body, err := get(ctx, c, assetURL)
	if err != nil {
		return nil, locale.Errorf("Download checksum manifest: %w", err)
	}
	defer body.Close()

	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return nil, locale.Errorf("Read checksum manifest: %w", err)
	}
	out := parseSums(string(raw))
	if len(out) == 0 {
		return nil, locale.NewError("Checksum manifest is empty or has an unrecognized format")
	}
	return out, nil
}

// lookupSum matches exact, then case-insensitive, filename.
func lookupSum(sums map[string]string, name string) (string, bool) {
	if v, ok := sums[name]; ok {
		return v, true
	}
	for k, v := range sums {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// parseSums parses "<sha256>  <filename>" lines (sha256sum format; shasum's
// binary mode prefixes the filename with "*").
func parseSums(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 || !isHexSHA256(fields[0]) {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" {
			continue
		}
		out[name] = strings.ToLower(fields[0])
	}
	return out
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// download writes an asset to dst, computing SHA256 and reporting progress.
func download(ctx context.Context, c *http.Client, a Asset, dst string, prog Progress) (string, error) {
	body, err := get(ctx, c, a.URL)
	if err != nil {
		return "", locale.Errorf("Download %s: %w", a.Name, err)
	}
	defer body.Close()

	f, err := os.Create(dst)
	if err != nil {
		return "", locale.Errorf("Create temporary file: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	pw := &progressWriter{total: a.Size, prog: prog, name: a.Name, last: time.Now()}
	if _, err := io.Copy(io.MultiWriter(f, h, pw), body); err != nil {
		return "", locale.Errorf("Download interrupted: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", locale.Errorf("Write to disk failed: %w", err)
	}
	if a.Size > 0 && pw.written != a.Size {
		return "", locale.Errorf("Incomplete download: expected %d bytes, got %d bytes", a.Size, pw.written)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// get performs an allowlist-restricted GET and returns the response body.
func get(ctx context.Context, c *http.Client, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if err := checkURL(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "counterweight-recontools")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// extractBinary extracts the single file named wantBase (case-insensitive
// basename match) from a ZIP archive into dst, executable.
func extractBinary(zipPath, dst, wantBase string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return locale.Errorf("Open release package: %w", err)
	}
	defer zr.Close()

	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() || !strings.EqualFold(path.Base(entry.Name), wantBase) {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return locale.Errorf("Read %s: %w", entry.Name, err)
		}
		defer rc.Close()

		f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return locale.Errorf("Write extracted binary: %w", err)
		}
		defer f.Close()

		n, err := io.Copy(f, io.LimitReader(rc, maxBinarySize+1))
		if err != nil {
			return locale.Errorf("Extract %s: %w", entry.Name, err)
		}
		if n > maxBinarySize {
			return locale.Errorf("Binary in release package exceeds %s; refusing extraction", humanSize(maxBinarySize))
		}
		if n == 0 {
			return locale.Errorf("%s in the release package is empty", wantBase)
		}
		return f.Sync()
	}
	return locale.Errorf("Release package does not contain %s", wantBase)
}

// smokeTest runs the binary with -version to confirm it actually executes on
// this system (arch mismatch, missing libc, etc. would otherwise only surface
// the first time an agent tries to use it).
func smokeTest(binPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, "-version")
	return cmd.Run()
}

// probeVersion runs the binary with -version and returns the first non-empty
// output line, best-effort. An empty result does not indicate failure — some
// builds print to stderr in a form worth skipping rather than guessing at.
func probeVersion(binPath string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, binPath, "-version").CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// progressWriter counts bytes and throttles updates instead of emitting SSE
// for every chunk.
type progressWriter struct {
	total   int64
	written int64
	name    string
	prog    Progress
	last    time.Time
}

func (w *progressWriter) Write(b []byte) (int, error) {
	w.written += int64(len(b))
	if time.Since(w.last) < 300*time.Millisecond {
		return len(b), nil
	}
	w.last = time.Now()
	pct := -1
	if w.total > 0 {
		pct = int(w.written * 100 / w.total)
	}
	w.prog(PhaseDownload, pct, fmt.Sprintf("Downloading %s / %s", humanSize(w.written), humanSize(w.total)))
	return len(b), nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12] + "…"
	}
	return sum
}
