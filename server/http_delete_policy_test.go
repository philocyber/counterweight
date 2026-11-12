package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
	actool "github.com/Autumn-27/norma/tool"
)

func TestDefaultHTTPDeleteRulesBlockSDKRequestsBeforeTransport(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	priorRules, err := d.ListInterceptRules()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, r := range priorRules {
			_, _ = d.Exec(`UPDATE intercept_rules SET enabled=$1 WHERE id=$2`, r.Enabled, r.ID)
		}
	})
	selected := 0
	for _, r := range priorRules {
		enabled := r.Name == "[Built-in] HTTP DELETE via curl / wget" || r.Name == "[Built-in] HTTP DELETE method in scripts (JS/general)"
		if enabled {
			selected++
		}
		if _, err := d.Exec(`UPDATE intercept_rules SET enabled=$1 WHERE id=$2`, enabled, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	if selected != 2 {
		t.Fatalf("expected actual seeded DELETE policies, found %d", selected)
	}
	ic := intercept.New(d)
	tools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	config := ic.GetJudgeConfig()
	t.Cleanup(func() { _ = ic.SetEnabledTools(tools); _ = ic.SetJudgeConfig(config) })
	if err := ic.SetEnabledTools([]string{"Bash", "WebFetch"}); err != nil {
		t.Fatal(err)
	}
	priorResolve, priorAugment := agent.ToolResolve, agent.ToolAugment
	agent.ToolResolve = nil
	t.Cleanup(func() { agent.ToolResolve, agent.ToolAugment = priorResolve, priorAugment })
	for _, judge := range []bool{false, true} {
		for _, lang := range []locale.Lang{locale.En} {
			for _, tc := range []struct {
				name, kind, command, method string
				denied                      bool
			}{
				{"quoted-method", "command", `curl -q -X 'DELETE' %s`, "", true},
				{"assigned-method", "command", `curl -q --request=DELETE %s`, "", true},
				{"quoted-fragments", "command", `c'u'rl -q -'X'DE'LE'TE %s`, "", true},
				{"native-delete", "http", "", "DELETE", true},
				{"normalized-native-delete", "http", "", " delete ", true},
				{"native-get", "http", "", "GET", false},
			} {
				t.Run(fmt.Sprintf("%s/judge=%v/%s", tc.name, judge, lang), func(t *testing.T) {
					if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: judge}); err != nil {
						t.Fatal(err)
					}
					reviews := 0
					ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
						reviews++
						return intercept.Decision{Action: "allow"}, nil
					})
					var requests atomic.Int32
					listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						if r.Method != "GET" {
							t.Errorf("blocked method reached transport: %s", r.Method)
						}
						_, _ = w.Write([]byte("allowed data"))
					}))
					defer listener.Close()
					spec := map[string]string{"method": tc.method, "url": listener.URL + "/fixture"}
					if tc.kind == "command" {
						spec = map[string]string{"command": fmt.Sprintf(tc.command, listener.URL+"/fixture")}
					}
					execRaw, _ := json.Marshal(spec)
					row := &db.Tool{Key: "delete_boundary_fixture", Kind: tc.kind, Exec: execRaw}
					tool := (&Server{}).buildCustomTool(row)
					agent.ToolAugment = func(context.Context, string) ([]actool.CoreTool, agent.DeferredInfo, func()) {
						return []actool.CoreTool{tool}, agent.DeferredInfo{}, func() {}
					}
					taskID := fmt.Sprintf("delete-boundary-%d", time.Now().UnixNano())
					t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID) })
					base, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					ctx := intercept.WithTaskContext(locale.WithLang(base, lang), taskID, "fixture", func(a db.Activity) {
						if a.Kind == "intercept_request" {
							t.Error("recognized DELETE requested approval instead of matching its deny rule")
						}
					})
					chat := agent.NewChatAgent(&customOperationProvider{name: row.Key, input: json.RawMessage(`{}`)}, "fixture", t.TempDir(), nil, 100000)
					chat.SetGuard(guard.NewWithInterceptor(ic))
					if _, err := chat.Chat(ctx, "fixture", "delete-boundary-session", "fixture", 2, 0, false, nil); err != nil {
						t.Fatal(err)
					}
					wantRequests, wantReviews := int32(0), 0
					if !tc.denied {
						wantRequests = 1
						if judge {
							wantReviews = 1
						}
					}
					if requests.Load() != wantRequests || reviews != wantReviews {
						t.Fatalf("boundary bypass: requests=%d want=%d modelReviews=%d want=%d", requests.Load(), wantRequests, reviews, wantReviews)
					}
					rows, err := d.ListTaskIntercepts(taskID)
					if err != nil {
						t.Fatal(err)
					}
					if tc.denied {
						if len(rows) != 1 || rows[0].Status != "denied" || rows[0].RuleID == nil || rows[0].ToolName != row.Key {
							t.Fatalf("seeded-policy denial audit missing: rows=%d", len(rows))
						}
						detail, err := d.GetInterceptDetail(rows[0].ID)
						if err != nil || detail == nil || detail.Audit == nil || detail.Audit.InitialAction != "deny" || detail.Audit.ExecutionStatus != "not_executed" || detail.Audit.ModelFallback {
							t.Fatalf("deny provenance missing: %v", err)
						}
					}
				})
			}
		}
	}
}
