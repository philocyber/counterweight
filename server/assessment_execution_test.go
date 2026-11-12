package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
)

func TestAssessmentAssembleStoredBindingsAndSkills(t *testing.T) {
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
	oldTrafficBinding := agent.FindingTrafficBindingEnabled
	t.Cleanup(func() {
		agent.ToolAugment, agent.ToolResolve = oldAugment, oldResolve
		agent.FindingTrafficBindingEnabled = oldTrafficBinding
	})
	dir := t.TempDir()
	skills := []string{"whitebox-code-review-methodology", "dom-vulnerability-static-analysis", "custom-sanitizer-audit", "vuln-assessment-methodology", "api-recon", "http-query-method", "playwright-cli"}
	for _, name := range skills {
		if err := os.MkdirAll(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: fixture "+name+"\n---\nRead fixture source."), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Emulate an already seeded installation: the old rows lack new agent bindings.
	wireTools(pg, buildDomainReg(pg.Assets()))
	marker, exists, err := pg.GetSetting("code_review_execution_bindings_v1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if exists {
			_ = pg.SetSetting("code_review_execution_bindings_v1", marker)
		} else {
			_, _ = pg.Exec(`DELETE FROM settings WHERE key='code_review_execution_bindings_v1'`)
		}
	})
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
	for _, key := range []string{db.CodeReviewPlannerAgentKey, db.CodeReviewWorkerAgentKey} {
		if err := pg.RemoveAgentFromToolBindings(key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pg.Exec(`DELETE FROM settings WHERE key='code_review_execution_bindings_v1'`); err != nil {
		t.Fatal(err)
	}
	wireTools(pg, buildDomainReg(pg.Assets()))
	for _, key := range []string{db.CodeReviewPlannerAgentKey, db.CodeReviewWorkerAgentKey} {
		report, err := pg.GetTool("report_finding")
		if err != nil || !contains(report.Agents, key) {
			t.Fatalf("existing catalog not upgraded: %s %v", key, err)
		}
		// An operator attempts to reintroduce assets, scope and interactive shell.
		if err := pg.AddAgentToToolBinding(key, []string{"insert_assets", "add_company_scope", "add_task_scope"}); err != nil {
			t.Fatal(err)
		}
	}
	hostCalls := 0
	wireAgentAugment(pg, dir, func(context.Context) ([]actool.CoreTool, map[string][]string) {
		hostCalls++
		var tools []actool.CoreTool
		for _, name := range []string{"custom_command", "custom_http", "traffic_search", "mcp__fixture__exec"} {
			tools = append(tools, actool.Build(actool.Spec{Name: name, Schema: map[string]any{"type": "object"}}))
		}
		return tools, nil
	})
	for _, typ := range []string{db.AssessmentGeneral, db.AssessmentWebApp, db.AssessmentNetwork, db.AssessmentCodeReview} {
		for _, role := range []string{"planner", "worker"} {
			key := agent.TaskAgentKey(typ, role)
			a, err := pg.GetAgentByKey(key)
			if err != nil || a == nil {
				t.Fatalf("agent missing: %s %v", key, err)
			}
			original, err := pg.AgentSkillNames(a.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = pg.SetAgentSkillVisibility(a.ID, original)
				_, _ = pg.Exec(`UPDATE agents SET interactive_shell=$1 WHERE id=$2`, a.InteractiveShell, a.ID)
			})
			if err := pg.SetAgentSkillVisibility(a.ID, skills); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.Exec(`UPDATE agents SET interactive_shell=true WHERE id=$1`, a.ID); err != nil {
				t.Fatal(err)
			}
			ctx := agent.WithRunInfo(t.Context(), agent.RunInfo{AssessmentType: typ})
			ts := agent.NewToolSet(nil, "")
			base := ts.PlannerTools()
			if role == "worker" {
				base = ts.WorkerTools()
			}
			base = append(base, actool.DefaultTools()...)
			before := hostCalls
			tools, def, cleanup := agent.AugmentTools(ctx, key, base)
			cleanup()
			names := map[string]bool{}
			skillText := ""
			for _, tool := range tools {
				names[tool.Name()] = true
				if tool.Name() == "Skill" {
					raw, _ := json.Marshal(tool.InputSchema())
					skillText = tool.Prompt() + tool.Description() + string(raw)
				}
			}
			if typ == db.AssessmentCodeReview {
				if hostCalls != before {
					t.Error("review constructed host/custom tools")
				}
				for _, name := range []string{"Bash", "Write", "Edit", "MultiEdit", "shell_open", "shell_read", "shell_send", "shell_close", "shell_list", "insert_assets", "add_company_scope", "add_task_scope", "traffic_search", "custom_command", "custom_http", "mcp__fixture__exec", "SearchExtraTools", "ExecuteExtraTool"} {
					if names[name] {
						t.Errorf("%s exposed %s despite review type", key, name)
					}
				}
				if len(def.Deferred) > 0 {
					t.Error("review retained deferred tools")
				}
			} else {
				for _, name := range []string{"Bash", "Write", "custom_command", "custom_http", "mcp__fixture__exec"} {
					if !names[name] {
						t.Errorf("%s/%s lost %s", typ, key, name)
					}
				}
			}
			if typ != db.AssessmentCodeReview && names["traffic_search"] != (role == "worker") {
				t.Errorf("%s/%s changed default traffic binding", typ, role)
			}
			for _, name := range skills {
				want := agent.SkillAllowedForAssessment(typ, key, name)
				if strings.Contains(skillText, name) != want {
					t.Errorf("%s/%s skill %s visibility want %v: %s", typ, key, name, want, skillText)
				}
			}
		}
	}
	seedPrompts(pg)
	for _, key := range []string{db.CodeReviewPlannerAgentKey, db.CodeReviewWorkerAgentKey} {
		a, _ := pg.GetAgentByKey(key)
		if a == nil {
			t.Fatalf("missing seeded agent %s", key)
		}
		body, err := pg.CurrentPrompt(a.ID)
		if err != nil || strings.TrimSpace(body) == "" {
			t.Fatalf("missing editable prompt for %s: %v", key, err)
		}
		// Repeated startup must not restore bindings removed through the UI.
		if err := pg.RemoveAgentFromTool(key, "report_finding"); err != nil {
			t.Fatal(err)
		}
		wireTools(pg, buildDomainReg(pg.Assets()))
		row, _ := pg.GetTool("report_finding")
		if contains(row.Agents, key) {
			t.Fatal("restart overwrote operator binding")
		}
	}
}
