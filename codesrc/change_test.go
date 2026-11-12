package codesrc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// changeGit runs git in dir with a fixed identity and no user or system
// configuration, so the fixtures behave the same on every machine.
func changeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "core.autocrlf=false", "-c", "protocol.file.allow=always"}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func changeEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func writeLines(t *testing.T, path string, n int, edits map[int]string) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if s, ok := edits[i]; ok {
			b.WriteString(s + "\n")
		} else {
			fmt.Fprintf(&b, "line %d\n", i)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeText(t *testing.T, path, word string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s %d\n", word, i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// divergedRepo builds main (base tip moved on after the branch point) and a
// feature branch that adds, modifies, deletes, renames and changes a binary.
func divergedRepo(t *testing.T) (dir, base, head string) {
	t.Helper()
	changeEnv(t)
	dir = t.TempDir()
	changeGit(t, dir, "init", "-q", "-b", "main")
	writeLines(t, filepath.Join(dir, "app", "a.txt"), 12, nil)
	writeText(t, filepath.Join(dir, "gone.txt"), "to be removed", 5)
	writeLines(t, filepath.Join(dir, "old_name.txt"), 20, nil)
	writeLines(t, filepath.Join(dir, "other.txt"), 5, nil)
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("A\x00B\x00old"), 0o644); err != nil {
		t.Fatal(err)
	}
	changeGit(t, dir, "add", "-A")
	changeGit(t, dir, "commit", "-q", "-m", "init")
	changeGit(t, dir, "checkout", "-q", "-b", "feature")

	writeLines(t, filepath.Join(dir, "app", "a.txt"), 12, map[int]string{3: "changed 3", 8: "changed 8", 9: "changed 9"})
	writeText(t, filepath.Join(dir, "added.txt"), "brand new", 4)
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "old_name.txt"), filepath.Join(dir, "new_name.txt")); err != nil {
		t.Fatal(err)
	}
	writeLines(t, filepath.Join(dir, "new_name.txt"), 20, map[int]string{10: "edited after rename"})
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("A\x00B\x00new"), 0o644); err != nil {
		t.Fatal(err)
	}
	changeGit(t, dir, "add", "-A")
	changeGit(t, dir, "commit", "-q", "-m", "feature")
	head = changeGit(t, dir, "rev-parse", "HEAD")

	changeGit(t, dir, "checkout", "-q", "main")
	writeLines(t, filepath.Join(dir, "other.txt"), 5, map[int]string{2: "main moved on"})
	changeGit(t, dir, "add", "-A")
	changeGit(t, dir, "commit", "-q", "-m", "main moves")
	base = changeGit(t, dir, "rev-parse", "HEAD")
	return dir, base, head
}

func fileOf(c *ChangeSet, path string) *ChangedFile {
	for i := range c.Files {
		if c.Files[i].Path == path {
			return &c.Files[i]
		}
	}
	return nil
}

