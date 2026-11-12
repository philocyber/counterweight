package codesrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixWorktreeFixture(t *testing.T, name string) (clone, wt string) {
	t.Helper()
	clone = gitFixture(t)
	wt = filepath.Join(t.TempDir(), name)
	if _, err := AddFixWorktree(t.Context(), clone, wt, FixBranchName(7, 3)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "app", "db.py"), []byte("q = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return clone, wt
}

// A worktree whose .git pointer was rewritten is rejected, and nothing named by the
// replacement repository is started.
func TestWorktreeGitCommandsRejectTamperedPointer(t *testing.T) {
	_, wt := fixWorktreeFixture(t, "fix-7")
	if _, _, err := CollectChanges(t.Context(), wt); err != nil {
		t.Fatalf("untouched worktree: %v", err)
	}
	original, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "started")

	// A repository the fixer authored inside the worktree, with a back-reference that
	// names the worktree's own .git file and a configuration that starts a program.
	evil := filepath.Join(wt, "evil", "worktrees", "x")
	if err := os.MkdirAll(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"HEAD":      "ref: refs/heads/main\n",
		"commondir": "..\n",
		"gitdir":    filepath.Join(wt, ".git") + "\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(evil, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := "[core]\n\tfsmonitor = touch " + filepath.ToSlash(marker) + "\n[filter \"x\"]\n\tclean = touch " + filepath.ToSlash(marker) + "\n"
	if err := os.WriteFile(filepath.Join(wt, "evil", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".gitattributes"), []byte("* filter=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, pointer := range map[string]string{
		"inside the worktree": "gitdir: " + evil + "\n",
		"another worktree":    "gitdir: " + filepath.Join(filepath.Dir(strings.TrimSpace(strings.TrimPrefix(string(original), "gitdir: "))), "missing") + "\n",
		"not a pointer":       "something else\n",
		"empty":               "",
	} {
		if err := os.WriteFile(filepath.Join(wt, ".git"), []byte(pointer), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := CollectChanges(t.Context(), wt); err == nil {
			t.Errorf("%s: pointer accepted by CollectChanges", name)
		}
		if _, err := CommitFix(t.Context(), wt, "msg", "n", "n@example.invalid"); err == nil {
			t.Errorf("%s: pointer accepted by CommitFix", name)
		}
		if err := PushFix(t.Context(), wt, Repo{Owner: "o", Name: "r"}, FixBranchName(7, 3), "tok"); err == nil || strings.Contains(err.Error(), "failed") && !strings.Contains(err.Error(), "pointer") {
			t.Errorf("%s: pointer accepted by PushFix: %v", name, err)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a program named by the planted repository was started")
	}

	// A directory in place of the file is rejected as well; the original pointer works again.
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(wt, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CollectChanges(t.Context(), wt); err == nil {
		t.Error(".git directory accepted")
	}
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CollectChanges(t.Context(), wt); err != nil {
		t.Fatalf("restored pointer: %v", err)
	}
}

// Another fix worktree's git directory is not accepted for this worktree.
func TestWorktreeGitDirMustBelongToTheWorktree(t *testing.T) {
	clone, wt := fixWorktreeFixture(t, "fix-7")
	other := filepath.Join(t.TempDir(), "fix-8")
	if _, err := AddFixWorktree(t.Context(), clone, other, FixBranchName(8, 4)); err != nil {
		t.Fatal(err)
	}
	foreign, err := os.ReadFile(filepath.Join(other, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CollectChanges(t.Context(), wt); err == nil {
		t.Fatal("another worktree's git directory was accepted")
	}
}

// Configuration that starts programs is switched off even when the repository's own
// configuration asks for it.
func TestWorktreeGitIgnoresProgramConfiguration(t *testing.T) {
	clone, wt := fixWorktreeFixture(t, "fix-7")
	marker := filepath.Join(t.TempDir(), "started")
	script := "touch " + filepath.ToSlash(marker)
	for _, kv := range [][2]string{{"core.fsmonitor", script}, {"filter.x.clean", script}, {"filter.x.process", script}, {"filter.x.required", "true"}, {"diff.external", script}} {
		if _, err := git(t.Context(), clone, "config", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wt, ".gitattributes"), []byte("* filter=x diff=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CollectChanges(t.Context(), wt); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("configured program was started")
	}
}

// Line-ending attributes of the worktree still apply to what the server stages.
func TestWorktreeGitKeepsLineEndingAttributes(t *testing.T) {
	_, wt := fixWorktreeFixture(t, "fix-7")
	for name, body := range map[string]string{".gitattributes": "*.py text eol=crlf\n", "app/db.py": "q = 1\r\nr = 2\r\n"} {
		if err := os.WriteFile(filepath.Join(wt, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := CollectChanges(t.Context(), wt); err != nil {
		t.Fatal(err)
	}
	staged, err := worktreeGit(t.Context(), wt, "cat-file", "blob", ":app/db.py")
	if err != nil || staged != "q = 1\nr = 2\n" {
		t.Fatalf("staged with other line endings: %q %v", staged, err)
	}
}
