package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/db"
)

var forbiddenReviewTools = []string{"Bash", "TodoWrite", "Write", "Edit", "MultiEdit", "Sleep", "TaskOutput", "TaskStop", "TaskList", "Monitor", "shell_open", "shell_send", "shell_read", "shell_close", "shell_list", "WebFetch", "WebSearch", "web_search", "traffic_search", "traffic_get", "traffic_blob", "insert_assets", "upsert_asset", "add_company_scope", "add_task_scope", "list_assets", "list_untested_assets", "mcp__fixture__run", "custom_command", "custom_http", "SearchExtraTools", "ExecuteExtraTool", "Compress", "future_unknown_tool"}

func inertAssessmentTool(name string) actool.CoreTool {
	return actool.Build(actool.Spec{Name: name, Schema: obj(nil), Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
		return actool.Text("fixture"), nil
	}})
}

func TestAssessmentToolBoundaryAfterResolution(t *testing.T) {
	oldAugment, oldResolve := ToolAugment, ToolResolve
	oldTraffic := FindingTrafficBindingEnabled
	oldReviewExtra := ReviewExtraTools
	t.Cleanup(func() {
		ToolAugment, ToolResolve = oldAugment, oldResolve
		FindingTrafficBindingEnabled = oldTraffic
		ReviewExtraTools = oldReviewExtra
	})
	ReviewExtraTools = func(context.Context, int64) []actool.CoreTool {
		return []actool.CoreTool{inertAssessmentTool("custom_command"), inertAssessmentTool("future_unknown_tool"), inertAssessmentTool("resolve_scan_lead")}
	}
	FindingTrafficBindingEnabled = func() bool { return true }
	ToolAugment = func(context.Context, string) ([]actool.CoreTool, DeferredInfo, func()) {
		return []actool.CoreTool{inertAssessmentTool("Skill")}, DeferredInfo{Deferred: []string{"custom_command"}, GlobalNames: []string{"custom_command"}}, nil
	}
	ToolResolve = func(_ context.Context, _ string, tools []actool.CoreTool) []actool.CoreTool {
		// Simulate stored bindings reintroducing every forbidden family at the final resolver.
		for _, name := range forbiddenReviewTools {
			tools = append(tools, inertAssessmentTool(name))
		}
		return tools
	}
	for _, typ := range []string{db.AssessmentGeneral, db.AssessmentWebApp, db.AssessmentNetwork, db.AssessmentCodeReview} {
		for _, role := range []string{"planner", "worker", "mainagent", "goals", "reporter", "auto", "custom-fixture"} {
			key := TaskAgentKey(typ, role)
			want := role
			if typ == db.AssessmentCodeReview && (role == "planner" || role == "worker") {
				want = "codereview_" + role
			}
			if key != want {
				t.Fatalf("%s %s resolved %s", typ, role, key)
			}
			ctx := WithRunInfo(t.Context(), RunInfo{AssessmentType: typ})
			ts := NewToolSet(nil, "")
			domain := ts.MainAgentTools()
			if role == "planner" {
				domain = ts.PlannerTools()
			}
			if role == "worker" {
				domain = ts.WorkerTools()
			}
			tools, def, cleanup := AugmentTools(ctx, key, append(domain, baseToolsFor(key)...))
			cleanup()
			names := map[string]bool{}
			for _, tool := range tools {
				names[tool.Name()] = true
			}
			for _, name := range forbiddenReviewTools {
				if names[name] == (typ == db.AssessmentCodeReview) {
					t.Errorf("%s/%s forbidden family %s present=%v", typ, role, name, names[name])
				}
			}
			for _, name := range []string{"Read", "LS", "Glob", "Grep", "Skill", "report_finding", "list_findings", "node_detail", "get_worker_trace", "search_all_worker_traces"} {
				if !names[name] {
					t.Errorf("%s/%s missing %s", typ, role, name)
				}
			}
			if role != "worker" && role != "planner" {
				for _, name := range []string{"graph_overview", "list_findings", "list_facts", "node_detail", "get_worker_output", "get_worker_trace", "add_hint", "add_intent", "steer_work", "set_goals", "set_constraints"} {
					if !names[name] {
						t.Errorf("%s/%s missing observe/steer %s", typ, role, name)
					}
				}
			}
			if typ == db.AssessmentCodeReview && (len(def.Deferred) > 0 || len(def.GlobalNames) > 0 || def.Unlock != nil) {
				t.Fatal("review retained deferred tools")
			}
		}
	}
}

