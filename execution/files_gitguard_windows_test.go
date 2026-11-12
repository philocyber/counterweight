package execution

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

// shortPath returns the 8.3 form of an existing path, or the path unchanged
// when the volume does not generate short names.
func shortPath(t *testing.T, path string) string {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 1024)
	n, err := syscall.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return path
	}
	return syscall.UTF16ToString(buf[:n])
}

func TestConfinedWriteRefusesGitShortNames(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("git marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitFile := filepath.Join(root, "sub")
	if err := os.Mkdir(gitFile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitFile, ".git"), []byte("gitdir: pointer"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirShort := filepath.Base(shortPath(t, gitDir))
	fileShort := filepath.Base(shortPath(t, filepath.Join(gitFile, ".git")))
	if strings.EqualFold(dirShort, ".git") || strings.EqualFold(fileShort, ".git") {
		t.Skip("the test volume does not generate 8.3 short names")
	}
	files, err := Open(Roots{Read: []string{root}, Write: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	for _, tc := range []struct{ tool, path string }{
		{"Write", filepath.Join(dirShort, "config")},
		{"Write", filepath.Join(dirShort, "hooks", "post-commit")},
		{"Write", filepath.Join("sub", fileShort)},
		{"Edit", filepath.Join(dirShort, "config")},
		{"Edit", filepath.Join("sub", fileShort)},
		{"MultiEdit", filepath.Join(dirShort, "config")},
		{"MultiEdit", filepath.Join("sub", fileShort)},
		{"Write", strings.ToLower(filepath.Join(dirShort, "config"))},
	} {
		in := map[string]any{"file_path": tc.path, "content": "bad", "old_string": "gitdir", "new_string": "bad", "edits": []map[string]any{{"old_string": "git", "new_string": "bad"}}}
		var tool actool.CoreTool = actool.NewWrite()
		switch tc.tool {
		case "Edit":
			tool = actool.NewEdit()
		case "MultiEdit":
			tool = actool.NewMultiEdit()
		}
		if r := callFile(t, files.Tool(tool), root, in); !r.IsError {
			t.Errorf("%s through short name %q was accepted", tc.tool, tc.path)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(gitDir, "config")); string(b) != "git marker" {
		t.Error("git config was modified")
	}
	if b, _ := os.ReadFile(filepath.Join(gitFile, ".git")); string(b) != "gitdir: pointer" {
		t.Error("git pointer file was modified")
	}
	if _, err := os.Stat(filepath.Join(gitDir, "hooks")); err == nil {
		t.Error("directory created inside .git")
	}
	if r := callFile(t, files.Tool(actool.NewWrite()), root, map[string]any{"file_path": "ordinary~1.txt", "content": "ok"}); r.IsError {
		t.Fatalf("ordinary tilde name refused: %s", r.Flatten())
	}
}

func TestConfinedWriteRefusesWindowsGitSpellings(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := Open(Roots{Read: []string{root}, Write: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	for _, name := range []string{".GIT", ".Git", ".git.", ".git ", ".git. .", ".git::$INDEX_ALLOCATION", ".git:stream"} {
		for _, p := range []string{filepath.Join(name, "config"), name, filepath.Join("a", name, "x")} {
			if r := callFile(t, files.Tool(actool.NewWrite()), root, map[string]any{"file_path": p, "content": "bad"}); !r.IsError {
				t.Errorf("write to %q accepted", p)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "config")); err == nil {
		t.Error("file created inside .git")
	}
}
