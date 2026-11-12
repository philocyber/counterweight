package agent

import (
	"context"
	"testing"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/db"
)

func TestAssessmentTaskSessionsSDKBoundary(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	oldAugment, oldResolve := ToolAugment, ToolResolve
	t.Cleanup(func() { ToolAugment, ToolResolve = oldAugment, oldResolve })
	ToolAugment = nil
	ToolResolve = func(_ context.Context, _ string, tools []actool.CoreTool) []actool.CoreTool {
		for _, name := range forbiddenReviewTools {
			tools = append(tools, inertAssessmentTool(name))
		}
		return tools
	}
	for _, typ := range []string{db.AssessmentCodeReview, db.AssessmentGeneral} {
		opts := db.TaskCreateOptions{AssessmentType: typ}
		if typ == db.AssessmentCodeReview {
			opts.CodeTarget = &db.TaskCodeTarget{Repository: "example/fixture", URL: "https://github.com/example/fixture", Mode: "light", LocalPath: t.TempDir()}
		}
		task, err := d.CreateTaskWithOptions("boundary fixture", "Review fixture", opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
		ts := d.Exploration(task.ExplorationID)
		for _, key := range []string{"mainagent", "goals", "reporter", "auto", "custom-fixture"} {
			t.Run(typ+"/"+key, func(t *testing.T) {
				requests := 0
				provider := workerInspectProvider{Provider: captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
					yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "fixture complete"}, nil)
					yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
				}}, check: func(req llm.CompletionRequest) {
					requests++
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
						required := []string{"Read", "LS", "Glob", "Grep"}
						if key == "goals" {
							required = []string{"set_goals", "set_constraints"}
						}
						if key == "mainagent" {
							required = append(required, "add_hint", "add_intent", "steer_work", "set_goals", "set_constraints", "graph_overview", "list_findings")
						}
						for _, name := range required {
							if !names[name] {
								t.Errorf("SDK missing %s", name)
							}
						}
					} else if key != "goals" && (!names["Bash"] || !names["WebFetch"]) {
						t.Error("general lost full tools")
					}
					if key == "goals" && names["add_task_scope"] != (typ == db.AssessmentGeneral) {
						t.Error("goal scope tool mismatch")
					}
				}}
				ctx := WithRunInfo(t.Context(), RunInfo{TaskID: task.ID, ExplorationID: ts.ID(), AssessmentType: typ})
				switch key {
				case "mainagent":
					m := NewMainAgent(provider, "fixture", t.TempDir(), nil, 100000, 1)
					m.SetNoaEnabled(func() bool { return true })
					m.SetWebSearch(WebSearchOpts{Enabled: true, Backend: "ddgs"})
					_, err = m.Chat(ctx, task.ID, 0, d.Assets(), ts, task.Goal, "Inspect progress", nil, nil, nil, nil, nil)
				case "goals":
					DecomposeGoalsWithProvider(ctx, provider, t.TempDir(), task.Goal, "", d.Assets(), ts, task.ID, false, 0, nil)
				default:
					c := NewChatAgent(provider, "fixture", t.TempDir(), nil, 100000)
					c.SetNoaEnabled(func() bool { return true })
					c.SetWebSearch(WebSearchOpts{Enabled: true, Backend: "ddgs"})
					_, err = c.Chat(ctx, key, "fixture-session", "Inspect progress", 1, 0, true, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if requests == 0 {
					t.Fatal("no fixture request inspected")
				}
			})
		}
	}
}