func TestAssessmentExecutionUsesSelectedAgentsAndSDKTools(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	oldPrompt, oldAugment, oldResolve := PromptOverride, ToolAugment, ToolResolve
	t.Cleanup(func() { PromptOverride, ToolAugment, ToolResolve = oldPrompt, oldAugment, oldResolve })
	ToolResolve = nil
	for _, typ := range []string{db.AssessmentGeneral, db.AssessmentWebApp, db.AssessmentNetwork, db.AssessmentCodeReview} {
		task, err := d.CreateTaskWithOptions("execution fixture", "Review fixture", db.TaskCreateOptions{AssessmentType: typ, CodeTarget: func() *db.TaskCodeTarget {
			if typ != db.AssessmentCodeReview {
				return nil
			}
			return &db.TaskCodeTarget{Repository: "example/fixture", URL: "https://github.com/example/fixture", LocalPath: t.TempDir(), Mode: "balanced"}
		}()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
		ts := d.Exploration(task.ExplorationID)
		for _, role := range []string{"planner", "worker"} {
			t.Run(typ+"/"+role, func(t *testing.T) {
				wantKey := TaskAgentKey(typ, role)
				seenPrompt, seenTools, requests := false, false, 0
				PromptOverride = func(key string) (string, bool) {
					if key == wantKey {
						seenPrompt = true
					}
					return "editable-" + key, true
				}
				ToolAugment = func(_ context.Context, key string) ([]actool.CoreTool, DeferredInfo, func()) {
					if key != wantKey {
						t.Errorf("tool key=%s want %s", key, wantKey)
					}
					seenTools = true
					return nil, DeferredInfo{}, nil
				}
				provider := workerInspectProvider{Provider: captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
					if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "fixture complete"}, nil) {
						return
					}
					yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
				}}, check: func(req llm.CompletionRequest) {
					requests++
					if !strings.Contains(strings.Join(req.System, "\n"), "editable-"+wantKey) {
						t.Error("selected editable prompt missing")
					}
					names := map[string]bool{}
					for _, tool := range req.Tools {
						names[tool.Name] = true
					}
					if typ == db.AssessmentCodeReview {
						for _, name := range forbiddenReviewTools {
							if names[name] {
								t.Errorf("SDK exposed %s", name)
							}
						}
						for _, name := range []string{"Read", "LS", "Glob", "Grep"} {
							if !names[name] {
								t.Errorf("SDK missing %s", name)
							}
						}
					} else if !names["Bash"] || !names["WebFetch"] {
						t.Error("running-target tools missing")
					}
				}}
				if role == "planner" {
					p := NewPlanner(provider, "fixture", t.TempDir(), nil, 100000, 2)
					p.SetNoaEnabled(func() bool { return true })
					p.SetWebSearch(WebSearchOpts{Enabled: true, Backend: "ddgs"})
					if _, _, err := p.Plan(t.Context(), task.ID, d.Assets(), ts, task.Goal, nil, nil); err != nil {
						t.Fatal(err)
					}
				} else {
					id, err := ts.AddIntent(map[string]any{"summary": "Read src/ for input validation"}, 5, nil, "planner")
					if err != nil {
						t.Fatal(err)
					}
					intent, err := ts.GetNode(id)
					if err != nil {
						t.Fatal(err)
					}
					w := NewWorker(provider, "fixture", t.TempDir(), nil, 100000, 2)
					w.SetNoaEnabled(func() bool { return true })
					w.SetWebSearch(WebSearchOpts{Enabled: true, Backend: "ddgs"})
					if _, _, err := w.Execute(t.Context(), "worker", task.ID, d.Assets(), ts, intent, nil, nil, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				if !seenPrompt || !seenTools || requests == 0 {
					t.Fatalf("execution unobserved: prompt=%v tools=%v requests=%d", seenPrompt, seenTools, requests)
				}
			})
		}
	}
}

