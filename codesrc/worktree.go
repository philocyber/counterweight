package codesrc

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Philocyber/counterweight/locale"
)

// Fix workspaces. Each fix of a confirmed finding gets its own git worktree on a
// server-named branch. The fixer agent edits files there; the server, not the
// model, computes the diff and decides whether it may go to review. Every git call
// disables hooks, external diff drivers and textconv.

// Patch limits: a fix for one finding is small. Larger changes need a human.
const (
	MaxPatchFiles = 10
	MaxPatchLines = 400
)

var fixBranch = regexp.MustCompile(`^counterweight/fix-[0-9]+(-[a-z0-9-]{1,40})?$`)

// FixBranchName is the server-generated branch for a finding's fix attempt.
func FixBranchName(findingID, runID int64) string {
	return "counterweight/fix-" + strconv.FormatInt(findingID, 10) + "-run-" + strconv.FormatInt(runID, 10)
}

// AddFixWorktree creates dest as a worktree of cloneDir on a new branch at the
// clone's checked-out commit, and returns that base commit.
func AddFixWorktree(ctx context.Context, cloneDir, dest, branch string) (string, error) {
	if !fixBranch.MatchString(branch) {
		return "", locale.Errorf("invalid fix branch %q", branch)
	}
	if _, err := os.Stat(dest); err == nil {
		return "", locale.Errorf("fix worktree %q already exists", dest)
	}
	base, err := HeadCommit(ctx, cloneDir)
	if err != nil {
		return "", err
	}
	if _, err := git(ctx, cloneDir, "worktree", "add", "-b", branch, "--", dest, base); err != nil {
		return "", err
	}
	return base, nil
}

// Change is one file in a fix's staged diff.
type Change struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // A, M, D, T
	OldMode string `json:"old_mode"`
	NewMode string `json:"new_mode"`
	Binary  bool   `json:"binary"`
	Lines   int    `json:"lines"` // added + deleted
}

// CollectChanges stages everything in the worktree and returns the per-file
// changes and the unified diff, both computed by git.
func CollectChanges(ctx context.Context, worktree string) ([]Change, string, error) {
	if _, err := worktreeGit(ctx, worktree, "add", "-A"); err != nil {
		return nil, "", err
	}
	raw, err := worktreeGit(ctx, worktree, "diff", "--cached", "--raw", "-z", "--no-renames")
	if err != nil {
		return nil, "", err
	}
	changes := parseRaw(raw)
	numstat, err := worktreeGit(ctx, worktree, "diff", "--cached", "--numstat", "-z", "--no-renames")
	if err != nil {
		return nil, "", err
	}
	applyNumstat(changes, numstat)
	diff, err := worktreeGit(ctx, worktree, "diff", "--cached", "--no-ext-diff", "--no-textconv", "--no-renames")
	if err != nil {
		return nil, "", err
	}
	return changes, diff, nil
}

// parseRaw reads `git diff --raw -z`: ":old new oldsha newsha status\0path\0".
func parseRaw(out string) []Change {
	var changes []Change
	parts := strings.Split(out, "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(parts[i], ":"))
		if len(meta) < 5 {
			continue
		}
		changes = append(changes, Change{Path: parts[i+1], OldMode: meta[0], NewMode: meta[1], Status: meta[4]})
	}
	return changes
}

// applyNumstat reads `git diff --numstat -z`: "added\tdeleted\tpath\0" ("-" for binary).
func applyNumstat(changes []Change, out string) {
	byPath := map[string]*Change{}
	for i := range changes {
		byPath[changes[i].Path] = &changes[i]
	}
	for _, rec := range strings.Split(out, "\x00") {
		f := strings.SplitN(rec, "\t", 3)
		if len(f) != 3 {
			continue
		}
		c := byPath[f[2]]
		if c == nil {
			continue
		}
		if f[0] == "-" || f[1] == "-" {
			c.Binary = true
			continue
		}
		a, _ := strconv.Atoi(f[0])
		d, _ := strconv.Atoi(f[1])
		c.Lines = a + d
	}
}

// deniedPrefixes are CI/CD, automation and repository-metadata paths a security
// fix never needs to touch; a change there is how an injected fixer would turn
// a pull request into code execution or secret access.
var deniedPrefixes = []string{".github/", ".gitlab/", ".circleci/", ".buildkite/", ".azure-pipelines/", ".husky/", ".devcontainer/"}