func TestComputeChangeKinds(t *testing.T) {
	dir, base, head := divergedRepo(t)
	c, err := ComputeChange(t.Context(), dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if c.HeadCommit != head || c.BaseCommit != base || c.MergeBase == base || c.MergeBase == "" {
		t.Fatalf("commits: %+v", c)
	}
	// The diverged base moved other.txt; merge-base semantics must not show it.
	if fileOf(c, "other.txt") != nil {
		t.Fatalf("a change made on the base after the branch point leaked into the list: %+v", c.Files)
	}
	if c.TotalFiles != 5 || len(c.Files) != 5 || c.FilesTruncated || c.DiffTruncated {
		t.Fatalf("files=%d total=%d: %+v", len(c.Files), c.TotalFiles, c.Files)
	}

	a := fileOf(c, "app/a.txt")
	if a == nil || a.Status != "modified" || len(a.Ranges) != 2 || a.Ranges[0] != (LineRange{3, 3}) || a.Ranges[1] != (LineRange{8, 9}) {
		t.Fatalf("modified file: %+v", a)
	}
	if add := fileOf(c, "added.txt"); add == nil || add.Status != "added" || len(add.Ranges) != 1 || add.Ranges[0] != (LineRange{1, 4}) {
		t.Fatalf("added file: %+v", add)
	}
	if del := fileOf(c, "gone.txt"); del == nil || del.Status != "deleted" || len(del.Ranges) != 0 {
		t.Fatalf("deleted file: %+v", del)
	}
	ren := fileOf(c, "new_name.txt")
	if ren == nil || ren.Status != "renamed" || ren.OldPath != "old_name.txt" || len(ren.Ranges) != 1 || ren.Ranges[0] != (LineRange{10, 10}) {
		t.Fatalf("renamed file: %+v", ren)
	}
	if bin := fileOf(c, "blob.bin"); bin == nil || !bin.Binary || len(bin.Ranges) != 0 || bin.Whole {
		t.Fatalf("binary file: %+v", bin)
	}
	if !strings.Contains(c.Diff, "+changed 3") || strings.Contains(c.Diff, "main moved on") {
		t.Fatalf("diff text: %q", c.Diff)
	}

	// Classification.
	for _, tc := range []struct {
		file       string
		start, end int
		want       bool
	}{
		{"app/a.txt", 3, 3, true},
		{"./app/a.txt", 2, 4, true}, // overlaps line 3
		{"app/a.txt", 4, 7, false},
		{"app/a.txt", 9, 20, true},
		{"added.txt", 2, 2, true},
		{"new_name.txt", 10, 10, true},
		{"new_name.txt", 11, 12, false},
		{"old_name.txt", 10, 10, false}, // head-side path only
		{"gone.txt", 1, 1, false},
		{"blob.bin", 1, 1, false},
		{"untouched.go", 1, 1, false},
		{"", 1, 1, false},
	} {
		if got := c.Contains(tc.file, tc.start, tc.end); got != tc.want {
			t.Errorf("Contains(%q,%d,%d)=%v want %v", tc.file, tc.start, tc.end, got, tc.want)
		}
	}
}

func TestComputeChangePureDeletionCountsAsAdjacent(t *testing.T) {
	changeEnv(t)
	dir := t.TempDir()
	changeGit(t, dir, "init", "-q", "-b", "main")
	writeLines(t, filepath.Join(dir, "v.txt"), 10, nil)
	changeGit(t, dir, "add", "-A")
	changeGit(t, dir, "commit", "-q", "-m", "init")
	base := changeGit(t, dir, "rev-parse", "HEAD")
	var kept strings.Builder
	for i := 1; i <= 10; i++ {
		if i != 5 && i != 6 { // a removed validation
			fmt.Fprintf(&kept, "line %d\n", i)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "v.txt"), []byte(kept.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	changeGit(t, dir, "commit", "-q", "-am", "drop")
	head := changeGit(t, dir, "rev-parse", "HEAD")
	c, err := ComputeChange(t.Context(), dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	f := fileOf(c, "v.txt")
	if f == nil || len(f.Ranges) != 1 || f.Ranges[0] != (LineRange{4, 5}) {
		t.Fatalf("pure deletion: %+v", f)
	}
}

func TestComputeChangeCapsFilesAndDiff(t *testing.T) {
	changeEnv(t)
	dir := t.TempDir()
	changeGit(t, dir, "init", "-q", "-b", "main")
	writeLines(t, filepath.Join(dir, "seed.txt"), 1, nil)
	changeGit(t, dir, "add", "-A")
	changeGit(t, dir, "commit", "-q", "-m", "init")
	base := changeGit(t, dir, "rev-parse", "HEAD")
	for i := 0; i < MaxChangedFiles+20; i++ {
		writeLines(t, filepath.Join(dir, "many", fmt.Sprintf("f%03d.txt", i)), 40, nil)
	}
	changeGit(t, dir, "add", "-A")
	changeGit(t, dir, "commit", "-q", "-m", "many")
	head := changeGit(t, dir, "rev-parse", "HEAD")

	c, err := ComputeChange(t.Context(), dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if c.TotalFiles != MaxChangedFiles+20 || len(c.Files) != MaxChangedFiles || !c.FilesTruncated || !c.DiffTruncated {
		t.Fatalf("total=%d kept=%d filesTrunc=%v diffTrunc=%v", c.TotalFiles, len(c.Files), c.FilesTruncated, c.DiffTruncated)
	}
	if len(c.Diff) > MaxDiffBytes || !strings.HasSuffix(c.Diff, "\n") {
		t.Fatalf("diff size %d, ends %q", len(c.Diff), c.Diff[max(0, len(c.Diff)-10):])
	}
	// A listed file keeps exact ranges; an unlisted one cannot be told apart, so it counts as inside.
	if !c.Contains("many/f000.txt", 40, 40) || c.Contains("many/f000.txt", 41, 41) {
		t.Fatal("listed file ranges are wrong")
	}
	if !c.Contains("never/listed.go", 1, 1) {
		t.Fatal("a file outside a truncated list must count as inside")
	}
}

func TestComputeChangeDiffCapMarksFilesWhole(t *testing.T) {
	changeEnv(t)
	dir := t.TempDir()
	changeGit(t, dir, "init", "-q", "-b", "main")
	writeLines(t, filepath.Join(dir, "seed.txt"), 1, nil)
	changeGit(t, dir, "add", "-A")
	changeGit(t, dir, "commit", "-q", "-m", "init")
	base := changeGit(t, dir, "rev-parse", "HEAD")
	// More patch text than the scan cap: later files cannot be ranged.
	saved := maxPatchScanBytes
	t.Cleanup(func() { maxPatchScanBytes = saved })
	maxPatchScanBytes = 2000
	for i := 0; i < 10; i++ {
		writeLines(t, filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), 60, nil)
	}
	changeGit(t, dir, "add", "-A")
	changeGit(t, dir, "commit", "-q", "-m", "files")
	head := changeGit(t, dir, "rev-parse", "HEAD")
	c, err := ComputeChange(t.Context(), dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if first := fileOf(c, "f0.txt"); first == nil || first.Whole || len(first.Ranges) != 1 {
		t.Fatalf("first file: %+v", first)
	}
	last := fileOf(c, "f9.txt")
	if last == nil || !last.Whole || !c.Contains("f9.txt", 7, 7) {
		t.Fatalf("a file beyond the scan cap must count as wholly changed: %+v", last)
	}
}

// A repository that declares an external diff driver or a textconv filter must
// not get it executed by the change computation.
func TestComputeChangeNeverRunsRepositoryPrograms(t *testing.T) {
	changeEnv(t)
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "evil.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran >> '"+filepath.ToSlash(marker)+"'\ncat \"$1\" 2>/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	changeGit(t, dir, "init", "-q", "-b", "main")
	writeLines(t, filepath.Join(dir, "x.txt"), 5, nil)
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt diff=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changeGit(t, dir, "add", "-A")
	changeGit(t, dir, "commit", "-q", "-m", "init")
	base := changeGit(t, dir, "rev-parse", "HEAD")
	writeLines(t, filepath.Join(dir, "x.txt"), 5, map[int]string{2: "changed"})
	changeGit(t, dir, "commit", "-q", "-am", "edit")
	head := changeGit(t, dir, "rev-parse", "HEAD")

	cmd := "sh '" + filepath.ToSlash(script) + "'"
	changeGit(t, dir, "config", "diff.evil.command", cmd)
	changeGit(t, dir, "config", "diff.evil.textconv", cmd)
	changeGit(t, dir, "config", "core.fsmonitor", cmd)
	// GIT_EXTERNAL_DIFF in the process environment must not apply either.
	t.Setenv("GIT_EXTERNAL_DIFF", script)

	// Control: plain git does run the driver here. If it does not on this
	// machine the test cannot prove anything.
	exec.Command("git", "-C", dir, "diff", base, head).Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("the control diff did not run the configured driver on this platform")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	c, err := ComputeChange(t.Context(), dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repository-declared diff driver, textconv or fsmonitor program was executed")
	}
	if f := fileOf(c, "x.txt"); f == nil || len(f.Ranges) != 1 || f.Ranges[0] != (LineRange{2, 2}) {
		t.Fatalf("x.txt: %+v", f)
	}
}

// originWithPull builds an origin that has a base branch which moved on, a
// pull request head ref, and a shallow clone of the base's default branch.
func originWithPull(t *testing.T) (origin, clone, prHead string) {
	t.Helper()
	changeEnv(t)
	origin = t.TempDir()
	changeGit(t, origin, "init", "-q", "-b", "main")
	for i := 1; i <= 3; i++ {
		writeLines(t, filepath.Join(origin, "app.txt"), 6, map[int]string{i: fmt.Sprintf("main %d", i)})
		changeGit(t, origin, "add", "-A")
		changeGit(t, origin, "commit", "-q", "-m", fmt.Sprintf("c%d", i))
		if i == 1 {
			changeGit(t, origin, "checkout", "-q", "-b", "topic")
			writeLines(t, filepath.Join(origin, "feature.txt"), 3, nil)
			writeLines(t, filepath.Join(origin, "app.txt"), 6, map[int]string{1: "main 1", 5: "topic edit"})
			changeGit(t, origin, "add", "-A")
			changeGit(t, origin, "commit", "-q", "-m", "topic")
			prHead = changeGit(t, origin, "rev-parse", "HEAD")
			changeGit(t, origin, "update-ref", "refs/pull/7/head", prHead)
			changeGit(t, origin, "checkout", "-q", "main")
		}
	}
	clone = filepath.Join(t.TempDir(), "src")
	changeGit(t, t.TempDir(), "clone", "-q", "--depth", "1", "--no-tags", "file://"+filepath.ToSlash(origin), clone)
	return origin, clone, prHead
}

func TestFetchChangePullRequestIntoShallowClone(t *testing.T) {
	origin, clone, prHead := originWithPull(t)
	if out := changeGit(t, clone, "rev-parse", "--is-shallow-repository"); out != "true" {
		t.Fatalf("fixture clone is not shallow: %s", out)
	}
	var calls [][]string
	run := func(ctx context.Context, _ string, args []string, env []string, dir string) error {
		calls = append(calls, append([]string(nil), args...))
		for i, a := range args {
			if a == "https://github.com/acme/example.git" {
				args = append([]string(nil), args...)
				args[i] = "file://" + filepath.ToSlash(origin)
			}
		}
		_, err := runGitCmd(newGitCmd(ctx, dir, env, append([]string{"-c", "protocol.file.allow=always"}, args...)...), "fetch")
		return err
	}
	c, err := FetchChange(t.Context(), FetchChangeOptions{
		Repo: Repo{Owner: "acme", Name: "example"}, Dir: clone,
		Request: ChangeRequest{PullNumber: 7, Base: "main"}, Run: run,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.HeadCommit != prHead || c.PullNumber != 7 || c.HeadRef != "pull/7" || c.BaseRef != "main" {
		t.Fatalf("change: %+v", c)
	}
	if head := changeGit(t, clone, "rev-parse", "HEAD"); head != prHead {
		t.Fatalf("head not checked out: %s", head)
	}
	if _, err := os.Stat(filepath.Join(clone, "feature.txt")); err != nil {
		t.Fatal("the head's files are not in the working tree")
	}
	if f := fileOf(c, "feature.txt"); f == nil || f.Status != "added" {
		t.Fatalf("files: %+v", c.Files)
	}
	if f := fileOf(c, "app.txt"); f == nil || len(f.Ranges) != 1 || f.Ranges[0] != (LineRange{5, 5}) {
		t.Fatalf("app.txt: %+v", f)
	}
	if len(calls) == 0 || calls[0][0] != "fetch" || !contains(calls[0], "+refs/pull/7/head:refs/counterweight/head") || !contains(calls[0], "--no-tags") {
		t.Fatalf("fetch args: %v", calls)
	}
}

func TestFetchChangeDeepensWhenAncestorIsFarAway(t *testing.T) {
	origin, clone, prHead := originWithPull(t)
	savedDepth, savedDeepen := fetchDepth, deepenBy
	t.Cleanup(func() { fetchDepth, deepenBy = savedDepth, savedDeepen })
	fetchDepth, deepenBy = 1, 50
	var calls int
	run := func(ctx context.Context, _ string, args []string, env []string, dir string) error {
		calls++
		args = append([]string(nil), args...)
		for i, a := range args {
			if strings.HasPrefix(a, "https://github.com/") {
				args[i] = "file://" + filepath.ToSlash(origin)
			}
		}
		_, err := runGitCmd(newGitCmd(ctx, dir, env, append([]string{"-c", "protocol.file.allow=always"}, args...)...), "fetch")
		return err
	}
	c, err := FetchChange(t.Context(), FetchChangeOptions{
		Repo: Repo{Owner: "acme", Name: "example"}, Dir: clone,
		Request: ChangeRequest{Base: "main", Head: "topic"}, Run: run,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("fetch calls = %d, want the shallow fetch and one deepening", calls)
	}
	if c.HeadCommit != prHead || c.HeadRef != "topic" || c.PullNumber != 0 {
		t.Fatalf("change: %+v", c)
	}
}

func TestFetchChangeFailureDoesNotEchoTheToken(t *testing.T) {
	_, clone, _ := originWithPull(t)
	run := func(_ context.Context, _ string, _ []string, env []string, _ string) error {
		return fmt.Errorf("fatal: failed with %s", strings.Join(env, " "))
	}
	_, err := FetchChange(t.Context(), FetchChangeOptions{
		Repo: Repo{Owner: "acme", Name: "example"}, Dir: clone, Token: "ghp_secret_value",
		Request: ChangeRequest{PullNumber: 7, Base: "main"}, Run: run,
	})
	if err == nil || strings.Contains(err.Error(), "ghp_secret_value") {
		t.Fatalf("err = %v", err)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestParseChangeTarget(t *testing.T) {
	for _, tc := range []struct {
		repo, rng string
		wantRepo  string
		want      *ChangeRequest
		wantErr   bool
	}{
		{repo: "acme/example", wantRepo: "acme/example"},
		{repo: "acme/example#123", wantRepo: "acme/example", want: &ChangeRequest{PullNumber: 123}},
		{repo: "https://github.com/acme/example/pull/45", wantRepo: "https://github.com/acme/example", want: &ChangeRequest{PullNumber: 45}},
		{repo: "acme/example#release-1", wantRepo: "acme/example#release-1"}, // a ref, not a pull request
		{repo: "acme/example", rng: "main..feature/x", wantRepo: "acme/example", want: &ChangeRequest{Base: "main", Head: "feature/x"}},
		{repo: "acme/example", rng: "main...topic", wantRepo: "acme/example", want: &ChangeRequest{Base: "main", Head: "topic"}},
		{repo: "acme/example#5", rng: "a..b", wantErr: true},
		{repo: "acme/example", rng: "onlybase..", wantErr: true},
		{repo: "acme/example", rng: "nodots", wantErr: true},
		{repo: "acme/example", rng: "-x..y", wantErr: true},
		{repo: "acme/example#0", wantErr: true},
		{repo: "acme/example#99999999999", wantRepo: "acme/example#99999999999"},
	} {
		gotRepo, got, err := ParseChangeTarget(tc.repo, tc.rng)
		if (err != nil) != tc.wantErr {
			t.Errorf("%q %q: err=%v", tc.repo, tc.rng, err)
			continue
		}
		if err != nil {
			continue
		}
		if gotRepo != tc.wantRepo || (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%q %q: got %q %+v want %q %+v", tc.repo, tc.rng, gotRepo, got, tc.wantRepo, tc.want)
		}
	}
}

func TestChangeRequestValidate(t *testing.T) {
	for _, bad := range []ChangeRequest{{}, {Base: "main"}, {Head: "x"}, {PullNumber: -1}, {PullNumber: 1, Base: "--upload-pack=x"}, {Base: "a b", Head: "c"}} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	for _, ok := range []ChangeRequest{{PullNumber: 3}, {PullNumber: 3, Base: "release/1.x"}, {Base: "main", Head: strings.Repeat("a", 40)}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
}
