package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
)

func TestWorkerPersistedTargetsRecheckedBeforeProvider(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	for _, lang := range []locale.Lang{locale.En} {
		for _, resumed := range []bool{false, true} {
			for _, mode := range []string{"allowed", "allowed-legacy", "blocked", "legacy-blocked", "legacy-single-blocked", "mixed", "missing", "malformed", "nil-store", "db-error", "unanchored"} {
				t.Run(fmt.Sprintf("%s/resume=%v/%s", lang, resumed, mode), func(t *testing.T) {
					task, err := d.CreateTask("worker target revalidation", "fixture", nil, 0, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer d.DeleteTask(task.ID)
					// Persisted service identities normalize a bare-root trailing slash.
					endpoint := fmt.Sprintf("https://worker-target-%d.example.test", task.ID)
					id, err := d.Assets().UpsertHTTPService(db.UpsertHTTPServiceReq{URL: endpoint})
					if err != nil {
						t.Fatal(err)
					}
					defer d.Exec(`DELETE FROM assets WHERE id=$1`, id)
					if _, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "exact_url", endpoint, "fixture", true); err != nil {
						t.Fatal(err)
					}
					payload := map[string]any{"summary": "run the inert target policy fixture", "asset_ids": []int64{id}}
					if mode == "legacy-blocked" || mode == "allowed-legacy" {
						delete(payload, "asset_ids")
						payload["target_ids"] = []string{fmt.Sprint(id)}
					}
					if mode == "legacy-single-blocked" {
						payload["asset_ids"] = nil
						payload["target_id"] = fmt.Sprint(id)
					}
					if mode == "mixed" {
						outside, err := d.Assets().UpsertHTTPService(db.UpsertHTTPServiceReq{URL: endpoint + ".outside"})
						if err != nil {
							t.Fatal(err)
						}
						defer d.Exec(`DELETE FROM assets WHERE id=$1`, outside)
						payload["target_id"] = outside
					}
					if mode == "missing" {
						payload["asset_ids"] = []int64{9223372036854775807}
					}
					if mode == "malformed" {
						payload["asset_ids"] = []string{"invalid"}
					}
					if mode == "unanchored" {
						delete(payload, "asset_ids")
					}
					ts := d.Exploration(task.ExplorationID)
					intentID, err := ts.AddNode("intent", payload, 0, "running", "planner", nil)
					if err != nil {
						t.Fatal(err)
					}
					// Change policy after persistence to model a queued or resumed intent.
					if mode == "blocked" || mode == "legacy-blocked" || mode == "legacy-single-blocked" {
						if _, err := d.Assets().CreateTaskInterceptRule(task.ID, "block", "exact_url", endpoint, "revoked fixture", true); err != nil {
							t.Fatal(err)
						}
					}
					turns, executions := 0, 0
					provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
						turns++
						if turns == 1 {
							for _, event := range []llm.StreamEvent{{Type: llm.SEToolUseStart, ToolID: "probe", ToolName: "WorkerTargetProbe"}, {Type: llm.SEToolInputJSON, Text: `{}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}} {
								if !yield(event, nil) {
									return
								}
							}
						} else {
							yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
						}
					}}
					probe := tool.Build(tool.Spec{Name: "WorkerTargetProbe", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *tool.ToolContext) (tool.Result, error) {
						executions++
						return tool.Text("fixture"), nil
					}})
					worker := NewWorker(provider, "fixture", t.TempDir(), nil, 100000, 3, probe)
					ctx := locale.WithLang(t.Context(), lang)
					raw, _ := json.Marshal(payload)
					intent := &db.Node{ID: intentID, Payload: raw}
					store := d.Assets()
					if mode == "db-error" {
						dsn, _, err := db.DSN()
						if err != nil {
							t.Fatal(err)
						}
						closed, err := db.Open(dsn)
						if err != nil {
							t.Fatal(err)
						}
						store = closed.Assets()
						if err := closed.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "nil-store" {
						store = nil
					}
					if resumed {
						_, _, err = worker.ExecuteWithMessage(ctx, "worker", task.ID, store, ts, intent, nil, nil, nil, nil, "fixture", "continue")
					} else {
						_, _, err = worker.Execute(ctx, "worker", task.ID, store, ts, intent, nil, nil, nil, nil)
					}
					allowed := mode == "allowed" || mode == "allowed-legacy" || mode == "unanchored"
					if allowed {
						if err != nil || turns != 2 || executions != 1 {
							t.Fatalf("allowed workflow: turns=%d executions=%d error=%v", turns, executions, err)
						}
					} else if err == nil || turns != 0 || executions != 0 {
						t.Fatalf("invalid target reached provider/executor: turns=%d executions=%d error=%v", turns, executions, err)
					}
				})
			}
		}
	}
}
