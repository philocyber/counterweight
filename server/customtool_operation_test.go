package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
)

type customOperationProvider struct {
	name  string
	input json.RawMessage
	calls int
}

func (p *customOperationProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.calls++
	if p.calls == 1 {
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "operation-call", Name: p.name, Input: p.input}}}, "tool_use", llm.Usage{}, nil
	}
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("fixture complete")}}, "end_turn", llm.Usage{}, nil
}
func (p *customOperationProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
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

// Exercise production ChatAgent -> decorators -> SDK -> guard -> native
// executor -> audit completion with synthetic providers and loopback/temp targets.
func TestCustomOperationsReviewResolvedPlansThroughSDK(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires an explicit test Python interpreter")
	}
	if _, err := os.Stat(python); err != nil {
		t.Fatal(err)
	}
	ic := intercept.New(d)
	// This fixture exercises the optional model judge. Restrictive input rules
	// now require independent human authorization; that path has a separate
	// native-dispatch fixture which verifies the judge cannot override it.
	priorRules, err := d.ListInterceptRules()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, rule := range priorRules {
			_, _ = d.Exec(`UPDATE intercept_rules SET enabled=$1 WHERE id=$2`, rule.Enabled, rule.ID)
		}
	})
	for _, rule := range priorRules {
		if rule.Enabled {
			if _, err := d.Exec(`UPDATE intercept_rules SET enabled=false WHERE id=$1`, rule.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	priorTools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	priorConfig := ic.GetJudgeConfig()
	priorPython, priorPythonOK, err := d.GetSetting(settingPythonInterp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ic.SetEnabledTools(priorTools)
		_ = ic.SetJudgeConfig(priorConfig)
		if priorPythonOK {
			_ = d.SetSetting(settingPythonInterp, priorPython)
		} else {
			_, _ = d.Exec(`DELETE FROM settings WHERE key=$1`, settingPythonInterp)
		}
	})
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true, AskTimeoutSeconds: 2, AskTimeoutAction: "deny"}); err != nil {
		t.Fatal(err)
	}
	priorResolve, priorAugment := agent.ToolResolve, agent.ToolAugment
	agent.ToolResolve = nil
	t.Cleanup(func() { agent.ToolResolve, agent.ToolAugment = priorResolve, priorAugment })
	for _, kind := range []string{"command", "script", "http"} {
		for _, action := range []string{"deny", "allow", "ask", "human_deny"} {
			for _, lang := range []locale.Lang{locale.En} {
				for _, deferred := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/%s/deferred=%v", kind, action, lang, deferred)
					t.Run(name, func(t *testing.T) {
						workDir := t.TempDir()
						target := filepath.Join(workDir, "approved-operation.txt")
						var requests atomic.Int32
						listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							requests.Add(1)
							if r.Method != "POST" || r.Header.Get("X-Fixture") != "OPERATION_DEFAULT" || r.URL.Path != "/OPERATION_DEFAULT" {
								t.Errorf("unreviewed request: %s %s %v", r.Method, r.URL, r.Header)
							}
							_, _ = w.Write([]byte("operation received"))
						}))
						defer listener.Close()
						if err := d.SetSetting(settingPythonInterp, python); err != nil {
							t.Fatal(err)
						}
						execSpec := map[string]any{}
						switch kind {
						case "command":
							execSpec["command"] = "echo frozen-operation > \"" + target + "\""
						case "script":
							encoded, _ := json.Marshal(target)
							execSpec["code"] = "import pathlib,os,json,sys\np=json.load(sys.stdin)\nassert p['token']=='OPERATION_DEFAULT'\nassert os.environ['TOOL_TOKEN']=='OPERATION_DEFAULT'\npathlib.Path(" + string(encoded) + ").write_text('frozen-operation')\n"
						case "http":
							execSpec = map[string]any{"method": "POST", "url": listener.URL + "/{token}", "headers": map[string]string{"X-Fixture": "{token}"}, "body": "body-{token}"}
						}
						execRaw, _ := json.Marshal(execSpec)
						row := &db.Tool{Key: "operation_fixture", Kind: kind, Exec: execRaw, Schema: json.RawMessage(`{"type":"object","properties":{"token":{"type":"string","default":"OPERATION_DEFAULT"}}}`)}
						s := &Server{m: &Manager{pg: d, dir: workDir}}
						tool := s.buildCustomToolForLanguage(row, lang)
						var schema map[string]any
						_ = json.Unmarshal(row.Schema, &schema)
						tool = meterTool(agent.DecorateTool(tool, "fixture", schema), d, row.Key, "fixture", agent.RunInfo{})
						agent.ToolAugment = func(context.Context, string) ([]actool.CoreTool, agent.DeferredInfo, func()) {
							def := agent.DeferredInfo{}
							if deferred {
								def.Deferred = []string{row.Key}
								def.GlobalNames = []string{row.Key}
							}
							return []actool.CoreTool{tool}, def, func() {}
						}
						// Only the underlying executor is selected, proving that custom keys
						// cannot escape the existing Bash/WebFetch control selection.
						executor := "Bash"
						if kind == "http" {
							executor = "WebFetch"
						}
						if err := ic.SetEnabledTools([]string{executor}); err != nil {
							t.Fatal(err)
						}
						reviews := 0
						operationDir := ""
						ic.SetReviewer(func(_ context.Context, _ int64, _ string, input intercept.ReviewInput) (intercept.Decision, error) {
							reviews++
							if input.Tool != row.Key {
								t.Errorf("review lost registered custom name: %s", input.Tool)
							}
							var op map[string]any
							if err := json.Unmarshal(input.Arguments, &op); err != nil {
								t.Fatal(err)
							}
							if op["kind"] != kind || op["params"].(map[string]any)["token"] != "OPERATION_DEFAULT" {
								t.Errorf("raw/default-free review: %s", input.Arguments)
							}
							switch kind {
							case "command":
								if op["command"] != execSpec["command"] || op["working_directory"] == "" {
									t.Errorf("command not resolved: %s", input.Arguments)
								}
							case "script":
								operationDir, _ = op["working_directory"].(string)
								if op["code"] != execSpec["code"] || op["interpreter"] != python || op["stdin"] != `{"token":"OPERATION_DEFAULT"}` || op["environment_sha256"] == "" {
									t.Errorf("script not resolved: %s", input.Arguments)
								}
							case "http":
								if op["method"] != "POST" || op["url"] != listener.URL+"/OPERATION_DEFAULT" || op["body"] != "body-OPERATION_DEFAULT" {
									t.Errorf("HTTP operation not resolved: %s", input.Arguments)
								}
							}
							verdict := action
							if action == "human_deny" {
								verdict = "ask"
							}
							return intercept.Decision{Action: verdict, Message: "fixture operation verdict"}, nil
						})
						taskID := "operation-" + strings.ReplaceAll(name, "/", "-") + fmt.Sprintf("-%d", time.Now().UnixNano())
						t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID) })
						base, cancel := context.WithTimeout(t.Context(), 5*time.Second)
						defer cancel()
						ctx := intercept.WithTaskContext(locale.WithLang(base, lang), taskID, "fixture", func(a db.Activity) {
							if a.Kind != "intercept_request" || (action != "ask" && action != "human_deny") {
								return
							}
							var pending struct {
								ID int64 `json:"pending_id"`
							}
							if err := json.Unmarshal([]byte(a.Detail), &pending); err != nil {
								t.Error(err)
								return
							}
							// Change settings and the captured row's bytes while approval is
							// pending. The accepted execution must use the earlier plan.
							for i := range row.Exec {
								row.Exec[i] = ' '
							}
							_ = d.SetSetting(settingPythonInterp, filepath.Join(workDir, "different-interpreter.exe"))
							if err := ic.Decide(pending.ID, action == "ask"); err != nil {
								t.Error(err)
							}
						})
						provider := &customOperationProvider{name: row.Key, input: json.RawMessage(`{}`)}
						if deferred {
							provider.name = actool.ExecuteExtraToolName
							provider.input = json.RawMessage(`{"tool_name":"operation_fixture","params":{}}`)
						}
						chat := agent.NewChatAgent(provider, "fixture", workDir, nil, 100000)
						chat.SetGuard(guard.NewWithInterceptor(ic))
						chat.SetNonStreaming(func() bool { return deferred })
						var result db.Activity
						if _, err := chat.Chat(ctx, "fixture", "operation-session", "execute the fixture", 2, 0, false, func(a db.Activity) {
							if a.Kind == "tool_result" {
								result = a
							}
						}); err != nil {
							t.Fatal(err)
						}
						wantRun := action == "allow" || action == "ask"
						if reviews != 1 || result.ToolUseID != "operation-call" || result.IsError == wantRun {
							t.Fatalf("review/execution path: reviews=%d result=%+v", reviews, result)
						}
						if kind == "http" {
							if (requests.Load() == 1) != wantRun {
								t.Fatalf("HTTP calls: %d", requests.Load())
							}
						} else {
							content, err := os.ReadFile(target)
							if wantRun {
								text := string(content)
								if len(content) >= 2 && content[0] == 0xff && content[1] == 0xfe {
									units := make([]uint16, (len(content)-2)/2)
									for i := range units {
										units[i] = binary.LittleEndian.Uint16(content[2+2*i : 4+2*i])
									}
									text = string(utf16.Decode(units))
								}
								if err != nil || strings.TrimSpace(text) != "frozen-operation" {
									t.Fatalf("approved effect missing: %q %v result=%s", content, err, result.Detail)
								}
							} else if !os.IsNotExist(err) {
								t.Fatalf("denied operation wrote marker: %v", err)
							}
							if kind == "script" && !wantRun {
								if operationDir == "" {
									t.Fatal("script working directory was not reviewed")
								}
								if _, err := os.Stat(filepath.Join(operationDir, ".tools")); !os.IsNotExist(err) {
									t.Fatal("script file written before denial")
								}
							}
						}
						rows, err := d.ListTaskIntercepts(taskID)
						if err != nil || len(rows) != 1 {
							t.Fatalf("duplicate/missing operation audit: %d %v", len(rows), err)
						}
						detail, err := d.GetInterceptDetail(rows[0].ID)
						if err != nil {
							t.Fatal(err)
						}
						if detail.Audit == nil || detail.Audit.ToolUseID != "operation-call" || detail.Audit.Correlation != "exact" || detail.ToolName != row.Key {
							t.Fatalf("lost resolved invocation correlation: %+v", detail)
						}
						status := "succeeded"
						if !wantRun {
							status = "not_executed"
						}
						if detail.Audit.ExecutionStatus != status {
							t.Fatalf("wrong execution status: %+v", detail.Audit)
						}
					})
				}
			}
		}
	}
}
