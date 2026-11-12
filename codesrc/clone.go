package codesrc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Philocyber/counterweight/locale"
)

// Mode is the review depth. It governs how much of the tree the review agent
// pulls into scope and how much application context it builds; here it only
// decides clone depth (a shallow clone is enough for light/balanced, which read
// a subset of files; comprehensive keeps full history for cross-commit context).
type Mode string

const (
	ModeLight         Mode = "light"
	ModeBalanced      Mode = "balanced"
	ModeComprehensive Mode = "comprehensive"
)

// ParseMode validates a mode string, defaulting empty to balanced.
func ParseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case "", ModeBalanced:
		return ModeBalanced, nil
	case ModeLight:
		return ModeLight, nil
	case ModeComprehensive:
		return ModeComprehensive, nil
	default:
		return "", locale.Errorf("unknown review mode %q (want light, balanced or comprehensive)", s)
	}
}

// cloneDepth returns the git --depth for a mode, or 0 for a full clone.
func (m Mode) cloneDepth() int {
	if m == ModeComprehensive {
		return 0 // full history: comprehensive builds cross-commit/app-workflow context
	}
	return 1 // shallow: light/balanced only read a working-tree subset
}

// CloneOptions configures one clone.
type CloneOptions struct {
	Repo Repo
	Mode Mode
	// Dest is the directory the repo is cloned into. It must not already exist
	// or must be empty; the clone writes the tree directly under it.
	Dest string
	// Token, if set, authenticates to GitHub for private repos. It is passed via
	// an http.extraheader git config in the git process environment (never in the
	// command line, never embedded in the URL, never logged). Empty = anonymous (public repos only).
	Token string
	// Run executes the git command. nil uses the real git binary. Injected in
	// tests so validation and argument construction need no git or network. env
	// holds extra KEY=VALUE entries for that one process (git config such as the
	// auth header travels there, not in args).
	Run func(ctx context.Context, name string, args []string, env []string, dir string) error
}

// Clone fetches the repository source into opts.Dest. It validates the
// destination, builds a fixed HTTPS github.com clone command from already
// validated owner/name, and optionally checks out a ref. It returns the
// absolute path of the cloned tree.
func Clone(ctx context.Context, opts CloneOptions) (string, error) {
	if opts.Repo.Owner == "" || opts.Repo.Name == "" {
		return "", locale.NewError("clone requires a parsed repository")
	}
	mode := opts.Mode
	if mode == "" {
		mode = ModeBalanced
	}
	dest := strings.TrimSpace(opts.Dest)
	if dest == "" {
		return "", locale.NewError("clone requires a destination directory")
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if err := ensureEmptyDir(abs); err != nil {
		return "", err
	}

	run := opts.Run
	if run == nil {
		run = runGit
	}

	args := []string{}
	// Credentials, if any, as a per-invocation config so they never land in the
	// URL, in git's remote config on disk or in the process command line.
	var env []string
	if opts.Token != "" {
		env = gitConfigEnv("http."+opts.Repo.CloneURL()+".extraheader", "AUTHORIZATION: bearer "+opts.Token)
	}
	args = append(args, "clone", "--no-tags", "--single-branch")
	if d := mode.cloneDepth(); d > 0 {
		args = append(args, "--depth", strconv.Itoa(d))
	}
	if opts.Repo.Ref != "" {
		args = append(args, "--branch", opts.Repo.Ref)
	}
	// "--" terminates options so a (already validated) URL can never be read as
	// a flag. Dest is the final positional argument.
	args = append(args, "--", opts.Repo.CloneURL(), abs)

	if err := run(ctx, "git", args, env, ""); err != nil {
		return "", locale.Errorf("git clone of %s failed: %v", opts.Repo.Slug(), err)
	}
	return abs, nil
}

// HeadCommit returns the full commit SHA checked out in a cloned tree, so a
// review records exactly which code it covered.
func HeadCommit(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return "", locale.Errorf("git rev-parse in %s failed: %v", dir, err)
	}
	sha := strings.TrimSpace(string(out))
	if !commitSHA.MatchString(sha) {
		return "", locale.Errorf("unexpected commit id %q", sha)
	}
	return sha, nil
}

var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ensureEmptyDir accepts a path that does not exist (it is created) or that
// exists and is an empty directory. Anything else is refused so a clone never
// writes into a populated tree.
func ensureEmptyDir(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return os.MkdirAll(path, 0o755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return locale.Errorf("clone destination %q is not a directory", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return locale.Errorf("clone destination %q is not empty", path)
	}
	return nil
}

func runGit(ctx context.Context, name string, args []string, env []string, dir string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// Never prompt for credentials; fail closed on a private repo without a token.
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"), env...)
	return cmd.Run()
}

// gitConfigEnv returns the environment entries (git >= 2.31) that set one git
// config key for a single child process, appended after any GIT_CONFIG_COUNT
// already inherited so existing entries keep working. The value stays out of
// the command line, which other local users can read.
func gitConfigEnv(key, value string) []string {
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	if n < 0 {
		n = 0
	}
	i := strconv.Itoa(n)
	return []string{
		"GIT_CONFIG_COUNT=" + strconv.Itoa(n+1),
		"GIT_CONFIG_KEY_" + i + "=" + key,
		"GIT_CONFIG_VALUE_" + i + "=" + value,
	}
}
