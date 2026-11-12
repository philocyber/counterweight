package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// The endpoint a connection test talks to is not trusted. When it answers the
// ping with a tool call, nothing runs and nothing is written, because the
// test session offers and holds no tools.
func TestConnectionTestGivesEndpointNoTools(t *testing.T) {
	t.Setenv("AGENT_CORE_DISABLE_BACKGROUND_TASKS", "")
	for _, tool := range []string{"Write", "Bash", "Monitor"} {
		t.Run(tool, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "conntest-marker")
			command := "echo fixture > '" + marker + "'"
			if runtime.GOOS == "windows" {
				command = "Set-Content -Path '" + marker + "' -Value fixture"
			}
			input := map[string]any{"command": command}
			if tool == "Write" {
				input = map[string]any{"file_path": marker, "content": "fixture"}
			}
			arguments, _ := json.Marshal(input)
			partial, _ := json.Marshal(string(arguments))
			sse := strings.Join([]string{
				`event: message_start`,
				`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":1,"output_tokens":1}}}`,
				``,
				`event: content_block_start`,
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"` + tool + `","input":{}}}`,
				``,
				`event: content_block_delta`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + string(partial) + `}}`,
				``,
				`event: content_block_stop`,
				`data: {"type":"content_block_stop","index":0}`,
				``,
				`event: message_delta`,
				`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`,
				``,
				`event: message_stop`,
				`data: {"type":"message_stop"}`,
				``,
			}, "\n")
			var offered []string
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
				}
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &body)
				for _, offer := range body.Tools {
					offered = append(offered, offer.Name)
				}
				w.Header().Set("content-type", "text/event-stream")
				_, _ = io.WriteString(w, sse)
			}))
			defer srv.Close()
			_, _, _ = TestConnection(t.Context(), Config{Format: llm.FormatAnthropic, BaseURL: srv.URL, APIKey: "test-key", Model: "fixture-model", Stream: true})
			if requests == 0 {
				t.Fatal("the fixture endpoint was never asked")
			}
			if len(offered) != 0 {
				t.Errorf("connection test offered tools to the endpoint: %v", offered)
			}
			// A background process, had one been started, gets time to write.
			deadline := time.Now().Add(1500 * time.Millisecond)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatalf("a %s call from the endpoint under test was executed", tool)
			}
		})
	}
}
