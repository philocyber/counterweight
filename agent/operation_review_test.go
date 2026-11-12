package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

func TestToolDefaultsAreReviewedBeforeExecution(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	ic := intercept.New(d)
	tools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	config := ic.GetJudgeConfig()
	t.Cleanup(func() { _ = ic.SetEnabledTools(tools); _ = ic.SetJudgeConfig(config) })
	if err := ic.SetEnabledTools([]string{"default_probe"}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"deny", "allow"} {
		t.Run(action, func(t *testing.T) {
			schema := map[string]any{"type": "object", "properties": map[string]any{"target": map[string]any{"type": "string", "default": "resolved-default"}}}
			executions, reviews := 0, 0
			probe := actool.Build(actool.Spec{Name: "default_probe", Schema: schema, Run: func(_ context.Context, input json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
				executions++
				if string(input) != `{"target":"resolved-default"}` {
					t.Errorf("execution input changed after review: %s", input)
				}
				return actool.Text("done"), nil
			}})
			probe = guardPanic(DecorateTool(probe, "fixture", schema))
			ic.SetReviewer(func(_ context.Context, _ int64, _ string, in intercept.ReviewInput) (intercept.Decision, error) {
				reviews++
				if string(in.Arguments) != `{"target":"resolved-default"}` {
					t.Errorf("raw defaults reviewed: %s", in.Arguments)
				}
				schema["properties"].(map[string]any)["target"].(map[string]any)["default"] = "late replacement"
				return intercept.Decision{Action: action}, nil
			})
			turn := 0
			provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
				turn++
				if turn == 1 {
					yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "default-call", ToolName: "default_probe"}, nil)
					yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{}`}, nil)
					yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"}, nil)
				} else {
					yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "done"}, nil)
					yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
				}
			}}
			taskID := "default-review-" + action
			ctx := intercept.WithTaskContext(t.Context(), taskID, "fixture", nil)
			t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID) })
			if _, _, err := captureRun(ctx, agentcore.Options{Provider: provider, Tools: []actool.CoreTool{probe}, Hooks: guard.NewWithInterceptor(ic).Hooks(), PermissionMode: permission.ModeBypass, WorkingDir: t.TempDir(), MaxTurns: 2}, "fixture", nil); err != nil {
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
			if err != nil || detail.Audit == nil || detail.Audit.ToolUseID != "default-call" || detail.Audit.Correlation != "exact" {
				t.Fatalf("default review lost exact attribution: %+v %v", detail, err)
			}
		})
	}
}

type operationMarkerFixture struct{ actool.CoreTool }

func (operationMarkerFixture) RequiresOperationReview() bool { return true }

type denyOperationFixture struct{ calls int }

func (h *denyOperationFixture) PreToolUse(context.Context, string, []byte) (bool, string, []byte) {
	h.calls++
	return true, "fixture denied", nil
}
func (*denyOperationFixture) PostToolUse(context.Context, string, []byte, []byte, bool) {}
func (*denyOperationFixture) Stop(context.Context, []llm.Message) (bool, []string, string) {
	return false, nil, ""
}

func TestOperationReviewUsesRegisteredHandler(t *testing.T) {
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("fixture"))
	}))
	defer endpoint.Close()
	for _, replacement := range []string{"host", "sdk"} {
		t.Run(replacement, func(t *testing.T) {
			name := "same_name"
			input := `{}`
			if replacement == "sdk" {
				name = "WebFetch"
				b, _ := json.Marshal(map[string]string{"url": endpoint.URL, "prompt": "fixture"})
				input = string(b)
			}
			executions := 0
			plain := actool.Build(actool.Spec{Name: name, Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
				executions++
				return actool.Text("fixture"), nil
			}})
			tools := []actool.CoreTool{operationMarkerFixture{plain}}
			if replacement == "host" {
				tools = append(tools, plain)
			}
			hooks := &denyOperationFixture{}
			turn := 0
			provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
				turn++
				if turn == 1 {
					yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "collision", ToolName: name}, nil)
					yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: input}, nil)
					yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"}, nil)
				} else {
					yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
				}
			}}
			_, _, err := captureRun(t.Context(), agentcore.Options{Provider: provider, Tools: tools, Hooks: hooks, EnableWebFetch: replacement == "sdk", DisableBackgroundTasks: true, PermissionMode: permission.ModeBypass, WorkingDir: t.TempDir(), MaxTurns: 2}, "fixture", nil)
			if err != nil || hooks.calls != 1 || executions != 0 || requests.Load() != 0 {
				t.Fatalf("registry replacement bypassed guard: hooks=%d executions=%d requests=%d err=%v", hooks.calls, executions, requests.Load(), err)
			}
		})
	}
	// Construction checks the same backend configuration as NewSession and
	// performs no search requests, including the paid DeepSeek backend.
	for _, backend := range []string{"ddgs", "invalid-fixture"} {
		plain := actool.Build(actool.Spec{Name: "web_search", Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
			return actool.Text("fixture"), nil
		}})
		opts := agentcore.Options{Tools: []actool.CoreTool{operationMarkerFixture{plain}}, Hooks: &denyOperationFixture{}, EnableWebSearch: true, WebSearchBackend: backend, DisableBackgroundTasks: true}
		ConfigureOperationReview(&opts)
		h := opts.Hooks.(*operationHooks)
		if h.custom["web_search"] != (backend == "invalid-fixture") {
			t.Fatalf("incorrect web_search handler exemption for %s", backend)
		}
	}
}

type operationTestHooks struct {
	harness.HookRunner
	rewrite bool
}

func (h operationTestHooks) PreToolUse(context.Context, string, []byte) (bool, string, []byte) {
	if h.rewrite {
		return false, "", []byte(`{"command":"changed"}`)
	}
	return false, "", nil
}

func TestResolvedOperationRejectsHookRewriteAndCancellation(t *testing.T) {
	ctx := context.WithValue(t.Context(), reviewBindingKey{}, operationReviewBinding{name: "fixture", hooks: operationTestHooks{rewrite: true}})
	if err := ReviewOperation(ctx, "Bash", map[string]any{"command": "original"}, nil); err == nil {
		t.Fatal("changed hook plan was approved")
	}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), reviewBindingKey{}, operationReviewBinding{name: "fixture", hooks: operationTestHooks{}}))
	cancel()
	if err := ReviewOperation(ctx, "Bash", map[string]any{"command": "original"}, nil); err == nil {
		t.Fatal("cancelled operation was approved")
	}
	if err := ReviewOperation(RequireOperationReview(t.Context(), "fixture"), "Bash", map[string]any{}, nil); err == nil {
		t.Fatal("custom operation without session review binding was approved")
	}
}
