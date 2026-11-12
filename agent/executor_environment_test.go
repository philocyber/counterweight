package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func TestExecutorChildrenDoNotInheritServerCredentials(t *testing.T) {
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	probePath := os.Getenv("COUNTERWEIGHT_TEST_ENV_PROBE")
	if python == "" && probePath == "" {
		t.Skip("requires explicitly selected Python")
	}
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "COUNTERWEIGHT_PG_DSN", "COUNTERWEIGHT_LLM_PROXY"} {
		t.Setenv(key, "SW_SERVER_ENV_FIXTURE")
	}
	t.Setenv("AGENT_CORE_DISABLE_BACKGROUND_TASKS", "")
	t.Setenv("AGENT_CORE_DISABLE_INTERACTIVE_SHELL", "")
	code := `import os,json,base64;print('SW_ENV_RESULT='+base64.b64encode(json.dumps({k:os.getenv(k,'') for k in ['OPENAI_API_KEY','ANTHROPIC_API_KEY','COUNTERWEIGHT_PG_DSN','COUNTERWEIGHT_LLM_PROXY','SW_BENIGN','HTTP_PROXY']}).encode()).decode())`
	argument := "import base64;exec(base64.b64decode('" + base64.StdEncoding.EncodeToString([]byte(code)) + "'))"
	command := "'" + strings.ReplaceAll(python, "'", "''") + "' -c \"" + argument + "\""
	if probePath != "" {
		command = "'" + strings.ReplaceAll(probePath, "'", "''") + "'"
	}
	if runtime.GOOS == "windows" {
		command = "& " + command
	}
	for _, mode := range []string{"foreground", "background", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "interactive" && runtime.GOOS == "windows" {
				t.Skip("norma v0.4.3 PTY executor is unsupported on Windows")
			}
			var observed string
			probe := tool.Build(tool.Spec{Name: "ExecutorEnvProbe", Schema: map[string]any{"type": "object"}, Run: func(ctx context.Context, _ json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
				input, _ := json.Marshal(map[string]any{"command": command, "run_in_background": mode == "background"})
				var result tool.Result
				var err error
				if mode == "interactive" {
					input, _ = json.Marshal(map[string]any{"command": command, "mode": "interactive", "quiet_ms": 100, "startup_grace_ms": 1500})
					result, err = tool.NewShellOpen().Call(ctx, input, tc)
					var out struct {
						Output string `json:"output"`
					}
					if err == nil && !result.IsError {
						err = json.Unmarshal([]byte(result.Flatten()), &out)
						observed = out.Output
					}
				} else {
					result, err = tool.NewBash().Call(ctx, input, tc)
					observed = result.Flatten()
					if mode == "background" && err == nil && !result.IsError {
						jobs := tc.Tasks.List()
						if len(jobs) != 1 {
							return tool.Errorf("expected one fixture job"), nil
						}
						if _, done := tc.Tasks.Wait(ctx, jobs[0].ID, 5*time.Second); !done {
							return tool.Errorf("fixture job did not finish"), nil
						}
						observed, err = tc.Tasks.Output(jobs[0].ID, 0)
					}
				}
				if err != nil || result.IsError {
					return tool.Errorf(fmt.Sprintf("native executor fixture failed: %v %s", err, result.Flatten())), nil
				}
				return tool.Text("fixture complete"), nil
			}})
			turns := 0
			provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
				turns++
				if turns == 1 {
					for _, e := range []llm.StreamEvent{{Type: llm.SEToolUseStart, ToolID: "env-fixture", ToolName: probe.Name()}, {Type: llm.SEToolInputJSON, Text: `{}`}, {Type: llm.SEMessageDelta, StopReason: "tool_use"}} {
						if !yield(e, nil) {
							return
						}
					}
				} else {
					yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
				}
			}}
			opts := agentcore.Options{Provider: provider, Tools: []tool.CoreTool{probe}, PermissionMode: permission.ModeBypass, WorkingDir: t.TempDir(), MaxTurns: 2, DisableBackgroundTasks: mode == "foreground", BashEnv: []string{"OPENAI_API_KEY=SW_SERVER_ENV_OVERRIDE", "SW_BENIGN=SAFE_KEEP", "HTTP_PROXY=http://127.0.0.1:8788"}}
			if _, _, err := captureRun(t.Context(), opts, "run the inert environment fixture", nil); err != nil {
				t.Fatal(err)
			}
			match := regexp.MustCompile(`SW_ENV_RESULT=([A-Za-z0-9+/=]+)`).FindStringSubmatch(observed)
			if len(match) != 2 {
				t.Fatalf("native executor produced no verified environment output: %q", observed)
			}
			decoded, err := base64.StdEncoding.DecodeString(match[1])
			if err != nil {
				t.Fatal(err)
			}
			var values map[string]string
			if json.Unmarshal(decoded, &values) != nil {
				t.Fatalf("invalid fixture JSON: %q", decoded)
			}
			for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "COUNTERWEIGHT_PG_DSN", "COUNTERWEIGHT_LLM_PROXY"} {
				if values[key] != "" {
					t.Errorf("%s leaked a controlled credential: %q", key, values[key])
				}
				if os.Getenv(key) != "SW_SERVER_ENV_FIXTURE" {
					t.Errorf("parent provider environment changed: %s", key)
				}
			}
			if values["SW_BENIGN"] != "SAFE_KEEP" || values["HTTP_PROXY"] != "http://127.0.0.1:8788" {
				t.Fatalf("task/proxy environment changed: %v", values)
			}
		})
	}
}