var deniedNames = map[string]bool{
	".gitlab-ci.yml": true, ".travis.yml": true, "azure-pipelines.yml": true, "jenkinsfile": true,
	"bitbucket-pipelines.yml": true, ".gitmodules": true, ".gitattributes": true, ".pre-commit-config.yaml": true,
}

// GuardPatch returns why a fix may not go to review, or "" when it may. related
// reports whether a path belongs to the finding (cited in its evidence, or a test).
func GuardPatch(changes []Change, related func(path string) bool) string {
	if len(changes) == 0 {
		return locale.Text(locale.En, "The fix changed no files")
	}
	if len(changes) > MaxPatchFiles {
		return locale.Text(locale.En, "The fix changes %d files (limit %d)", len(changes), MaxPatchFiles)
	}
	lines := 0
	for _, c := range changes {
		p := filepath.ToSlash(c.Path)
		lower := strings.ToLower(p)
		for _, prefix := range deniedPrefixes {
			if strings.HasPrefix(lower, prefix) {
				return locale.Text(locale.En, "The fix changes CI/CD or repository automation: %s", p)
			}
		}
		if deniedNames[strings.ToLower(filepath.Base(p))] || strings.Contains("/"+lower+"/", "/.git/") {
			return locale.Text(locale.En, "The fix changes CI/CD or repository metadata: %s", p)
		}
		switch {
		case c.NewMode == "120000":
			return locale.Text(locale.En, "The fix adds a symbolic link: %s", p)
		case c.NewMode == "160000" || c.OldMode == "160000":
			return locale.Text(locale.En, "The fix changes a submodule: %s", p)
		case c.NewMode == "100755" && c.OldMode != "100755":
			return locale.Text(locale.En, "The fix makes a file executable: %s", p)
		case c.Binary:
			return locale.Text(locale.En, "The fix changes a binary file: %s", p)
		}
		if related != nil && !related(p) {
			return locale.Text(locale.En, "The fix changes a file not cited in the finding and not a test: %s", p)
		}
		lines += c.Lines
	}
	if lines > MaxPatchLines {
		return locale.Text(locale.En, "The fix changes %d lines (limit %d)", lines, MaxPatchLines)
	}
	return ""
}

// RelatedToEvidence accepts paths cited in the finding text and test files.
func RelatedToEvidence(evidence string) func(string) bool {
	text := strings.ToLower(filepath.ToSlash(evidence))
	return func(p string) bool {
		lower := strings.ToLower(p)
		if strings.Contains(text, lower) {
			return true
		}
		for _, seg := range strings.Split(lower, "/") {
			if seg == "test" || seg == "tests" || seg == "spec" || seg == "__tests__" ||
				strings.HasPrefix(seg, "test_") || strings.Contains(seg, "_test.") || strings.Contains(seg, ".test.") || strings.Contains(seg, ".spec.") {
				return true
			}
		}
		return false
	}
}

