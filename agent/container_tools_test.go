package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/execution"
)

type containerBackendFixture struct {
	command execution.Command
	result  execution.CommandResult
}

func (b *containerBackendFixture) Run(_ context.Context, c execution.Command) (execution.CommandResult, error) {
	b.command = c
	return b.result, nil
}

func containerCatalogFixture(t *testing.T, mode string, b *containerBackendFixture) {
	t.Helper()
	oldRoots, oldFailed, oldSkill := FileRoots, FileAccessFailed, SkillReadRoot
	oldResolve, oldAugment := ToolResolve, ToolAugment
	t.Cleanup(func() {
		FileRoots, FileAccessFailed, SkillReadRoot = oldRoots, oldFailed, oldSkill
		ToolResolve, ToolAugment = oldResolve, oldAugment
	})
	w := t.TempDir()
	SkillReadRoot = ""
	FileAccessFailed = nil
	ToolAugment = nil
	ToolResolve = nil
	FileRoots = func(context.Context, string) (execution.Roots, bool, error) {
		return execution.Roots{Mode: mode, TaskID: "fixture", Workspace: w, WorkingDir: w, Read: []string{w}, Write: []string{w}, Backend: b}, mode != execution.Host, nil
	}
}

func TestContainerToolRoutingPerAssessment(t *testing.T) {
	for _, mode := range []string{execution.Host, execution.ConfinedFiles, execution.Container} {
		for _, typ := range []string{db.AssessmentGeneral, db.AssessmentWebApp, db.AssessmentNetwork, db.AssessmentCodeReview} {
			t.Run(mode+"/"+typ, func(t *testing.T) {
				b := &containerBackendFixture{result: execution.CommandResult{Output: "sandbox output"}}
				containerCatalogFixture(t, mode, b)
				key := TaskAgentKey(typ, "worker")
				base := baseToolsFor(key)
				for _, name := range []string{"TaskOutput", "TaskStop", "TaskList", "Monitor", "shell_open", "shell_send", "shell_read", "shell_close", "shell_list", "mcp__local__tool", "custom_command", "custom_script", "create_mcp"} {
					base = append(base, actool.Build(actool.Spec{Name: name, Schema: map[string]any{"type": "object"}}))
				}
				ctx := WithRunInfo(t.Context(), RunInfo{AssessmentType: typ})
				tools, _, close := AugmentTools(ctx, key, base)
				defer close()
				names := map[string]bool{}
				for _, tool := range tools {
					names[tool.Name()] = true
					if tool.Name() == "Bash" {
						if mode == execution.Container {
							if !isContainerTool(tool) {
								t.Fatal("host Bash survived")
							}
							res, err := tool.Call(ctx, json.RawMessage(`{"command":"printf sandbox","timeout_ms":1234}`), &actool.ToolContext{})
							if err != nil || res.Flatten() != "sandbox output" || b.command.Shell != "printf sandbox" || b.command.Timeout.Milliseconds() != 1234 {
								t.Fatalf("route: %v %v %+v", res, err, b.command)
							}
						}
					}
				}
				if typ == db.AssessmentCodeReview {
					if names["Bash"] {
						t.Fatal("source review gained shell")
					}
					return
				}
				for _, name := range []string{"TaskOutput", "shell_open", "mcp__local__tool", "custom_command", "custom_script", "create_mcp"} {
					if names[name] != (mode != execution.Container) {
						t.Fatalf("%s availability in %s: %v", name, mode, names)
					}
				}
				for _, name := range []string{"Bash", "Read", "Write", "Grep"} {
					if !names[name] {
						t.Fatalf("lost %s", name)
					}
				}
			})
		}
	}
}

func TestContainerBashPreservesSchemaAndPermissionText(t *testing.T) {
	b := &containerBackendFixture{}
	containerCatalogFixture(t, execution.Container, b)
	tools, _, close := AugmentTools(t.Context(), "worker", []actool.CoreTool{actool.NewBash()})
	defer close()
	base := actool.NewBash()
	tool := tools[0]
	if !reflect.DeepEqual(base.InputSchema(), tool.InputSchema()) {
		t.Fatal("Bash schema changed")
	}
	raw := json.RawMessage(`{"command":"printf reviewed"}`)
	if !reflect.DeepEqual(base.CheckPermissions(t.Context(), raw, permission.Context{}), tool.CheckPermissions(t.Context(), raw, permission.Context{})) {
		t.Fatal("approval text changed")
	}
	res, err := tool.Call(t.Context(), json.RawMessage(`{"command":"printf no","run_in_background":true}`), nil)
	if err != nil || !res.IsError || b.command.Shell != "" {
		t.Fatal("background command launched")
	}
	if !strings.Contains(res.Flatten(), "unavailable") {
		t.Fatal(res)
	}
	opts := agentcore.Options{Tools: tools}
	configureContainerOptions(&opts)
	if !opts.DisableBackgroundTasks {
		t.Fatal("SDK would restore background management tools")
	}
}

func TestContainerKeepsClassifiedServerHTTPAndDomainTools(t *testing.T) {
	b := &containerBackendFixture{}
	containerCatalogFixture(t, execution.Container, b)
	http := ContainerServerTool(actool.Build(actool.Spec{Name: "operator_http", Schema: map[string]any{"type": "object"}}))
	base := append(NewToolSet(nil, "").WorkerTools(), http)
	tools, _, close := AugmentTools(t.Context(), "worker", base)
	defer close()
	names := map[string]bool{}
	for _, t := range tools {
		names[t.Name()] = true
	}
	if !names["operator_http"] || !names["report_finding"] || !names["record_fact"] {
		t.Fatalf("safe tools lost: %v", names)
	}
}

type containerReviewFixture struct {
	command string
	deny    bool
	calls   int
}

func (h *containerReviewFixture) PreToolUse(_ context.Context, name string, raw []byte) (bool, string, []byte) {
	if name == "Bash" {
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(raw, &in)
		h.command = in.Command
		h.calls++
	}
	return h.deny, "review fixture", nil
}
func (*containerReviewFixture) PostToolUse(context.Context, string, []byte, []byte, bool) {}
func (*containerReviewFixture) Stop(context.Context, []llm.Message) (bool, []string, string) {
	return false, nil, ""
}

func TestContainerBashUsesExistingInterceptionHooks(t *testing.T) {
	for _, deny := range []bool{true, false} {
		t.Run(map[bool]string{true: "denied", false: "allowed"}[deny], func(t *testing.T) {
			b := &containerBackendFixture{}
			containerCatalogFixture(t, execution.Container, b)
			tools, _, close := AugmentTools(t.Context(), "worker", []actool.CoreTool{actool.NewBash()})
			defer close()
			hooks := &containerReviewFixture{deny: deny}
			turn := 0
			provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
				turn++
				if turn == 1 {
					yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: "container-review", ToolName: "Bash"}, nil)
					yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{"command":"printf 'exact reviewed text'"}`}, nil)
					yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"}, nil)
				} else {
					yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
				}
			}}
			_, _, err := captureRun(t.Context(), agentcore.Options{Provider: provider, Tools: tools, Hooks: hooks, DisableBackgroundTasks: true, PermissionMode: permission.ModeBypass, MaxTurns: 2}, "fixture", nil)
			if err != nil || hooks.calls != 1 || hooks.command != "printf 'exact reviewed text'" {
				t.Fatalf("review: %+v %v", hooks, err)
			}
			if deny && b.command.Shell != "" {
				t.Fatal("denied command executed")
			}
			if !deny && b.command.Shell != hooks.command {
				t.Fatal("executed command differs from reviewed command")
			}
		})
	}
}
