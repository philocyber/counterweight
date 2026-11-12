package server

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Autumn-27/norma/llm"
)

type taskGuardFixtureProvider struct {
	path  string
	calls int
}

func (p *taskGuardFixtureProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.calls++
	if p.calls == 1 {
		input, _ := json.Marshal(map[string]string{"file_path": p.path, "content": "this write must be denied"})
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "fixture-write", Name: "Write", Input: input}}}, "tool_use", llm.Usage{}, nil
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("fixture finished")}}, "end_turn", llm.Usage{}, nil
}

func (p *taskGuardFixtureProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		message, stop, _, err := p.Complete(ctx, req)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		for _, block := range message.Content {
			if block.Type == llm.BlockToolUse {
				if !yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: block.ID, ToolName: block.Name}, nil) {
					return
				}
				if !yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(block.Input)}, nil) {
					return
				}
			} else if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: block.Text}, nil) {
				return
			}
		}
		yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: stop}, nil)
	}
}

// Prove the production agentsForTask factory installs the manager's configured
// guard. The attempted Write is confined to a fresh temporary directory.
func TestTaskAgentFactoryGuardsBuiltinWrites(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	priorResolve, priorAugment := agent.ToolResolve, agent.ToolAugment
	agent.ToolResolve, agent.ToolAugment = nil, nil
	t.Cleanup(func() { agent.ToolResolve, agent.ToolAugment = priorResolve, priorAugment })
	priorTools, err := m.interceptor.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	priorConfig := m.interceptor.GetJudgeConfig()
	t.Cleanup(func() { _ = m.interceptor.SetEnabledTools(priorTools); _ = m.interceptor.SetJudgeConfig(priorConfig) })
	if err := m.interceptor.SetEnabledTools([]string{"Write"}); err != nil {
		t.Fatal(err)
	}
	if err := m.interceptor.SetJudgeConfig(intercept.JudgeConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m.interceptor.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
		return intercept.Decision{Action: "deny", Message: "isolated fixture policy"}, nil
	})
	for _, missingGuard := range []bool{false, true} {
		for _, role := range []string{"planner", "mainagent"} {
			t.Run(role+"/fallback="+strconv.FormatBool(missingGuard), func(t *testing.T) {
				task, err := m.CreateTask("isolated factory fixture", "verify deny policy", nil, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				if missingGuard {
					task.Guard = nil
				}
				path := filepath.Join(t.TempDir(), "denied-write.txt")
				provider := &taskGuardFixtureProvider{path: path}
				s := &Server{m: m, ctx: t.Context(), engine: NewEngine(m), taskAgents: make(map[string]*taskAgentBundle), llmOn: true, llmProv: provider}
				bundle := s.agentsForTask(task)
				id, _ := strconv.ParseInt(task.ID, 10, 64)
				t.Cleanup(func() {
					_, _ = m.pg.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, task.ID)
					_ = m.pg.DeleteTask(id)
				})
				if role == "planner" {
					_, _, err = bundle.pl.Plan(t.Context(), id, m.Assets(), task.Store, task.Goal, nil, nil)
				} else {
					_, err = bundle.main.Chat(t.Context(), id, 0, m.Assets(), task.Store, task.Goal, "run the fixture", nil, nil, nil, nil, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("configured deny failed: file stat error=%v", err)
				}
				rows, err := m.pg.ListTaskIntercepts(task.ID)
				if err != nil || len(rows) != 1 || rows[0].Status != "denied" || rows[0].AgentName != role {
					t.Fatalf("factory did not enforce task policy: rows=%+v err=%v", rows, err)
				}
			})
		}
	}
}
