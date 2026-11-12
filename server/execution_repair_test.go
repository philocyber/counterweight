package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/execution"
	"github.com/Philocyber/counterweight/intercept"
)

func TestExecutionStandaloneFindingWorkspace(t *testing.T) {
	m, e := executionFixture(t)
	fid, err := m.pg.AddFinding(0, 0, "fixture", "standalone", "low", "fixture", "fixture", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.DeleteFinding(fid) })
	_, conv, _, err := m.pg.CreateFindingRetest(t.Context(), fid, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM conversations WHERE id=$1`, conv.ID) })
	ctx := intercept.WithConvID(t.Context(), conv.ID)
	roots, on, err := e.fileRoots(ctx, db.FindingRetestAgentKey)
	if err != nil || !on {
		t.Fatalf("admission: %v %v", on, err)
	}
	dir := filepath.Join(m.dir, "sessions", "conv-"+strconv.FormatInt(conv.ID, 10))
	if len(roots.Write) != 1 || roots.Write[0] != dir {
		t.Fatalf("roots: %+v", roots)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("workspace: %v", err)
	}
	outside := filepath.Join(m.dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside-secret-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := execution.Open(roots)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	call := func(tool actool.CoreTool, path string) actool.Result {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"file_path": path, "content": "inside marker"})
		result, err := files.Tool(tool).Call(ctx, raw, &actool.ToolContext{WorkingDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := call(actool.NewWrite(), "inside.txt"); result.IsError {
		t.Fatal(result.Flatten())
	}
	if result := call(actool.NewRead(), "inside.txt"); result.IsError || !strings.Contains(result.Flatten(), "inside marker") {
		t.Fatal(result.Flatten())
	}
	for _, path := range []string{outside, filepath.Join("..", "..", "outside.txt")} {
		if result := call(actool.NewRead(), path); !result.IsError || strings.Contains(result.Flatten(), "outside-secret-marker") {
			t.Fatalf("escape: %s", result.Flatten())
		}
	}
	if err := m.pg.SetSetting(settingIsolation, "false"); err != nil {
		t.Fatal(err)
	}
	if _, on, err := e.fileRoots(ctx, db.FindingRetestAgentKey); on || err != nil {
		t.Fatalf("host mode: %v %v", on, err)
	}
}

func TestExecutionMissingTaskDirectoryStarts(t *testing.T) {
	m, e := executionFixture(t)
	task := executionTask(t, m, db.AssessmentGeneral)
	dir := filepath.Join(m.dir, "tasks", task.ID)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory already present: %v", err)
	}
	called := false
	provider := retestProvider{complete: func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		called = true
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("fixture complete")}}, "end_turn", llm.Usage{}, nil
	}}
	s := &Server{m: m, engine: e, ctx: t.Context(), taskAgents: map[string]*taskAgentBundle{}, llmOn: true, llmProv: provider}
	bundle := s.agentsForTask(task)
	bundle.pl.SetNonStreaming(func() bool { return true })
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	if _, _, err := bundle.pl.Plan(t.Context(), id, m.Assets(), task.Store, task.Goal, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("provider not called")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}
}

func TestExecutionFileRootFailsClosedWithReason(t *testing.T) {
	m, e := executionFixture(t)
	task := executionTask(t, m, db.AssessmentGeneral)
	dir := filepath.Join(m.dir, "tasks", task.ID)
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := agent.WithRunInfo(t.Context(), agent.RunInfo{TaskID: executionRepairTaskID(task)})
	_, on, err := e.fileRoots(ctx, "worker")
	if !on || err == nil || !strings.Contains(err.Error(), "working directory could not be created or opened") {
		t.Fatalf("admission: %v %v", on, err)
	}
	state, err := m.pg.TaskApprovalState(executionRepairTaskID(task))
	if err != nil || state.Active() == nil || !strings.Contains(state.Active().Reason, "working directory could not be created or opened") {
		t.Fatalf("persisted reason: %+v %v", state, err)
	}
	if !e.IsPaused(task.ID) {
		t.Fatal("failed root did not pause task")
	}
}

func executionRepairTaskID(task *Task) int64 { id, _ := strconv.ParseInt(task.ID, 10, 64); return id }

func TestExecutionRootPathsStayInsideData(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{m: &Manager{dir: dir}}
	outside := t.TempDir()
	if err := e.validateFileRoots(execution.Roots{Read: []string{outside}}); err == nil || !strings.Contains(err.Error(), "escapes the data directory") {
		t.Fatalf("outside root: %v", err)
	}
	file := filepath.Join(dir, "file-root")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.validateFileRoots(execution.Roots{Read: []string{file}}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file root: %v", err)
	}
	if err := e.privateWorkingDirectory(filepath.Join(dir, "..", "escaped-workspace")); err == nil || !strings.Contains(err.Error(), "escapes the data directory") {
		t.Fatalf("creation escape: %v", err)
	}
	link := filepath.Join(dir, "linked-root")
	if err := os.Symlink(outside, link); err != nil {
		t.Logf("symbolic-link check unavailable: %v", err)
		return
	}
	if err := e.validateFileRoots(execution.Roots{Read: []string{link}}); err == nil || !strings.Contains(err.Error(), "escapes the data directory") {
		t.Fatalf("linked root: %v", err)
	}
}
