package execution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func callFile(t *testing.T, tool actool.CoreTool, cwd string, in map[string]any) actool.Result {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	r, err := tool.Call(context.Background(), raw, &actool.ToolContext{WorkingDir: cwd})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestConfinedTools(t *testing.T) {
	for _, sdk := range []actool.CoreTool{actool.NewRead(), actool.NewLS(), actool.NewGlob(), actool.NewGrep(), actool.NewWrite(), actool.NewEdit(), actool.NewMultiEdit()} {
		t.Run(sdk.Name(), func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "workspace")
			outside := filepath.Join(base, "outside")
			for _, p := range []string{root, outside} {
				if err := os.Mkdir(p, 0755); err != nil {
					t.Fatal(err)
				}
			}
			for _, p := range []string{filepath.Join(root, "file.txt"), filepath.Join(outside, "file.txt")} {
				if err := os.WriteFile(p, []byte("old marker\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			f, err := Open(Roots{Read: []string{root}, Write: []string{root}})
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			tool := f.Tool(sdk)
			input := func(p string) map[string]any {
				in := map[string]any{"file_path": p, "path": p, "pattern": "*", "content": "new", "old_string": "old", "new_string": "new", "edits": []map[string]any{{"old_string": "old", "new_string": "new"}}}
				if sdk.Name() == "Grep" {
					in["pattern"] = "marker"
				}
				return in
			}
			inside := "file.txt"
			dirTool := sdk.Name() == "LS" || sdk.Name() == "Glob" || sdk.Name() == "Grep"
			if dirTool {
				inside = "."
			}
			r := callFile(t, tool, root, input(inside))
			if r.IsError {
				t.Fatalf("inside: %s", r.Flatten())
			}
			routes := []string{outside, filepath.Join("..", "outside")}
			if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
				t.Logf("SKIP symbolic link escape: %v", err)
			} else {
				routes = append(routes, "link")
			}
			for _, route := range routes {
				p := route
				if !dirTool {
					p = filepath.Join(route, "file.txt")
				}
				r = callFile(t, tool, root, input(p))
				if !r.IsError || strings.Contains(r.Flatten(), "old marker") {
					t.Fatalf("escape %q: %+v", route, r)
				}
			}
			b, err := os.ReadFile(filepath.Join(outside, "file.txt"))
			if err != nil || string(b) != "old marker\n" {
				t.Fatal("outside marker changed")
			}
			if sdk.Name() == "Read" {
				r = callFile(t, tool, root, input(filepath.Join(root, "file.txt")))
				if r.IsError || !strings.Contains(r.Flatten(), "1\t") {
					t.Fatal("absolute inside or numbering failed")
				}
			}
		})
	}
}
func TestConfinedReadOnlyAndMissingRoots(t *testing.T) {
	root := t.TempDir()
	f, err := Open(Roots{Read: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !callFile(t, f.Tool(actool.NewWrite()), root, map[string]any{"file_path": "new.txt", "content": "no"}).IsError {
		t.Fatal("read root allowed write")
	}
	if _, err = Open(Roots{Read: []string{filepath.Join(root, "missing")}}); err == nil {
		t.Fatal("missing root accepted")
	}
}
func TestConfinedHardLinkBehavior(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("hardlink marker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "alias")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	f, err := Open(Roots{Read: []string{root}, Write: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := callFile(t, f.Tool(actool.NewRead()), root, map[string]any{"file_path": "alias"})
	if r.IsError || !strings.Contains(r.Flatten(), "hardlink marker") {
		t.Fatalf("os.Root hard link behavior changed: %s", r.Flatten())
	}
	r = callFile(t, f.Tool(actool.NewWrite()), root, map[string]any{"file_path": "alias", "content": "changed"})
	if r.IsError {
		t.Fatal(r.Flatten())
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "changed" {
		t.Fatal("expected os.Root hard link alias to share the inode")
	}
	t.Log("os.Root permits pre-existing hard links: reading/writing the alias accesses the same outside inode")
}
func TestExecutionSelfTest(t *testing.T) {
	skipped, err := SelfTest(nil)
	for _, s := range skipped {
		t.Log(s)
	}
	if err != nil {
		t.Fatal(err)
	}
	_, err = SelfTest(func(tool actool.CoreTool) actool.CoreTool {
		if tool.Name() == "Read" {
			return actool.NewRead()
		}
		return actool.NewWrite()
	})
	if err == nil {
		t.Fatal("unconfined tools passed self-test")
	}
	c := NewController()
	if c.Status(true).Status != "ready" || c.Status(false).Status != "off" {
		t.Fatal("wrong status")
	}
}

func TestConfinedInRootLinksAndGitAlias(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("git marker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(root, "alias")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if err := os.Symlink(".git", filepath.Join(root, "git-alias")); err != nil {
		t.Fatal(err)
	}
	files, err := Open(Roots{Read: []string{root}, Write: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	if r := callFile(t, files.Tool(actool.NewRead()), root, map[string]any{"file_path": "alias"}); r.IsError {
		t.Fatal(r.Flatten())
	}
	if r := callFile(t, files.Tool(actool.NewWrite()), root, map[string]any{"file_path": "alias", "content": "updated"}); r.IsError {
		t.Fatal(r.Flatten())
	}
	if r := callFile(t, files.Tool(actool.NewWrite()), root, map[string]any{"file_path": filepath.Join("git-alias", "config"), "content": "bad"}); !r.IsError {
		t.Fatal("git alias was writable")
	}
	b, _ := os.ReadFile(filepath.Join(root, ".git", "config"))
	if string(b) != "git marker" {
		t.Fatal("git metadata changed")
	}
}