// CommitFix commits the staged fix in the worktree with the given identity.
func CommitFix(ctx context.Context, worktree, message, name, email string) (string, error) {
	if _, err := worktreeGit(ctx, worktree, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := worktreeGit(ctx, worktree, "-c", "user.name="+name, "-c", "user.email="+email, "-c", "commit.gpgsign=false",
		"commit", "--no-verify", "-q", "-m", message); err != nil {
		return "", err
	}
	out, err := worktreeGit(ctx, worktree, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	if !commitSHA.MatchString(sha) {
		return "", locale.Errorf("unexpected commit id %q", sha)
	}
	return sha, nil
}

// PushFix pushes the fix branch to github.com/<owner>/<name>. The token travels
// only as a per-invocation auth header: never in the URL, remote config or logs.
func PushFix(ctx context.Context, worktree string, target Repo, branch, token string) error {
	if !fixBranch.MatchString(branch) {
		return locale.Errorf("invalid fix branch %q", branch)
	}
	cmd := newPushCmd(ctx, worktree, target, branch, token)
	if err := hardenWorktreeCmd(ctx, cmd, worktree); err != nil {
		return err
	}
	if _, err := runGitCmd(cmd, "push"); err != nil {
		// git echoes the failing command's config in some errors; never surface the header.
		return locale.Errorf("git push to %s failed", target.Slug())
	}
	return nil
}

// basicAuthConfigEnv returns the environment that gives one git process an
// Authorization header scoped to url only. Nothing secret reaches the command line.
func basicAuthConfigEnv(url, token string) []string {
	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return gitConfigEnv("http."+url+".extraheader", "AUTHORIZATION: basic "+auth)
}

// newPushCmd builds the git push of the fix branch with the token in the
// environment of that one process.
func newPushCmd(ctx context.Context, worktree string, target Repo, branch, token string) *exec.Cmd {
	url := target.CloneURL()
	return newGitCmd(ctx, worktree, basicAuthConfigEnv(url, token),
		"push", "--no-verify", "--", url, "refs/heads/"+branch+":refs/heads/"+branch)
}

// newFetchCmd builds the shallow fetch of one commit, authenticated through the
// process environment when a token is given.
func newFetchCmd(ctx context.Context, cloneDir string, repo Repo, sha, token string) *exec.Cmd {
	url := repo.CloneURL()
	var env []string
	if token != "" {
		env = basicAuthConfigEnv(url, token)
	}
	return newGitCmd(ctx, cloneDir, env, "fetch", "--no-tags", "--depth", "1", "--", url, sha)
}

var (
	noHooksOnce sync.Once
	noHooksDir  string
)

// newGitCmd builds one git command in dir with hooks, external diff drivers and
// credential prompts disabled. extraEnv is added to that process only.
func newGitCmd(ctx context.Context, dir string, extraEnv []string, args ...string) *exec.Cmd {
	noHooksOnce.Do(func() {
		noHooksDir, _ = os.MkdirTemp("", "counterweight-nohooks-")
	})
	full := append([]string{"-C", dir, "-c", "core.hooksPath=" + noHooksDir, "-c", "diff.external=", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_EXTERNAL_DIFF="), extraEnv...)
	return cmd
}

// worktreeGit runs git in a fix worktree, whose files the fixer agent wrote. See
// hardenWorktreeCmd for what that changes.
func worktreeGit(ctx context.Context, worktree string, args ...string) (string, error) {
	cmd := newGitCmd(ctx, worktree, nil, args...)
	if err := hardenWorktreeCmd(ctx, cmd, worktree); err != nil {
		return "", err
	}
	return runGitCmd(cmd, "worktree command")
}

// worktreeNeutralConfig switches off the fixed configuration keys through which git starts
// an external program for add, diff, commit or push. Content filters have names of their
// own: see worktreeFilterConfig.
var worktreeNeutralConfig = []string{
	"credential.helper=", "core.sshCommand=", "core.askPass=", "core.pager=cat", "core.editor=true",
	"core.excludesFile=", "core.fsmonitor=false", "diff.external=", "diff.guitool=",
	"protocol.ext.allow=never", "protocol.allow=never", "protocol.https.allow=always",
	"gpg.program=", "commit.gpgsign=false", "tag.gpgsign=false", "push.gpgsign=false",
}

// hardenWorktreeCmd points cmd at the git directory the server created for the worktree
// instead of letting git follow the worktree's own .git file, which the fixer could have
// rewritten, and neutralises configuration that starts external programs. A worktree whose
// .git pointer no longer names its own git directory is rejected.
func hardenWorktreeCmd(ctx context.Context, cmd *exec.Cmd, worktree string) error {
	gitDir, err := worktreeGitDir(worktree)
	if err != nil {
		return err
	}
	cmd.Env = append(cmd.Env, "GIT_DIR="+gitDir, "GIT_WORK_TREE="+worktree, "GIT_PAGER=cat", "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true")
	filters, err := worktreeFilterConfig(ctx, cmd.Env, worktree)
	if err != nil {
		return err
	}
	// Arguments of newGitCmd start with "-C", dir: the options go right after them.
	opts := make([]string, 0, 2*(len(worktreeNeutralConfig)+len(filters)))
	for _, kv := range append(slices.Clone(worktreeNeutralConfig), filters...) {
		opts = append(opts, "-c", kv)
	}
	cmd.Args = slices.Insert(cmd.Args, 3, opts...)
	return nil
}

// worktreeFilterConfig returns the settings that switch off every content filter the
// repository's own configuration defines. The worktree's attribute files stay in effect,
// because line endings depend on them, so a filter they name must have no program to
// start. Filters installed for the whole host, such as large-file storage, keep working.
func worktreeFilterConfig(ctx context.Context, env []string, worktree string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", worktree, "config", "--local", "--name-only", "-z",
		"--get-regexp", `^filter\..*\.(clean|smudge|process)$`)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, nil // no filter is configured
		}
		return nil, locale.Errorf("git %s failed: %v: %s", "config", err, strings.TrimSpace(stderr.String()))
	}
	var settings []string
	for _, key := range strings.Split(strings.TrimRight(stdout.String(), "\x00"), "\x00") {
		dot := strings.LastIndexByte(key, '.')
		// `-c` splits a setting at its first "=", so such a filter name cannot be overridden.
		if dot < 0 || strings.ContainsAny(key, "=\n") {
			return nil, locale.NewError("The repository configures a content filter that cannot be switched off")
		}
		settings = append(settings, key+"=", key[:dot]+".required=false")
	}
	return settings, nil
}

// worktreeGitDir returns the git directory of a fix worktree after checking that its .git
// file still points at it: a regular file naming a "worktrees" entry that lies outside the
// worktree (the fixer can write only inside it) and whose back-reference names this very
// file. Anything else means the pointer was replaced.
func worktreeGitDir(worktree string) (string, error) {
	tampered := locale.NewError("The fix worktree's git pointer was changed")
	dotgit := filepath.Join(worktree, ".git")
	info, err := os.Lstat(dotgit)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", tampered
	}
	raw, err := os.ReadFile(dotgit)
	if err != nil {
		return "", tampered
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	target, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "gitdir: ")
	if !ok || strings.TrimSpace(target) == "" {
		return "", tampered
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(worktree, target)
	}
	gitDir, err := filepath.EvalSymlinks(filepath.Clean(target))
	if err != nil {
		return "", tampered
	}
	realWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return "", tampered
	}
	if rel, err := filepath.Rel(realWorktree, gitDir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", tampered
	}
	if filepath.Base(filepath.Dir(gitDir)) != "worktrees" {
		return "", tampered
	}
	if st, err := os.Stat(filepath.Join(gitDir, "commondir")); err != nil || !st.Mode().IsRegular() {
		return "", tampered
	}
	back, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return "", tampered
	}
	// With worktree.useRelativePaths git writes this path relative to the git directory.
	backPath := strings.TrimSpace(string(back))
	if !filepath.IsAbs(backPath) {
		backPath = filepath.Join(gitDir, backPath)
	}
	backInfo, err := os.Stat(backPath)
	if err != nil || !os.SameFile(backInfo, info) {
		return "", tampered
	}
	return gitDir, nil
}