func TestAssessmentSkillVisibility(t *testing.T) {
	for _, role := range []string{"planner", "worker", "mainagent", "goals", "reporter", "auto", "custom-fixture"} {
		for _, typ := range []string{db.AssessmentGeneral, db.AssessmentWebApp, db.AssessmentNetwork, db.AssessmentCodeReview} {
			review := typ == db.AssessmentCodeReview
			for _, name := range []string{"whitebox-code-review-methodology", "dom-vulnerability-static-analysis", "custom-sanitizer-audit"} {
				if got := SkillAllowedForAssessment(typ, TaskAgentKey(typ, role), name); got != review {
					t.Errorf("%s/%s %s=%v", typ, role, name, got)
				}
			}
			for _, name := range []string{"api-recon", "http-query-method", "playwright-cli", "unclassified-custom-skill"} {
				if got := SkillAllowedForAssessment(typ, TaskAgentKey(typ, role), name); got == review {
					t.Errorf("%s/%s %s=%v", typ, role, name, got)
				}
			}
			if !SkillAllowedForAssessment(typ, TaskAgentKey(typ, role), "vuln-assessment-methodology") {
				t.Error("shared methodology unavailable")
			}
		}
	}
	for _, key := range []string{db.CodeVerifierAgentKey, db.CodeFixerAgentKey, db.PatchReviewerAgentKey} {
		if CodeReviewExecution(WithRunInfo(t.Context(), RunInfo{AssessmentType: db.AssessmentCodeReview}), key) {
			t.Errorf("mitigation restricted: %s", key)
		}
	}
}

func TestCodeReviewIntentAdmissionWithoutNetworkAssets(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTaskWithOptions("source intent fixture", "Review fixture", db.TaskCreateOptions{AssessmentType: db.AssessmentCodeReview, CodeTarget: &db.TaskCodeTarget{Repository: "example/fixture", URL: "https://github.com/example/fixture", Mode: "light", LocalPath: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	ts := NewToolSet(d.Exploration(task.ExplorationID), "planner")
	ts.SetTaskID(task.ID)
	ts.sourceReview = true
	ts.SetAssetStore(d.Assets(), d.Companies())
	ts.SetCoverageEnabled(false)
	if _, exists := ts.graphOverviewData()["coverage"]; exists {
		t.Fatal("review overview exposed network coverage")
	}
	id, err := ts.addOneIntent(intentItem{Summary: "Read src/auth.go and trace authorization"})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := d.Exploration(task.ExplorationID).GetNode(id)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithRunInfo(t.Context(), RunInfo{AssessmentType: db.AssessmentCodeReview})
	if _, err := validateWorkerIntentTargets(ctx, task.ID, nil, intent); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.addOneIntent(intentItem{Summary: "network asset", AssetIDs: []json.RawMessage{json.RawMessage("1")}}); err == nil {
		t.Fatal("review accepted network intent")
	}
	if d.Assets().CoverageEnabled(task.ID) {
		t.Fatal("asset coverage enabled")
	}
	if _, err := d.Exec(`UPDATE tasks SET coverage_enabled=true WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if d.Assets().CoverageEnabled(task.ID) {
		t.Fatal("stored flag reenabled review coverage")
	}
	scope, err := d.Assets().ListTaskScope(task.ID)
	if err != nil || len(scope) != 0 {
		t.Fatalf("network scope=%v err=%v", scope, err)
	}
}
