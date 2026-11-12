package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/execution"
)

func TestFileAdmissionUsesSessionOwner(t *testing.T) {
	oldRoots, oldFailure, oldSkills := FileRoots, FileAccessFailed, SkillReadRoot
	oldResolve, oldAugment := ToolResolve, ToolAugment
	t.Cleanup(func() {
		FileRoots, FileAccessFailed, SkillReadRoot = oldRoots, oldFailure, oldSkills
		ToolResolve, ToolAugment = oldResolve, oldAugment
	})
	SkillReadRoot = ""
	ToolResolve, ToolAugment = nil, nil
	FileRoots = func(context.Context, string) (execution.Roots, bool, error) {
		return execution.Roots{}, true, errors.New("another server's admission hook")
	}
	FileAccessFailed = func(context.Context, error) { t.Error("another server's failure hook invoked") }
	dir := t.TempDir()
	ctx := WithFileAccess(t.Context(), func(context.Context, string) (execution.Roots, bool, error) {
		return execution.Roots{Read: []string{dir}, Write: []string{dir}, WorkingDir: dir}, true, nil
	}, func(_ context.Context, err error) { t.Error(err) })
	tools, _, cleanup := AugmentTools(ctx, "worker", []actool.CoreTool{actool.NewWrite()})
	defer cleanup()
	if err := fileAdmissionError(tools); err != nil {
		t.Fatal(err)
	}
	if got := fileWorkingDirectory(ctx, "unavailable"); got != dir {
		t.Fatalf("working directory: %s", got)
	}
	result, err := tools[0].Call(ctx, []byte(`{"file_path":"inside.txt","content":"owner marker"}`), &actool.ToolContext{WorkingDir: dir})
	if err != nil || result.IsError {
		t.Fatalf("write: %v %s", err, result.Flatten())
	}
	if content, err := os.ReadFile(filepath.Join(dir, "inside.txt")); err != nil || string(content) != "owner marker" {
		t.Fatalf("content: %q %v", content, err)
	}
}
