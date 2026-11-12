package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/execution"
)

// Every path the fixer's write guard refuses must also be refused once the
// file tools are confined to the same worktree. insideRoot also refuses
// relative paths, but confined tools resolve those against the working
// directory, inside the worktree, so only absolute paths are compared.
func TestConfinedWritesRefuseWhatInsideRootRefuses(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: pointer"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(outside, "x.py"),
		filepath.Join(root, "..", "escape.py"),
		filepath.Join(root, ".git"),
		filepath.Join(root, ".GIT", "config"),
		root,
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err == nil {
		paths = append(paths, filepath.Join(root, "link", "x.py"))
	}
	files, err := execution.Open(execution.Roots{WorkingDir: root, Read: []string{root}, Write: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	for _, tool := range baseToolsFor(db.CodeFixerAgentKey) {
		if tool.Name() != "Write" {
			continue
		}
		confined := files.Tool(tool.(rootedWrite).CoreTool)
		for _, p := range paths {
			if insideRoot(root, p) {
				t.Fatalf("%q is accepted by insideRoot; the table is stale", p)
			}
			in, _ := json.Marshal(map[string]string{"file_path": p, "content": "bad"})
			res, err := confined.Call(context.Background(), in, &actool.ToolContext{WorkingDir: root})
			if err != nil || !res.IsError {
				t.Errorf("confined Write accepted %q", p)
			}
		}
	}
	for _, p := range []string{filepath.Join(outside, "x.py"), filepath.Join(outside, "escape.py")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("created %s", p)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".git")); string(b) != "gitdir: pointer" {
		t.Error("git pointer was modified")
	}
}
