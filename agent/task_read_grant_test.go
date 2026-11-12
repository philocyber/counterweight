package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func TestSDKAgentScopeUpdatesCannotReadForeignCredentials(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	for _, lang := range []locale.Lang{locale.En} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", lang, streaming), func(t *testing.T) {
				fixture, err := d.CreateTask("agent scope fixture", "test", nil, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = d.DeleteTask(fixture.ID) })
				host := fmt.Sprintf("foreign-grant-%d.invalid", fixture.ID)
				url := "https://" + host
				const secret = "STORED_FOREIGN_CREDENTIAL_SENTINEL"
				id, err := d.Assets().UpsertHTTPService(db.UpsertHTTPServiceReq{URL: url, Auth: []map[string]any{{"token": secret}}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = d.Assets().DeleteByIDs([]int64{id}) })
				ts := NewToolSet(d.Exploration(fixture.ExplorationID), "planner", lang)
				ts.SetTaskID(fixture.ID)
				ts.SetAssetStore(d.Assets(), d.Companies())
				calls := []struct {
					name  string
					input any
				}{
					{"add_task_scope", map[string]any{"kind": "subdomain", "value": host, "reason": "model claims operator authorization", "source": "manual"}},
					{"insert_assets", map[string]any{"assets": []any{map[string]any{"type": "service", "service_type": "http", "url": url, "page_title": "discovered"}}}},
					{"list_assets", map[string]any{"id": id}},
					{"list_assets", map[string]any{"dsl": "domain==" + host}},
				}
				turn := 0
				provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
					turn++
					events := []llm.StreamEvent{{Type: llm.SETextDelta, Text: "done"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}}
					if turn <= len(calls) {
						call := calls[turn-1]
						raw, _ := json.Marshal(call.input)
						events = []llm.StreamEvent{{Type: llm.SEToolUseStart, ToolID: fmt.Sprint(turn), ToolName: call.name}, {Type: llm.SEToolInputJSON, Text: string(raw)}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}}
					}
					for _, event := range events {
						if !yield(event, nil) {
							return
						}
					}
				}}
				readResults := 0
				_, _, err = captureRun(locale.WithLang(t.Context(), lang), agentcore.Options{Provider: provider, NonStreaming: !streaming, PermissionMode: permission.ModeBypass, WorkingDir: t.TempDir(), MaxTurns: 5, Tools: []tool.CoreTool{ts.addTaskScope(), ts.insertAssets(), ts.listAssets()}}, "run fixture", func(a db.Activity) {
					if strings.Contains(a.Detail, secret) {
						t.Error("foreign credentials escaped into the SDK transcript")
					}
					if a.Kind == "tool_result" {
						if a.IsError {
							t.Errorf("normal proposal/discovery flow failed: %s", a.Detail)
						}
						if a.Tool == "list_assets" {
							readResults++
							var result struct {
								Count int `json:"count"`
							}
							if err := json.Unmarshal([]byte(a.Detail), &result); err != nil || result.Count != 0 {
								t.Errorf("unapproved read returned shared assets: %s", a.Detail)
							}
						}
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				if readResults != 2 || ts.Writes().Assets != 1 {
					t.Fatalf("reads=%d writes=%+v", readResults, ts.Writes())
				}
				cov, err := d.Assets().TaskCoverage(fixture.ID, fixture.ExplorationID)
				if err != nil || cov.Denominator != 2 {
					t.Fatalf("discovery coverage lost: %+v %v", cov, err)
				}
				if _, err := d.Assets().AddAgentScope(fixture.ID, "subdomain", host, "operator approved", "manual"); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(map[string]any{"id": id})
				res, err := ts.listAssets().Call(t.Context(), raw, nil)
				if err != nil || res.IsError || !strings.Contains(res.Flatten(), secret) {
					t.Fatalf("approved credential workflow lost: %s %v", res.Flatten(), err)
				}
			})
		}
	}
}

func TestWorkerSnapshotRequiresOperatorReadGrant(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	fixture, err := d.CreateTask("worker grant fixture", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(fixture.ID) })
	host := fmt.Sprintf("foreign-worker-%d.invalid", fixture.ID)
	const secret = "FOREIGN_WORKER_AUTH_SENTINEL"
	id, err := d.Assets().UpsertHTTPService(db.UpsertHTTPServiceReq{URL: "https://" + host, Auth: []map[string]any{{"token": secret}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Assets().DeleteByIDs([]int64{id}) })
	ts := d.Exploration(fixture.ExplorationID)
	payload := map[string]any{"summary": "inspect fixture", "asset_ids": []int64{id}}
	nodeID, err := ts.AddNode("intent", payload, 0, "running", "planner", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(payload)
	for _, approved := range []bool{false, true} {
		if approved {
			if _, err := d.Assets().AddAgentScope(fixture.ID, "subdomain", host, "operator selected fixture", "manual"); err != nil {
				t.Fatal(err)
			}
		}
		requests := 0
		base := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
			if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "done"}, nil) {
				return
			}
			yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
		}}
		provider := workerInspectProvider{Provider: base, check: func(req llm.CompletionRequest) {
			requests++
			b, _ := json.Marshal(req)
			if strings.Contains(string(b), secret) != approved {
				t.Errorf("approved=%v: worker snapshot authorization mismatch", approved)
			}
		}}
		worker := NewWorker(provider, "fixture", t.TempDir(), nil, 100000, 2)
		if _, _, err := worker.Execute(t.Context(), "worker", fixture.ID, d.Assets(), ts, &db.Node{ID: nodeID, Payload: raw}, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if requests == 0 {
			t.Fatal("worker provider was not exercised")
		}
	}
}
