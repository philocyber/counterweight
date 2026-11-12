package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
)

// Exercise actual planner and task-chat sessions with a synthetic provider and
// an inert probe. No model service, target, shell command, or filesystem tool runs.
func TestTaskAgentsEnforceConfiguredApproval(t *testing.T) {
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
	priorTools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	priorConfig := ic.GetJudgeConfig()
	t.Cleanup(func() { _ = ic.SetEnabledTools(priorTools); _ = ic.SetJudgeConfig(priorConfig) })
	const probeName = "TaskGuardExecutionProbe"
	if err := ic.SetEnabledTools([]string{probeName}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true, AskTimeoutSeconds: 2, AskTimeoutAction: "deny"}); err != nil {
		t.Fatal(err)
	}
	for _, agentKey := range []string{"planner", "mainagent"} {
		for _, streaming := range []bool{true, false} {
			for _, policy := range []struct {
				name, action, status string
				approve, execute     bool
			}{
				{"allow", "allow", "allowed", false, true},
				{"deny", "deny", "denied", false, false},
				{"human allow", "ask", "allowed", true, true},
				{"human deny", "ask", "denied", false, false},
			} {
				t.Run(fmt.Sprintf("%s/stream=%v/%s", agentKey, streaming, policy.name), func(t *testing.T) {
					expID, err := d.CreateExploration("isolated guard fixture", "approval regression")
					if err != nil {
						t.Fatal(err)
					}
					taskID := strconv.FormatInt(expID, 10)
					t.Cleanup(func() {
						_, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID)
						_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
					})
					ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
						return intercept.Decision{Action: policy.action, Message: "fixture policy"}, nil
					})
					turn, executions, requests, snapshots := 0, 0, 0, 0
					provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
						turn++
						events := []llm.StreamEvent{{Type: llm.SETextDelta, Text: "fixture finished"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}}
						if turn == 1 {
							events = []llm.StreamEvent{{Type: llm.SEToolUseStart, ToolID: "fixture-probe", ToolName: probeName}, {Type: llm.SEToolInputJSON, Text: `{}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}}
						}
						for _, event := range events {
							if !yield(event, nil) {
								return
							}
						}
					}}
					bound := sidequestion.Bind(provider, sidequestion.Model{Model: "fixture", Streaming: streaming})
					probe := tool.Build(tool.Spec{Name: probeName, Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
						executions++
						return tool.Text("inert fixture result"), nil
					}})
					priorAugment := ToolAugment
					ToolAugment = func(context.Context, string) ([]tool.CoreTool, DeferredInfo, func()) {
						return []tool.CoreTool{probe}, DeferredInfo{}, nil
					}
					t.Cleanup(func() { ToolAugment = priorAugment })
					emit := func(a db.Activity) {
						if a.Kind != "intercept_request" {
							return
						}
						requests++
						if a.Worker != agentKey {
							t.Errorf("approval belongs to worker %q, want %q", a.Worker, agentKey)
						}
						var detail struct {
							ID int64 `json:"pending_id"`
						}
						if err := json.Unmarshal([]byte(a.Detail), &detail); err != nil {
							t.Error(err)
							return
						}
						if err := ic.Decide(detail.ID, policy.approve); err != nil {
							t.Error(err)
						}
					}
					ctx := sidequestion.WithPublisher(t.Context(), func(sidequestion.Snapshot) { snapshots++ })
					g := guard.NewWithInterceptor(ic)
					if agentKey == "planner" {
						p := NewPlanner(bound, "fixture", t.TempDir(), nil, 0, 3)
						p.SetGuard(g)
						p.SetNonStreaming(func() bool { return !streaming })
						_, _, err = p.Plan(ctx, expID, nil, d.Exploration(expID), "fixture goal", nil, emit)
					} else {
						m := NewMainAgent(bound, "fixture", t.TempDir(), nil, 0, 3)
						m.SetGuard(g)
						m.SetNonStreaming(func() bool { return !streaming })
						_, err = m.Chat(ctx, expID, 0, nil, d.Exploration(expID), "fixture goal", "run the fixture probe", emit, nil, nil, nil, nil)
						if snapshots == 0 {
							t.Error("side-question capture was lost when attaching guard hooks")
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					wantExecutions := 0
					if policy.execute {
						wantExecutions = 1
					}
					if executions != wantExecutions {
						t.Fatalf("probe executions=%d, want %d", executions, wantExecutions)
					}
					wantRequests := 0
					if policy.action == "ask" {
						wantRequests = 1
					}
					if requests != wantRequests {
						t.Fatalf("inline approval requests=%d, want %d", requests, wantRequests)
					}
					rows, err := d.ListTaskIntercepts(taskID)
					if err != nil || len(rows) != 1 {
						t.Fatalf("approval rows=%d err=%v", len(rows), err)
					}
					if rows[0].AgentName != agentKey || rows[0].Status != policy.status {
						t.Fatalf("incorrect approval attribution: %+v", rows[0])
					}
				})
			}
		}
	}
}
