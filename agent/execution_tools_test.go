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

func TestConfineCatalogRootsSkillsAndDefaults(t *testing.T) {
	oldRoots, oldFailed, oldSkills := FileRoots, FileAccessFailed, SkillReadRoot
	oldResolve, oldAugment := ToolResolve, ToolAugment
	t.Cleanup(func() {
		FileRoots = oldRoots
		FileAccessFailed = oldFailed
		SkillReadRoot = oldSkills
		ToolResolve = oldResolve
		ToolAugment = oldAugment
	})
	root, skills := t.TempDir(), t.TempDir()
	SkillReadRoot = skills
	if err := os.WriteFile(filepath.Join(skills, "fixture.txt"), []byte("bundled fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	FileRoots = func(context.Context, string) (execution.Roots, bool, error) {
		return execution.Roots{Read: []string{root}, Write: []string{root}}, true, nil
	}
	ToolAugment = nil
	// Resolution wraps the confined handler, preserving default injection/metering.
	ToolResolve = func(ctx context.Context, key string, tools []actool.CoreTool) []actool.CoreTool {
		for i, tool := range tools {
			if tool.Name() == "Read" {
				schema := tool.InputSchema()
				schema["properties"].(map[string]any)["file_path"].(map[string]any)["default"] = filepath.Join(skills, "fixture.txt")
				tools[i] = DecorateTool(tool, "", schema)
			}
		}
		return tools
	}
	tools, _, cleanup := AugmentTools(t.Context(), "worker", []actool.CoreTool{actool.NewRead(), actool.NewWrite()})
	defer cleanup()
	res, err := tools[0].Call(t.Context(), json.RawMessage(`{}`), &actool.ToolContext{WorkingDir: root})
	if err != nil || res.IsError {
		t.Fatalf("skill/default read: %s %v", res.Flatten(), err)
	}
	raw, _ := json.Marshal(map[string]any{"file_path": filepath.Join(skills, "fixture.txt"), "content": "no"})
	res, err = tools[1].Call(t.Context(), raw, &actool.ToolContext{WorkingDir: root})
	if err != nil || !res.IsError {
		t.Fatal("skill root was writable")
	}
}
func TestConfineAdmissionFailureSurvivesReviewFilter(t *testing.T) {
	oldRoots, oldFailed, oldSkills := FileRoots, FileAccessFailed, SkillReadRoot
	oldResolve, oldAugment := ToolResolve, ToolAugment
	t.Cleanup(func() {
		FileRoots = oldRoots
		FileAccessFailed = oldFailed
		SkillReadRoot = oldSkills
		ToolResolve = oldResolve
		ToolAugment = oldAugment
	})
	SkillReadRoot = ""
	ToolResolve = nil
	ToolAugment = nil
	failed := false
	FileAccessFailed = func(context.Context, error) { failed = true }
	FileRoots = func(context.Context, string) (execution.Roots, bool, error) {
		return execution.Roots{Read: []string{filepath.Join(t.TempDir(), "missing")}}, true, nil
	}
	ctx := WithRunInfo(t.Context(), RunInfo{AssessmentType: db.AssessmentCodeReview})
	tools, _, cleanup := AugmentTools(ctx, db.CodeReviewPlannerAgentKey, baseToolsFor(db.CodeReviewPlannerAgentKey))
	defer cleanup()
	if !failed || fileAdmissionError(tools) == nil {
		t.Fatal("missing roots did not block the session")
	}
}