// runGitCmd runs cmd returning stdout; sub names the git subcommand in errors.
func runGitCmd(cmd *exec.Cmd, sub string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", locale.Errorf("git %s failed: %v: %s", sub, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// git runs one git command in dir with hooks, external diff drivers and
// credential prompts disabled, returning stdout.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	return runGitCmd(newGitCmd(ctx, dir, nil, args...), args[0])
}

// AddMergedWorktree fetches one commit of the reviewed repository (a merged pull
// request's merge commit) into the review clone and checks it out, detached, at
// dest, so a re-check reads the code as merged. token is optional: a public
// repository needs none, and it is sent only as a per-invocation header.
func AddMergedWorktree(ctx context.Context, cloneDir, dest string, repo Repo, sha, token string) error {
	if !commitSHA.MatchString(sha) {
		return locale.Errorf("invalid commit %q", sha)
	}
	if _, err := os.Stat(dest); err == nil {
		return locale.Errorf("re-check worktree %q already exists", dest)
	}
	if _, err := runGitCmd(newFetchCmd(ctx, cloneDir, repo, sha, token), "fetch"); err != nil {
		// Never surface the header that some git errors echo.
		return locale.Errorf("git fetch of %s from %s failed", sha[:12], repo.Slug())
	}
	_, err := git(ctx, cloneDir, "worktree", "add", "--detach", "--", dest, sha)
	return err
}
