package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
)

// oneToolCall answers the first model turn with a single tool call and ends
// the run on the next one.
func oneToolCall(id, name, input string) captureUsageProvider {
	turn := 0
	return captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
		turn++
		if turn == 1 {
			yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: id, ToolName: name}, nil)
			yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: input}, nil)
			yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"}, nil)
		} else {
			yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "done"}, nil)
			yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
		}
	}}
}

// A deferred tool invoked through the SDK wrapper is reviewed under its own
// name and parameters, exactly like a direct call: the enabled-tool setting,
// the reviewer, the outcome and the record all refer to the inner tool.
func TestDeferredToolIsReviewedUnderItsOwnName(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	ic := intercept.New(d)
	tools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	config := ic.GetJudgeConfig()
	t.Cleanup(func() { _ = ic.SetEnabledTools(tools); _ = ic.SetJudgeConfig(config) })
	if err := ic.SetEnabledTools([]string{"mcp__fixture__probe"}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{"wrapped", "direct"} {
		for _, action := range []string{"deny", "allow"} {
			t.Run(call+"/"+action, func(t *testing.T) {
				executions, reviews := 0, 0
				probe := actool.Build(actool.Spec{Name: "mcp__fixture__probe", Schema: map[string]any{"type": "object"}, Run: func(_ context.Context, input json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
					executions++
					if string(input) != `{"target":"fixture"}` {
						t.Errorf("execution input differs from the reviewed one: %s", input)
					}
					return actool.Text("done"), nil
				}})
				ic.SetReviewer(func(_ context.Context, _ int64, _ string, in intercept.ReviewInput) (intercept.Decision, error) {
					reviews++
					if in.Tool != "mcp__fixture__probe" || string(in.Arguments) != `{"target":"fixture"}` {
						t.Errorf("reviewed %s %s instead of the inner tool call", in.Tool, in.Arguments)
					}
					return intercept.Decision{Action: action}, nil
				})
				name, input := "mcp__fixture__probe", `{"target":"fixture"}`
				if call == "wrapped" {
					name, input = actool.ExecuteExtraToolName, `{"tool_name":"mcp__fixture__probe","params":{"target":"fixture"}}`
				}
				taskID := "deferred-review-" + call + "-" + action
				ctx := intercept.WithTaskContext(t.Context(), taskID, "fixture", nil)
				t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID) })
				opts := agentcore.Options{Provider: oneToolCall("deferred-call", name, input), Tools: []actool.CoreTool{probe}, DeferredTools: []string{"mcp__fixture__probe"}, Hooks: guard.NewWithInterceptor(ic).Hooks(), DisableBackgroundTasks: true, PermissionMode: permission.ModeBypass, WorkingDir: t.TempDir(), MaxTurns: 2}
				if _, _, err := captureRun(ctx, opts, "fixture", nil); err != nil {
					t.Fatal(err)
				}
				want := 0
				if action == "allow" {
					want = 1
				}
				if executions != want || reviews != 1 {
					t.Fatalf("reviewed %d executed %d", reviews, executions)
				}
				rows, err := d.ListTaskIntercepts(taskID)
				if err != nil || len(rows) != 1 {
					t.Fatalf("audit rows %d %v", len(rows), err)
				}
				detail, err := d.GetInterceptDetail(rows[0].ID)
				if err != nil || detail.ToolName != "mcp__fixture__probe" || detail.Audit == nil || detail.Audit.ToolUseID != "deferred-call" || detail.Audit.Correlation != "exact" {
					t.Fatalf("record is not under the inner tool with exact attribution: %+v %v", detail, err)
				}
			})
		}
	}
}

