package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/db"
)

func baseToolNames(agentKey string) []string {
	var names []string
	for _, t := range baseToolsFor(agentKey) {
		names = append(names, t.Name())
	}
	slices.Sort(names)
	return names
}

func TestCodeReviewAgentsBaseTools(t *testing.T) {
	for _, key := range []string{db.CodeVerifierAgentKey, db.PatchReviewerAgentKey} {
		if got, want := baseToolNames(key), []string{"Glob", "Grep", "LS", "Read"}; !slices.Equal(got, want) {
			t.Fatalf("%s base tools=%v want %v", key, got, want)
		}
	}
	if got, want := baseToolNames(db.CodeFixerAgentKey), []string{"Edit", "Glob", "Grep", "LS", "MultiEdit", "Read", "Write"}; !slices.Equal(got, want) {
		t.Fatalf("codefixer base tools=%v want %v", got, want)
	}
	// Other chat agents keep the full SDK set, shell included.
	if names := baseToolNames("auto"); !slices.Contains(names, "Bash") || !slices.Contains(names, "Write") {
		t.Fatalf("auto agent lost default tools: %v", names)
	}
}

func TestFixerWritesStayInsideWorktree(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		filepath.Join(root, "app", "db.py"):          true,
		filepath.Join(root, "new", "dir", "test.py"): true,
		filepath.Join(outside, "x.py"):               false,
		filepath.Join(root, "..", "escape.py"):       false,
		filepath.Join(root, ".git"):                  false,
		filepath.Join(root, ".GIT", "config"):        false,
		root:                                         false,
		"app/db.py":                                  false,
		"":                                           false,
	} {
		if got := insideRoot(root, path); got != want {
			t.Errorf("insideRoot(%q)=%v want %v", path, got, want)
		}
	}
	if insideRoot("", filepath.Join(root, "a.py")) {
		t.Error("empty root allowed a write")
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err == nil {
		if insideRoot(root, filepath.Join(root, "link", "x.py")) {
			t.Error("write through a symlink escaped the worktree")
		}
	}

	old := WriteRoot
	t.Cleanup(func() { WriteRoot = old })
	WriteRoot = func(context.Context) string { return root }
	var write actool.CoreTool
	for _, tool := range baseToolsFor(db.CodeFixerAgentKey) {
		if tool.Name() == "Write" {
			write = tool
		}
	}
	call := func(path string) actool.Result {
		in, _ := json.Marshal(map[string]string{"file_path": path, "content": "fixed\n"})
		res, err := write.Call(t.Context(), in, &actool.ToolContext{})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := call(filepath.Join(outside, "pwn.py")); !res.IsError {
		t.Fatal("write outside the worktree succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwn.py")); err == nil {
		t.Fatal("file created outside the worktree")
	}
	if res := call(filepath.Join(root, "app", "db.py")); res.IsError {
		t.Fatalf("write inside the worktree refused: %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "app", "db.py")); string(b) != "fixed\n" {
		t.Fatalf("content=%q", b)
	}
}
