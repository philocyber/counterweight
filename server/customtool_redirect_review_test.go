package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

func TestCustomHTTPRedirectsReviewEffectiveMethodBodyAndDestination(t *testing.T) {
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
	if err := ic.SetEnabledTools([]string{"WebFetch"}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	priorResolve, priorAugment := agent.ToolResolve, agent.ToolAugment
	agent.ToolResolve = nil
	t.Cleanup(func() { agent.ToolResolve, agent.ToolAugment = priorResolve, priorAugment })
	for _, denyFinal := range []bool{false, true} {
		t.Run(fmt.Sprintf("deny-final=%v", denyFinal), func(t *testing.T) {
			var starts, relays, finishes atomic.Int32
			finish := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				finishes.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != "GET" || len(body) != 0 {
					t.Errorf("redirected GET retained body: %s %q", r.Method, body)
				}
				_, _ = w.Write([]byte("final destination"))
			}))
			defer finish.Close()
			relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				relays.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || string(body) != "reviewed-body" {
					t.Errorf("307 replay changed: %s %q", r.Method, body)
				}
				http.Redirect(w, r, finish.URL+"/final", http.StatusSeeOther)
			}))
			defer relay.Close()
			start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				starts.Add(1)
				http.Redirect(w, r, relay.URL+"/relay", http.StatusTemporaryRedirect)
			}))
			defer start.Close()
			row := &db.Tool{Key: "redirect_fixture", Kind: "http", Exec: json.RawMessage(fmt.Sprintf(`{"method":"POST","url":%q,"body":"reviewed-body"}`, start.URL+"/start")), Schema: json.RawMessage(`{"type":"object","properties":{"unused":{"type":"string"}}}`)}
			s := &Server{}
			tool := s.buildCustomTool(row)
			agent.ToolAugment = func(context.Context, string) ([]actool.CoreTool, agent.DeferredInfo, func()) {
				return []actool.CoreTool{tool}, agent.DeferredInfo{}, func() {}
			}
			reviews := 0
			ic.SetReviewer(func(_ context.Context, _ int64, _ string, in intercept.ReviewInput) (intercept.Decision, error) {
				reviews++
				var op struct {
					Method, URL, Body string
					Hop               int `json:"redirect_hop"`
				}
				if err := json.Unmarshal(in.Arguments, &op); err != nil {
					t.Error(err)
				}
				wantURL := []string{start.URL + "/start", relay.URL + "/relay", finish.URL + "/final"}
				if reviews > 3 {
					t.Errorf("duplicate request review %d", reviews)
					return intercept.Decision{Action: "deny"}, nil
				}
				wantMethod, wantBody := "POST", "reviewed-body"
				if reviews == 3 {
					wantMethod, wantBody = "GET", ""
				}
				if op.URL != wantURL[reviews-1] || op.Method != wantMethod || op.Body != wantBody || op.Hop != reviews-1 {
					t.Errorf("redirect reviewed a stale operation: %s", in.Arguments)
				}
				action := "allow"
				if denyFinal && reviews == 3 {
					action = "deny"
				}
				return intercept.Decision{Action: action, Message: "redirect fixture verdict"}, nil
			})
			taskID := fmt.Sprintf("redirect-review-%v", denyFinal)
			t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID) })
			ctx := intercept.WithTaskContext(t.Context(), taskID, "fixture", nil)
			provider := &customOperationProvider{name: row.Key, input: json.RawMessage(`{}`)}
			chat := agent.NewChatAgent(provider, "fixture", t.TempDir(), nil, 100000)
			chat.SetGuard(guard.NewWithInterceptor(ic))
			var result db.Activity
			if _, err := chat.Chat(ctx, "fixture", "redirect-session", "fixture", 2, 0, false, func(a db.Activity) {
				if a.Kind == "tool_result" {
					result = a
				}
			}); err != nil {
				t.Fatal(err)
			}
			wantFinal := int32(1)
			if denyFinal {
				wantFinal = 0
			}
			if reviews != 3 || starts.Load() != 1 || relays.Load() != 1 || finishes.Load() != wantFinal || result.IsError != denyFinal {
				t.Fatalf("review/request path: reviews=%d requests=%d/%d/%d result=%+v", reviews, starts.Load(), relays.Load(), finishes.Load(), result)
			}
			rows, err := d.ListTaskIntercepts(taskID)
			if err != nil || len(rows) != 3 {
				t.Fatalf("redirect audits: %d %v", len(rows), err)
			}
			for _, row := range rows {
				detail, err := d.GetInterceptDetail(row.ID)
				if err != nil || detail.Audit == nil || detail.Audit.ToolUseID != "operation-call" || detail.Audit.Correlation != "exact" {
					t.Fatalf("redirect audit identity: %+v %v", detail, err)
				}
				wantStatus := "succeeded"
				if denyFinal {
					wantStatus = "failed"
					if row.Status == "denied" {
						wantStatus = "not_executed"
					}
				}
				if detail.Audit.ExecutionStatus != wantStatus {
					t.Fatalf("redirect audit never completed: %+v", detail.Audit)
				}
			}
		})
	}
}
