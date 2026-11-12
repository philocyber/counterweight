package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
	actool "github.com/Autumn-27/norma/tool"
)

func TestUnconfinedCustomCommandRequiresHumanBeforeNativeDispatch(t *testing.T) {
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
	rule, err := d.CreateInterceptRule("unconfined fixture restriction", "tool_input", "string", "UNMATCHED_RESTRICTION_FIXTURE", "deny", "", 1000000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteInterceptRule(rule.ID) })
	ic.Invalidate()
	priorResolve, priorAugment := agent.ToolResolve, agent.ToolAugment
	agent.ToolResolve = nil
	t.Cleanup(func() { agent.ToolResolve, agent.ToolAugment = priorResolve, priorAugment })
	for _, judge := range []bool{false, true} {
		for _, lang := range []locale.Lang{locale.En} {
			for _, response := range []string{"allow", "deny", "no-decision"} {
				t.Run(fmt.Sprintf("judge=%v/%s/%s", judge, lang, response), func(t *testing.T) {
					if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: judge, AskTimeoutSeconds: 1, AskTimeoutAction: "allow"}); err != nil {
						t.Fatal(err)
					}
					reviews := 0
					ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
						reviews++
						return intercept.Decision{Action: "allow"}, nil
					})
					workDir := t.TempDir()
					target := filepath.Join(workDir, "authorized-marker.txt")
					argument := shellQuote(target)
					if runtime.GOOS == "windows" {
						argument = quotePowerShellLiteral(target)
					}
					execRaw, _ := json.Marshal(map[string]string{"command": "echo AUTHORIZED_FIXTURE > " + argument})
					row := &db.Tool{Key: "unconfined_marker_fixture", Kind: "command", Exec: execRaw}
					tool := (&Server{}).buildCustomTool(row)
					agent.ToolAugment = func(context.Context, string) ([]actool.CoreTool, agent.DeferredInfo, func()) {
						return []actool.CoreTool{tool}, agent.DeferredInfo{}, func() {}
					}
					taskID := fmt.Sprintf("unconfined-native-%d", time.Now().UnixNano())
					t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID) })
					deadline := 5 * time.Second
					if response == "no-decision" {
						deadline = 300 * time.Millisecond
					}
					base, cancel := context.WithTimeout(t.Context(), deadline)
					defer cancel()
					pendingID := int64(0)
					ctx := intercept.WithTaskContext(locale.WithLang(base, lang), taskID, "fixture", func(activity db.Activity) {
						if activity.Kind != "intercept_request" {
							return
						}
						if _, err := os.Stat(target); !os.IsNotExist(err) {
							t.Errorf("native side effect occurred before approval: %v", err)
						}
						var detail struct {
							ID int64 `json:"pending_id"`
						}
						if err := json.Unmarshal([]byte(activity.Detail), &detail); err != nil {
							t.Error(err)
							return
						}
						pendingID = detail.ID
						if response != "no-decision" {
							if err := ic.Decide(pendingID, response == "allow"); err != nil {
								t.Error(err)
							}
						}
					})
					chat := agent.NewChatAgent(&customOperationProvider{name: row.Key, input: json.RawMessage(`{}`)}, "fixture", workDir, nil, 100000)
					chat.SetGuard(guard.NewWithInterceptor(ic))
					_, err := chat.Chat(ctx, "fixture", "unconfined-session", "fixture", 2, 0, false, nil)
					if err != nil && !(response == "no-decision" && errors.Is(err, context.DeadlineExceeded)) {
						t.Fatal(err)
					}
					_, statErr := os.Stat(target)
					if response == "allow" {
						if statErr != nil {
							t.Fatalf("authorized native workflow failed: %v", statErr)
						}
					} else if !os.IsNotExist(statErr) {
						t.Fatalf("native dispatch without authorization: %v", statErr)
					}
					if pendingID == 0 || reviews != 0 {
						t.Fatalf("human approval bypassed: pending=%d modelReviews=%d", pendingID, reviews)
					}
					rows, err := d.ListTaskIntercepts(taskID)
					want := "denied"
					if response == "allow" {
						want = "allowed"
					}
					// SDK cancellation returns before its hook goroutine necessarily
					// finishes recording the denial. Await that exact pending row;
					// keep the terminal status assertion rather than accepting pending.
					if response == "no-decision" {
						until := time.Now().Add(time.Second)
						for err == nil && len(rows) == 1 && rows[0].Status == "pending" && time.Now().Before(until) {
							time.Sleep(10 * time.Millisecond)
							rows, err = d.ListTaskIntercepts(taskID)
						}
					}
					if err != nil || len(rows) != 1 || rows[0].Status != want {
						t.Fatalf("authorization row mismatch: count=%d wantStatus=%s error=%v", len(rows), want, err)
					}
					// Synthetic decisions have no foreign rule ID; their name and
					// provenance are recorded in the immutable review audit.
					detail, err := d.GetInterceptDetail(pendingID)
					if err != nil || detail == nil || detail.Audit == nil {
						t.Fatalf("authorization audit missing: %v", err)
					}
					wantAction, wantExecution := "deny", "not_executed"
					if response == "allow" {
						wantAction, wantExecution = "allow", "succeeded"
					}
					if detail.Audit.RuleName != "Unconfined shell execution" || detail.Audit.InitialAction != "ask" || detail.Audit.ModelFallback || detail.Audit.ConfigDigest == "" || detail.Audit.EffectiveAction != wantAction || detail.Audit.ExecutionStatus != wantExecution {
						t.Fatalf("authorization audit mismatch: rule=%s action=%s execution=%s", detail.Audit.RuleName, detail.Audit.EffectiveAction, detail.Audit.ExecutionStatus)
					}
					if response != "allow" {
						if _, err := os.Stat(target); !os.IsNotExist(err) {
							t.Fatalf("native effect after terminal denial: %v", err)
						}
					}
				})
			}
		}
	}
}
