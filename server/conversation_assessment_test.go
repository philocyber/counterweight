package server

import (
	"context"
	"encoding/json"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
)

func TestAssessmentConversationBindingsAndAssembly(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Fatal(err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	oldAugment, oldResolve := agent.ToolAugment, agent.ToolResolve
	oldTraffic := agent.FindingTrafficBindingEnabled
	t.Cleanup(func() {
		agent.ToolAugment, agent.ToolResolve = oldAugment, oldResolve
		agent.FindingTrafficBindingEnabled = oldTraffic
	})
	s := &Server{m: &Manager{pg: pg}}
	wireTools(pg, buildDomainReg(pg.Assets()))
	rows, err := pg.ListTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		saved := *row
		t.Cleanup(func() {
			bindings, _ := json.Marshal(saved.Agents)
			_ = pg.UpdateTool(saved.Key, saved.Description, saved.Schema, bindings, saved.Enabled)
		})
	}
	hostCalls := 0
	wireAgentAugment(pg, t.TempDir(), func(context.Context) ([]actool.CoreTool, map[string][]string) {
		hostCalls++
		return []actool.CoreTool{actool.Build(actool.Spec{Name: "custom_exec", Schema: map[string]any{"type": "object"}})}, nil
	}, s.reviewConversationTools)
	var generalID int64
	unbound, err := pg.CreateConversation("auto", "Fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(`DELETE FROM conversations WHERE id=$1`, unbound.ID) })
	if ctx, err := s.conversationAssessmentContext(t.Context(), unbound.ID); err != nil || agent.RunInfoFrom(ctx).AssessmentType != "" {
		t.Fatal("unbound platform assistant changed")
	}
	for _, typ := range []string{db.AssessmentGeneral, db.AssessmentCodeReview} {
		opts := db.TaskCreateOptions{AssessmentType: typ}
		if typ == db.AssessmentCodeReview {
			opts.CodeTarget = &db.TaskCodeTarget{Repository: "example/fixture", URL: "https://github.com/example/fixture", Mode: "light", LocalPath: t.TempDir()}
		}
		task, err := pg.CreateTaskWithOptions("conversation fixture", "Review fixture", opts)
		if err != nil {
			t.Fatal(err)
		}
		initialID := task.ID
		t.Cleanup(func() { _ = pg.DeleteTask(initialID) })
		if typ == db.AssessmentGeneral {
			generalID = task.ID
		}
		for _, key := range []string{"mainagent", "goals", "reporter", "auto", "custom_boundary_fixture"} {
			a, err := pg.GetAgentByKey(key)
			if err != nil {
				t.Fatal(err)
			}
			if a == nil {
				a, err = pg.CreateAgent(key, "Fixture", "Fixture")
				if err != nil {
					t.Fatal(err)
				}
				created := a
				t.Cleanup(func() { _, _ = pg.Exec(`DELETE FROM agents WHERE id=$1`, created.ID) })
			}
			t.Cleanup(func() { _, _ = pg.Exec(`UPDATE agents SET interactive_shell=$1 WHERE id=$2`, a.InteractiveShell, a.ID) })
			_, err = pg.Exec(`UPDATE agents SET interactive_shell=true WHERE id=$1`, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := pg.AddAgentToToolBinding(key, []string{"insert_assets", "add_task_scope", "add_company_scope", "get_task_graph", "list_task_findings", "get_task_node_detail", "get_task_worker_trace", "list_task_worker_traces", "search_task_worker_traces", "add_task_hint", "pause_task", "update_finding_report"}); err != nil {
				t.Fatal(err)
			}
			conv, err := pg.CreateConversation(key, "Fixture", nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = pg.Exec(`DELETE FROM conversations WHERE id=$1`, conv.ID) })
			ids := []int64{task.ID}
			if typ == db.AssessmentCodeReview {
				ids = []int64{generalID, 0, task.ID}
			}
			if err := pg.BindConversationTasks(conv.ID, ids); err != nil {
				t.Fatal(err)
			}
			for turn := 0; turn < 2; turn++ {
				ctx, err := s.conversationAssessmentContext(t.Context(), conv.ID)
				if err != nil {
					t.Fatal(err)
				}
				before := hostCalls
				tools, _, cleanup := agent.AugmentTools(ctx, key, actool.DefaultTools())
				cleanup()
				names := map[string]bool{}
				for _, tool := range tools {
					names[tool.Name()] = true
				}
				if typ == db.AssessmentCodeReview {
					if before != hostCalls {
						t.Error("review constructed custom tools")
					}
					for _, name := range []string{"Bash", "Write", "Edit", "MultiEdit", "shell_open", "shell_read", "shell_send", "shell_list", "shell_close", "insert_assets", "add_task_scope", "add_company_scope", "custom_exec"} {
						if names[name] {
							t.Errorf("%s retained %s", key, name)
						}
					}
					for _, name := range []string{"Read", "LS", "Glob", "Grep", "get_task_graph", "list_task_findings", "get_task_node_detail", "get_task_worker_trace", "list_task_worker_traces", "search_task_worker_traces", "add_task_hint", "pause_task", "update_finding_report"} {
						if !names[name] {
							t.Errorf("%s missing %s", key, name)
						}
					}
				} else {
					if !names["Bash"] || !names["Write"] || !names["custom_exec"] || !names["insert_assets"] {
						t.Errorf("%s general catalog changed", key)
					}
					if agent.RunInfoFrom(ctx).TaskID != 0 {
						t.Error("general assistant identity changed")
					}
				}
			}
			if typ == db.AssessmentCodeReview {
				_, _ = pg.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID)
				if _, err := s.conversationAssessmentContext(t.Context(), conv.ID); err == nil {
					t.Error("missing task did not fail closed")
				}
				// Recreate a source task for the remaining agent kinds.
				task, err = pg.CreateTaskWithOptions("conversation fixture", "Review fixture", opts)
				if err != nil {
					t.Fatal(err)
				}
				savedID := task.ID
				t.Cleanup(func() { _ = pg.DeleteTask(savedID) })
			}
		}
	}
}

func TestAssessmentMergedTriggerPrincipals(t *testing.T) {
	item := mergeAllRuns([]triggeredRun{{taskID: 11, agentKey: "auto"}, {taskID: 22, agentKey: "auto"}, {taskID: 11, agentKey: "auto"}})
	if len(item.taskIDs) != 2 || item.taskIDs[0] != 11 || item.taskIDs[1] != 22 {
		t.Fatalf("merged principals lost: %v", item.taskIDs)
	}
}