func TestAssessmentFindingActionsUnchanged(t *testing.T) {
	oldAugment, oldResolve := ToolAugment, ToolResolve
	t.Cleanup(func() { ToolAugment, ToolResolve = oldAugment, oldResolve })
	ToolAugment, ToolResolve = nil, nil
	for _, key := range []string{db.CodeVerifierAgentKey, db.CodeFixerAgentKey, db.PatchReviewerAgentKey, db.FindingRetestAgentKey} {
		tools, def, cleanup := AugmentTools(t.Context(), key, baseToolsFor(key))
		cleanup()
		ctx := WithRunInfo(t.Context(), RunInfo{AssessmentType: db.AssessmentCodeReview})
		reviewTools, reviewDef, cleanup := AugmentTools(ctx, key, baseToolsFor(key))
		cleanup()
		if len(tools) != len(reviewTools) || len(def.Deferred) != len(reviewDef.Deferred) {
			t.Fatalf("%s catalog changed", key)
		}
		for i := range tools {
			if tools[i].Name() != reviewTools[i].Name() {
				t.Fatalf("%s tool changed", key)
			}
		}
		opts := agentcore.Options{Tools: reviewTools, EnableWebFetch: true, EnableWebSearch: true, Todos: actool.NewTodoStore()}
		restrictReviewOptions(ctx, key, &opts)
		// Only the retester needs the network; the agents that read the reviewed
		// source keep their assembled tools and lose every SDK injection.
		open := opts.EnableWebFetch && opts.EnableWebSearch && !opts.DisableBackgroundTasks && opts.Todos != nil
		closed := !opts.EnableWebFetch && !opts.EnableWebSearch && opts.DisableBackgroundTasks && opts.Todos == nil
		if len(opts.Tools) != len(reviewTools) || key == db.FindingRetestAgentKey && !open || key != db.FindingRetestAgentKey && !closed {
			t.Fatalf("%s SDK policy changed", key)
		}
		if !SkillAllowedForAssessment(db.AssessmentCodeReview, key, "custom-fixture") {
			t.Fatalf("%s skill policy changed", key)
		}
	}
}

// The provider request is what the model can call: the SDK adds its own tools
// after assembly, whatever the agent's flags say.
func TestSourceReadingChatSessionsExposeNoNetworkOrProcessTools(t *testing.T) {
	oldAugment, oldResolve := ToolAugment, ToolResolve
	t.Cleanup(func() { ToolAugment, ToolResolve = oldAugment, oldResolve })
	ToolAugment, ToolResolve = nil, nil
	for _, key := range []string{CodeReviewChatAgentKey, db.CodeVerifierAgentKey, db.CodeFixerAgentKey, db.PatchReviewerAgentKey, db.FindingRetestAgentKey} {
		t.Run(key, func(t *testing.T) {
			requests := 0
			provider := workerInspectProvider{Provider: captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
				yield(llm.StreamEvent{Type: llm.SETextDelta, Text: "fixture complete"}, nil)
				yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
			}}, check: func(req llm.CompletionRequest) {
				requests++
				names := map[string]bool{}
				for _, tool := range req.Tools {
					names[tool.Name] = true
					if len(tool.Name) > 5 && tool.Name[:5] == "mcp__" && key != db.FindingRetestAgentKey {
						t.Errorf("SDK exposed %s", tool.Name)
					}
				}
				if key == db.FindingRetestAgentKey {
					if !names["Bash"] || !names["WebFetch"] {
						t.Error("retester lost its tools")
					}
					return
				}
				for _, name := range []string{"Bash", "WebFetch", "WebSearch", "web_search", "TodoWrite", "TaskOutput", "TaskStop", "TaskList", "Monitor", "shell_open", "shell_send"} {
					if names[name] {
						t.Errorf("SDK exposed %s", name)
					}
				}
				for _, name := range []string{"Read", "LS", "Glob", "Grep"} {
					if !names[name] {
						t.Errorf("SDK missing %s", name)
					}
				}
				if writes := names["Write"] && names["Edit"] && names["MultiEdit"]; writes != (key == db.CodeFixerAgentKey) || !writes && (names["Write"] || names["Edit"] || names["MultiEdit"]) {
					t.Errorf("write tools mismatch: %v", names)
				}
			}}
			c := NewChatAgent(provider, "fixture", t.TempDir(), nil, 100000)
			c.SetWebSearch(WebSearchOpts{Enabled: true, Backend: "ddgs"})
			if _, err := c.Chat(t.Context(), key, "fixture-session", "Inspect the source", 1, 0, true, nil); err != nil {
				t.Fatal(err)
			}
			if requests == 0 {
				t.Fatal("no fixture request inspected")
			}
		})
	}
}
