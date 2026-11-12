package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	actool "github.com/Autumn-27/norma/tool"
)

func TestCommandToolDeclaredParametersThroughNativeSDK(t *testing.T) {
	previous, priorResolve := agent.ToolAugment, agent.ToolResolve
	// A resolver left by an earlier test reads a database that is closed by now
	// and returns no tools at all.
	agent.ToolResolve = nil
	t.Cleanup(func() { agent.ToolAugment, agent.ToolResolve = previous, priorResolve })
	for _, lang := range []locale.Lang{locale.En} {
		for _, deferred := range []bool{false, true} {
			for _, mode := range []string{"declared", "default", "extra", "metadata-change", "fallback", "fallback-extra"} {
				t.Run(string(lang)+"/"+map[bool]string{false: "direct", true: "deferred"}[deferred]+"/"+mode, func(t *testing.T) {
					dir := t.TempDir()
					marker := filepath.Join(dir, "native-parameter-result.txt")
					parameter := "token"
					var schema json.RawMessage = json.RawMessage(`{"type":"object","properties":{"token":{"type":"string","default":"DECLARED_DEFAULT"}},"additionalProperties":true}`)
					input := json.RawMessage(`{"token":"DECLARED_VALUE"}`)
					want := "DECLARED_VALUE"
					blocked := mode == "extra" || mode == "metadata-change" || mode == "fallback-extra"
					switch mode {
					case "default":
						input = json.RawMessage(`{}`)
						want = "DECLARED_DEFAULT"
					case "extra", "metadata-change":
						input = json.RawMessage(`{"token":"DECLARED_VALUE","ghost":"{token}; $(inert)"}`)
					case "fallback":
						schema = nil
						parameter = "args"
						input = json.RawMessage(`{"args":"FALLBACK_VALUE"}`)
						want = "FALLBACK_VALUE"
					case "fallback-extra":
						schema = nil
						parameter = "args"
						input = json.RawMessage(`{"args":"FALLBACK_VALUE","ghost":"{args}"}`)
					}
					execRaw, _ := json.Marshal(commandExec{Command: `echo {` + parameter + `} > "` + filepath.ToSlash(marker) + `"`})
					row := &db.Tool{Key: "parameter_fixture", Kind: "command", Exec: execRaw, Schema: schema}
					tool := (&Server{}).buildCustomToolForLanguage(row, lang)
					if tool.InputSchema()["additionalProperties"] != false {
						t.Fatal("model schema still advertises undeclared command parameters")
					}
					if mode == "metadata-change" {
						tool.InputSchema()["properties"].(map[string]any)["ghost"] = map[string]any{"type": "string"}
						tool.InputSchema()["additionalProperties"] = true
					}
					tool = agent.DecorateTool(tool, "fixture", tool.InputSchema())
					agent.ToolAugment = func(context.Context, string) ([]actool.CoreTool, agent.DeferredInfo, func()) {
						info := agent.DeferredInfo{}
						if deferred {
							info.Deferred = []string{row.Key}
							info.GlobalNames = []string{row.Key}
						}
						return []actool.CoreTool{tool}, info, func() {}
					}
					provider := &customOperationProvider{name: row.Key, input: input}
					if deferred {
						provider.name = actool.ExecuteExtraToolName
						provider.input, _ = json.Marshal(map[string]any{"tool_name": row.Key, "params": json.RawMessage(input)})
					}
					chat := agent.NewChatAgent(provider, "fixture", dir, nil, 100000)
					chat.SetNonStreaming(func() bool { return deferred })
					ctx, cancel := context.WithTimeout(locale.WithLang(t.Context(), lang), 5*time.Second)
					defer cancel()
					var result db.Activity
					_, err := chat.Chat(ctx, "fixture", "parameter-session", "run fixture", 2, 0, false, func(a db.Activity) {
						if a.Kind == "tool_result" {
							result = a
						}
					})
					if err != nil {
						t.Fatal(err)
					}
					content, readErr := os.ReadFile(marker)
					if result.ToolUseID != "operation-call" || result.IsError != blocked {
						t.Fatalf("native parameter result: %+v marker=%q marker_error=%v", result, content, readErr)
					}
					text := string(content)
					// Windows PowerShell's unchanged redirection writes UTF-16LE.
					// Decode the recorded bytes before comparing the actual value.
					if len(content) >= 2 && content[0] == 0xff && content[1] == 0xfe {
						if len(content)%2 != 0 {
							t.Fatal("truncated UTF-16 marker")
						}
						units := make([]uint16, (len(content)-2)/2)
						for i := range units {
							units[i] = binary.LittleEndian.Uint16(content[2+i*2:])
						}
						text = string(utf16.Decode(units))
					}
					if blocked {
						if !os.IsNotExist(readErr) {
							t.Fatalf("undeclared parameters reached native execution: %q %v", content, readErr)
						}
						if !strings.Contains(result.Detail, locale.Text(lang, "Command tool parameters must be declared in its schema")) {
							t.Fatalf("wrong localized refusal: %s", result.Detail)
						}
					} else if readErr != nil || strings.TrimSpace(text) != want {
						t.Fatalf("declared/default/fallback execution changed: %q %v", content, readErr)
					}
				})
			}
		}
	}
}
