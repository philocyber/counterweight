package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func TestPolicyDatabaseFailureBlocksSDKExecution(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	owner, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	control := intercept.New(owner)
	priorTools, err := control.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	priorConfig := control.GetJudgeConfig()
	t.Cleanup(func() { _ = control.SetEnabledTools(priorTools); _ = control.SetJudgeConfig(priorConfig) })
	const name = "DatabaseFailureExecutionProbe"
	if err := control.SetEnabledTools([]string{name}); err != nil {
		t.Fatal(err)
	}
	if err := control.SetJudgeConfig(intercept.JudgeConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, warm := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("cached=%v/stream=%v", warm, streaming), func(t *testing.T) {
				failed, err := db.Open(dsn)
				if err != nil {
					t.Fatal(err)
				}
				i := intercept.New(failed)
				i.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
					return intercept.Decision{Action: "allow"}, nil
				})
				if warm && !i.IsToolEnabled(name) {
					t.Fatal("fixture did not warm its policy cache")
				}
				if err := failed.Close(); err != nil {
					t.Fatal(err)
				}
				turn, executions := 0, 0
				provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
					turn++
					events := []llm.StreamEvent{{Type: llm.SETextDelta, Text: "fixture complete"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}}
					if turn == 1 {
						events = []llm.StreamEvent{{Type: llm.SEToolUseStart, ToolID: "failure-probe", ToolName: name}, {Type: llm.SEToolInputJSON, Text: `{}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}}
					}
					for _, event := range events {
						if !yield(event, nil) {
							return
						}
					}
				}}
				probe := tool.Build(tool.Spec{Name: name, Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
					executions++
					return tool.Text("inert fixture"), nil
				}})
				_, _, err = captureRun(t.Context(), agentcore.Options{Provider: provider, Tools: []tool.CoreTool{probe}, Hooks: guard.NewWithInterceptor(i).Hooks(), PermissionMode: permission.ModeBypass, NonStreaming: !streaming, WorkingDir: t.TempDir(), MaxTurns: 2}, "attempt the isolated fixture", nil)
				if err != nil {
					t.Fatal(err)
				}
				if executions != 0 {
					t.Fatalf("database failure permitted %d executions", executions)
				}
			})
		}
	}
}
