package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Autumn-27/norma/harness"
	"github.com/jackc/pgx/v5"
)

func TestWorkerHTTPDestinationPolicyWithoutIntentAssets(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_DESTINATION_E2E") != "1" {
		t.Skip("requires explicit disposable transport database")
	}
	config, err := pgx.ParseConfig(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil || !strings.HasPrefix(config.Database, "counterweight_transport_") {
		t.Fatal("explicit disposable transport database required")
	}
	d, err := db.Open(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ic := intercept.New(d)
	priorTools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	priorJudge := ic.GetJudgeConfig()
	defer func() { _ = ic.SetEnabledTools(priorTools); _ = ic.SetJudgeConfig(priorJudge) }()
	if err := ic.SetEnabledTools([]string{"WebFetch"}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	priorResolve, priorAugment := agent.ToolResolve, agent.ToolAugment
	agent.ToolResolve, agent.ToolAugment = nil, nil
	defer func() { agent.ToolResolve, agent.ToolAugment = priorResolve, priorAugment }()
	for _, lang := range []locale.Lang{locale.En} {
		for _, streaming := range []bool{false, true} {
			for _, mode := range []string{"allowed", "denied-initial", "denied-redirect", "no-allowlist", "task-block", "global-block", "disabled-allow", "revoked-redirect", "revoked-review"} {
				t.Run(fmt.Sprintf("%s/stream=%v/%s", lang, streaming, mode), func(t *testing.T) {
					fixture, err := d.CreateTask("destination policy fixture", "test", nil, 0, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer d.DeleteTask(fixture.ID)
					var starts, finishes atomic.Int32
					var redirectGrant int64
					var initialGrant int64
					const querySecret = "SYNTHETIC_REDIRECT_QUERY_SECRET"
					finish := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						finishes.Add(1)
						w.Write([]byte("authorized fixture response"))
					}))
					defer finish.Close()
					start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						starts.Add(1)
						if mode == "revoked-redirect" {
							if deleted, err := d.Assets().DeleteTaskInterceptRule(fixture.ID, redirectGrant); err != nil || !deleted {
								t.Error(err)
							}
						}
						http.Redirect(w, r, finish.URL+"/finish?token="+querySecret, http.StatusTemporaryRedirect)
					}))
					defer start.Close()
					if mode != "no-allowlist" {
						allowedURL := start.URL + "/start"
						if mode == "denied-initial" {
							allowedURL = finish.URL + "/finish?token=" + querySecret
						}
						initial, err := d.Assets().CreateTaskInterceptRule(fixture.ID, "allow", "exact_url", allowedURL, "fixture", mode != "disabled-allow")
						if err != nil {
							t.Fatal(err)
						}
						initialGrant = initial.ID
						if mode == "allowed" || mode == "task-block" || mode == "revoked-redirect" || mode == "revoked-review" {
							rule, err := d.Assets().CreateTaskInterceptRule(fixture.ID, "allow", "exact_url", finish.URL+"/finish?token="+querySecret, "fixture", true)
							if err != nil {
								t.Fatal(err)
							}
							redirectGrant = rule.ID
						}
						if mode == "global-block" {
							rule, err := d.CreateAssetInterceptRule("exact_url", start.URL+"/start", "fixture", true)
							if err != nil {
								t.Fatal(err)
							}
							defer d.DeleteAssetInterceptRule(rule.ID)
						}
						if mode == "task-block" {
							if _, err := d.Assets().CreateTaskInterceptRule(fixture.ID, "block", "exact_url", start.URL+"/start", "fixture", true); err != nil {
								t.Fatal(err)
							}
						}
					}
					store := d.Exploration(fixture.ExplorationID)
					id, err := store.AddIntent(map[string]any{"summary": "GET a concrete target without asset_ids"}, 5, nil, "fixture")
					if err != nil {
						t.Fatal(err)
					}
					intent, err := store.GetNode(id)
					if err != nil {
						t.Fatal(err)
					}
					s := &Server{m: &Manager{assets: d.Assets()}}
					row := &db.Tool{Key: "destination_fixture", Kind: "http", Exec: json.RawMessage(`{"method":"POST","url":"{url}","body":"synthetic-body"}`), Schema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}}}`)}
					input, _ := json.Marshal(map[string]any{"url": start.URL + "/start", "asset_ids": []int{999999}, "target": "operator-approved.invalid"})
					provider := &customOperationProvider{name: row.Key, input: input}
					worker := agent.NewWorker(provider, "fixture", t.TempDir(), nil, 100000, 2, s.buildCustomTool(row))
					worker.SetNonStreaming(func() bool { return !streaming })
					var result db.Activity
					results := 0
					var hooks harness.HookRunner
					reviews := 0
					if mode == "revoked-review" {
						ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
							reviews++
							deleted, err := d.Assets().DeleteTaskInterceptRule(fixture.ID, initialGrant)
							if err != nil || !deleted {
								t.Errorf("fixture revocation failed: %v", err)
							}
							return intercept.Decision{Action: "allow", Message: "fixture operation approved"}, nil
						})
						hooks = guard.NewWithInterceptor(ic).Hooks()
					}
					_, _, err = worker.Execute(locale.WithLang(t.Context(), lang), "worker", fixture.ID, d.Assets(), store, intent, hooks, func(a db.Activity) {
						if a.Kind == "tool_result" && a.Tool == row.Key {
							result = a
							results++
						}
					}, nil, nil)
					if err != nil {
						t.Fatal(err)
					}
					deny := mode != "allowed" && mode != "no-allowlist" && mode != "disabled-allow"
					wantStarts, wantFinishes := int32(1), int32(1)
					if deny {
						wantFinishes = 0
					}
					if mode == "denied-initial" || mode == "task-block" || mode == "global-block" || mode == "revoked-review" {
						wantStarts = 0
					}
					if results != 1 || result.IsError != deny || starts.Load() != wantStarts || finishes.Load() != wantFinishes {
						t.Fatalf("actual SDK/request path results=%d starts=%d finishes=%d result=%+v", results, starts.Load(), finishes.Load(), result)
					}
					if mode == "revoked-review" && reviews != 1 {
						t.Fatalf("expected actual operation review, got %d", reviews)
					}
					if deny && !strings.Contains(result.Detail, "blocked by asset policy") {
						t.Fatal("request failed for a reason other than destination policy")
					}
					if deny && strings.Contains(result.Detail, querySecret) {
						t.Fatal("redirect query secret leaked into failure diagnostics")
					}
					if !deny && !strings.Contains(result.Detail, "authorized fixture response") {
						t.Fatal("authorized response workflow lost")
					}
				})
			}
		}
	}
}

func TestHTTPDestinationMissingTaskPolicyFailsClosed(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer target.Close()
	ctx := agent.WithRunInfo(t.Context(), agent.RunInfo{TaskID: 1})
	raw, _ := json.Marshal(httpExec{URL: target.URL})
	s := &Server{}
	res, err := s.runHTTPTool(ctx, raw, nil, nil)
	if err != nil || !res.IsError || requests.Load() != 0 {
		t.Fatalf("missing policy allowed request: %s %v", res.Flatten(), err)
	}
}

func TestHTTPResolvedDestinationBlock(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_DESTINATION_E2E") != "1" {
		t.Skip("requires explicit disposable transport database")
	}
	config, err := pgx.ParseConfig(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil || !strings.HasPrefix(config.Database, "counterweight_transport_") {
		t.Fatal("explicit disposable transport database required")
	}
	d, err := db.Open(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask("resolved destination fixture", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte("fixture")) }))
	defer target.Close()
	endpoint := strings.Replace(target.URL, "127.0.0.1", "localhost", 1) + "/?token=SYNTHETIC_PEER_SECRET"
	if _, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "exact_url", endpoint, "fixture", true); err != nil {
		t.Fatal(err)
	}
	var blocks []int64
	for _, cidr := range []string{"127.0.0.0/8", "::1/128"} {
		block, err := d.Assets().CreateTaskInterceptRule(task.ID, "block", "cidr", cidr, "fixture", true)
		if err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, block.ID)
	}
	s := &Server{m: &Manager{assets: d.Assets()}}
	raw, _ := json.Marshal(httpExec{URL: endpoint})
	ctx := agent.WithRunInfo(locale.WithLang(t.Context(), locale.En), agent.RunInfo{TaskID: task.ID})
	res, err := s.runHTTPTool(ctx, raw, nil, nil)
	if err != nil || !res.IsError || requests.Load() != 0 || !strings.Contains(res.Flatten(), "blocked by asset policy") || strings.Contains(res.Flatten(), "SYNTHETIC_PEER_SECRET") {
		t.Fatalf("resolved IP block escaped or diagnostic changed: requests=%d result=%s err=%v", requests.Load(), res.Flatten(), err)
	}
	for _, id := range blocks {
		if deleted, err := d.Assets().DeleteTaskInterceptRule(task.ID, id); err != nil || !deleted {
			t.Fatal("fixture block removal failed")
		}
	}
	res, err = s.runHTTPTool(ctx, raw, nil, nil)
	if err != nil || res.IsError || requests.Load() != 1 {
		t.Fatalf("domain/URL grant requires unintended IP allow rule: %s %v", res.Flatten(), err)
	}
}

func TestHTTPDestinationPolicyDatabaseFailureFailsClosed(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_DESTINATION_E2E") != "1" {
		t.Skip("requires explicit disposable transport database")
	}
	config, err := pgx.ParseConfig(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil || !strings.HasPrefix(config.Database, "counterweight_transport_") {
		t.Fatal("explicit disposable transport database required")
	}
	d, err := db.Open(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	assets := d.Assets()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer target.Close()
	s := &Server{m: &Manager{assets: assets}}
	raw, _ := json.Marshal(httpExec{URL: target.URL})
	res, err := s.runHTTPTool(t.Context(), raw, nil, nil)
	if err != nil || !res.IsError || requests.Load() != 0 || !strings.Contains(res.Flatten(), "policy could not be loaded") {
		t.Fatalf("unavailable database allowed request or wrong failure: %s %v", res.Flatten(), err)
	}
}