// A deferred tool that is still locked does not run when the call names it
// directly, and runs once it is unlocked.
func TestLockedDeferredToolCannotBeCalledByName(t *testing.T) {
	executions := 0
	locked := actool.Build(actool.Spec{Name: "mcp__fixture__locked", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
		executions++
		return actool.Text("done"), nil
	}})
	unlock := actool.NewUnlockSet()
	run := func(name, input string) {
		t.Helper()
		opts := agentcore.Options{Provider: oneToolCall("locked-call", name, input), Tools: []actool.CoreTool{locked}, DeferredTools: []string{"mcp__fixture__locked"}, UnlockSet: unlock, Hooks: guard.New().Hooks(), DisableBackgroundTasks: true, PermissionMode: permission.ModeBypass, WorkingDir: t.TempDir(), MaxTurns: 2}
		if _, _, err := captureRun(t.Context(), opts, "fixture", nil); err != nil {
			t.Fatal(err)
		}
	}
	run("mcp__fixture__locked", `{}`)
	run(actool.ExecuteExtraToolName, `{"tool_name":"mcp__fixture__locked","params":{}}`)
	if executions != 0 {
		t.Fatalf("locked deferred tool ran %d time(s)", executions)
	}
	unlock.Add("mcp__fixture__locked")
	run("mcp__fixture__locked", `{}`)
	run(actool.ExecuteExtraToolName, `{"tool_name":"mcp__fixture__locked","params":{}}`)
	if executions != 2 {
		t.Fatalf("unlocked deferred tool ran %d time(s), want 2", executions)
	}
}

// After an approval pause on a wrapped deferred tool, the approval recorded
// for the inner tool is recognised as covering the paused call.
func TestContinuationRecognisesApprovedDeferredTool(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{
			Type: llm.BlockToolUse, ID: "call-1", Name: actool.ExecuteExtraToolName, Input: json.RawMessage(`{"tool_name":"mcp__fixture__probe","params":{"target":"fixture"}}`),
		}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{
			Type: llm.BlockToolResult, ToolUseID: "call-1", IsError: true,
			Content: []llm.ContentBlock{{Type: llm.BlockText, Text: intercept.ApprovalDeferredMessage(locale.En)}},
		}}},
	}
	approved, ok := continuationAfterApprovalHold(locale.En, messages, intercept.ApprovalResume{
		Replays: []intercept.ExactReplay{{Tool: "mcp__fixture__probe", Input: []byte(`{"target":"fixture"}`)}},
	})
	if !ok || !strings.Contains(approved, "Repeat that exact call") {
		t.Fatalf("approved deferred call not recognised: ok=%v text=%q", ok, approved)
	}
	other, ok := continuationAfterApprovalHold(locale.En, messages, intercept.ApprovalResume{
		Replays: []intercept.ExactReplay{{Tool: "mcp__fixture__other", Input: []byte(`{"target":"fixture"}`)}},
	})
	if !ok || !strings.Contains(other, "was not approved") {
		t.Fatalf("approval for another tool covered the paused call: ok=%v text=%q", ok, other)
	}
}

// Monitor would start a host command with the server's own environment. It is
// refused in a session with the guard attached and in one without any hooks.
func TestMonitorIsRefusedWithAndWithoutGuard(t *testing.T) {
	t.Setenv("AGENT_CORE_DISABLE_BACKGROUND_TASKS", "")
	for _, session := range []string{"guarded", "no hooks"} {
		t.Run(session, func(t *testing.T) {
			var results []db.Activity
			opts := agentcore.Options{Provider: oneToolCall("monitor-call", "Monitor", `{"command":"echo fixture-monitor"}`), Tools: []actool.CoreTool{}, PermissionMode: permission.ModeBypass, WorkingDir: t.TempDir(), MaxTurns: 2}
			if session == "guarded" {
				opts.Hooks = guard.New().Hooks()
			}
			if _, _, err := captureRun(t.Context(), opts, "fixture", func(r db.Activity) {
				if r.Kind == "tool_result" {
					results = append(results, r)
				}
			}); err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || !results[0].IsError || strings.Contains(results[0].Detail, "Started monitor") {
				t.Fatalf("Monitor was started: %+v", results)
			}
			if session == "guarded" && !strings.Contains(results[0].Detail, "Bash") {
				t.Fatalf("refusal does not point to Bash: %q", results[0].Detail)
			}
		})
	}
}

// The process never lets the SDK install a search binary on first use.
func TestRipgrepInstallationIsDisabled(t *testing.T) {
	if os.Getenv("NORMA_RIPGREP_NO_INSTALL") == "" {
		t.Fatal("the SDK may install ripgrep through npm on the first Grep call")
	}
}
