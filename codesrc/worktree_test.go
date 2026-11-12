package codesrc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "fixture@example.invalid"},
		{"config", "user.name", "fixture"},
	} {
		if _, err := git(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "db.py"), []byte("q = 'SELECT * FROM t WHERE id=' + uid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "init"}} {
		if _, err := git(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestFixWorktreeCollectsServerSideDiff(t *testing.T) {
	clone := gitFixture(t)
	wt := filepath.Join(t.TempDir(), "fix-7")
	branch := FixBranchName(7, 3)
	base, err := AddFixWorktree(t.Context(), clone, wt, branch)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := HeadCommit(t.Context(), clone)
	if base != head {
		t.Fatalf("base=%s want clone HEAD %s", base, head)
	}
	if _, err := AddFixWorktree(t.Context(), clone, wt, branch); err == nil {
		t.Fatal("existing worktree reused")
	}
	if _, err := AddFixWorktree(t.Context(), clone, filepath.Join(t.TempDir(), "x"), "main"); err == nil {
		t.Fatal("non-fix branch accepted")
	}

	if err := os.WriteFile(filepath.Join(wt, "app", "db.py"), []byte("q = 'SELECT * FROM t WHERE id=%s'\nparams = (uid,)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "tests", "test_db.py"), []byte("def test_param(): pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, diff, err := CollectChanges(t.Context(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || !strings.Contains(diff, "params = (uid,)") || !strings.Contains(diff, "tests/test_db.py") {
		t.Fatalf("changes=%+v diff=%s", changes, diff)
	}
	if blocked := GuardPatch(changes, RelatedToEvidence("app/db.py:1 concatenates uid into SQL")); blocked != "" {
		t.Fatalf("legitimate fix blocked: %s", blocked)
	}

	if err := os.MkdirAll(filepath.Join(wt, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".github", "workflows", "ci.yml"), []byte("on: push\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, _, err = CollectChanges(t.Context(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if blocked := GuardPatch(changes, RelatedToEvidence("app/db.py:1")); !strings.Contains(blocked, ".github/workflows/ci.yml") {
		t.Fatalf("workflow change not blocked: %q", blocked)
	}
}

func TestGuardPatchRules(t *testing.T) {
	ok := Change{Path: "app/db.py", Status: "M", OldMode: "100644", NewMode: "100644", Lines: 4}
	all := func(string) bool { return true }
	many := make([]Change, MaxPatchFiles+1)
	for i := range many {
		many[i] = ok
	}
	for name, tc := range map[string]struct {
		changes []Change
		related func(string) bool
		want    string
	}{
		"empty":      {nil, all, "no files"},
		"too many":   {many, all, "files"},
		"too long":   {[]Change{{Path: "a.py", NewMode: "100644", OldMode: "100644", Lines: MaxPatchLines + 1}}, all, "lines"},
		"symlink":    {[]Change{{Path: "link", NewMode: "120000", OldMode: "000000"}}, all, "symbolic link"},
		"submodule":  {[]Change{{Path: "vendor/x", NewMode: "160000", OldMode: "000000"}}, all, "submodule"},
		"executable": {[]Change{{Path: "run.sh", NewMode: "100755", OldMode: "100644"}}, all, "executable"},
		"binary":     {[]Change{{Path: "a.bin", NewMode: "100644", OldMode: "100644", Binary: true}}, all, "binary"},
		"gitlab ci":  {[]Change{{Path: ".gitlab-ci.yml", NewMode: "100644", OldMode: "100644"}}, all, "CI/CD"},
		"attributes": {[]Change{{Path: "sub/.gitattributes", NewMode: "100644", OldMode: "000000"}}, all, "metadata"},
		"unrelated":  {[]Change{ok}, RelatedToEvidence("handlers/user.py:9"), "not cited"},
		"cited":      {[]Change{ok}, RelatedToEvidence("Trace: APP/DB.PY:1 → sink"), ""},
		"test file":  {[]Change{{Path: "pkg/db_test.go", NewMode: "100644", OldMode: "000000"}}, RelatedToEvidence("x"), ""},
	} {
		got := GuardPatch(tc.changes, tc.related)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: GuardPatch=%q want containing %q", name, got, tc.want)
		}
	}
}

func TestAddMergedWorktreeRefusesBadCommit(t *testing.T) {
	repo, _ := ParseRepo("acme/fixture")
	for _, sha := range []string{"", "HEAD", "--upload-pack=x", "0123456789abcdef0123456789abcdef0123456"} {
		if err := AddMergedWorktree(t.Context(), t.TempDir(), filepath.Join(t.TempDir(), "wt"), repo, sha, ""); err == nil {
			t.Fatalf("accepted commit %q", sha)
		}
	}
}
