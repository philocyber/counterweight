package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

func TestCustomConfiguredCommandCannotBypassExecutorRule(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ic := intercept.New(d)
	tools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	config := ic.GetJudgeConfig()
	t.Cleanup(func() { _ = ic.SetEnabledTools(tools); _ = ic.SetJudgeConfig(config) })
	if err := ic.SetEnabledTools([]string{"Bash"}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	rule, err := d.CreateInterceptRule("fixture resolved command deny", "tool_input", "string", "RESTRICTED_OPERATION_MARKER", "deny", "configured operation denied", 100000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteInterceptRule(rule.ID) })
	ic.Invalidate()
	workDir := t.TempDir()
	target := filepath.Join(workDir, "must-not-exist.txt")
	execRaw, _ := json.Marshal(map[string]any{"command": "echo RESTRICTED_OPERATION_MARKER > \"" + target + "\""})
	row := &db.Tool{Key: "restricted_fixture", Kind: "command", Exec: execRaw}
	tool := (&Server{}).buildCustomTool(row)
	priorResolve, priorAugment := agent.ToolResolve, agent.ToolAugment
	agent.ToolResolve = nil
	t.Cleanup(func() { agent.ToolResolve, agent.ToolAugment = priorResolve, priorAugment })
	agent.ToolAugment = func(context.Context, string) ([]actool.CoreTool, agent.DeferredInfo, func()) {
		return []actool.CoreTool{tool}, agent.DeferredInfo{}, func() {}
	}
	taskID := "custom-rule-fixture"
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID) })
	ctx := intercept.WithTaskContext(t.Context(), taskID, "fixture", nil)
	chat := agent.NewChatAgent(&customOperationProvider{name: row.Key, input: json.RawMessage(`{}`)}, "fixture", workDir, nil, 100000)
	chat.SetGuard(guard.NewWithInterceptor(ic))
	if _, err := chat.Chat(ctx, "fixture", "rule-session", "fixture", 2, 0, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("abstract params bypassed concrete-operation rule: marker stat %v", err)
	}
	rows, err := d.ListTaskIntercepts(taskID)
	if err != nil || len(rows) != 1 || rows[0].Status != "denied" || rows[0].ToolName != row.Key {
		t.Fatalf("missing concrete denial audit: %+v %v", rows, err)
	}
}
