package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
)

type glmWireFunction struct {
	Name       string         `json:"name"`
	Arguments  string         `json:"arguments"`
	Parameters map[string]any `json:"parameters"`
}
type glmWireMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
	ToolCallID       string `json:"tool_call_id"`
	ToolCalls        []struct {
		ID       string          `json:"id"`
		Type     string          `json:"type"`
		Function glmWireFunction `json:"function"`
	} `json:"tool_calls"`
}
type glmWireRequest struct {
	Model    string `json:"model"`
	Thinking struct {
		Type string `json:"type"`
	} `json:"thinking"`
	ReasoningEffort string           `json:"reasoning_effort"`
	Stream          bool             `json:"stream"`
	MaxTokens       int              `json:"max_tokens"`
	Messages        []glmWireMessage `json:"messages"`
	Tools           []struct {
		Type     string          `json:"type"`
		Function glmWireFunction `json:"function"`
	} `json:"tools"`
}

// Verify the documented GLM-5.3 OpenAI-compatible HTTP contract through the real
// pinned adapter, with both API path prefixes redirected to a local test server.
// This proves protocol wiring, not provider availability or Coding Plan entitlement.
// Reference: https://docs.z.ai/guides/llm/glm-5.3 (thinking enabled, effort max).
func TestGLM53OpenAIProtocol(t *testing.T) {
	for _, endpoint := range []struct{ name, basePath, completionPath string }{
		{"general API", "/api/paas/v4", "/api/paas/v4/chat/completions"},
		{"Coding Plan API", "/api/coding/paas/v4", "/api/coding/paas/v4/chat/completions"},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			received := make(chan glmWireRequest, 3)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != endpoint.completionPath {
					t.Errorf("request=%s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer offline-test-key" {
					t.Error("Bearer authentication was not sent correctly")
				}
				if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
					t.Errorf("content type=%q", r.Header.Get("Content-Type"))
				}
				var body glmWireRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "invalid JSON", 400)
					return
				}
				received <- body
				if body.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					// Fragment both text and function arguments to verify real stream assembly.
					for _, frame := range []string{
						`{"id":"local-glm","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Use supplied city."}}]}`,
						`{"id":"local-glm","choices":[{"index":0,"delta":{"content":"Checking "}}]}`,
						`{"id":"local-glm","choices":[{"index":0,"delta":{"content":"weather."}}]}`,
						`{"id":"local-glm","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"lookup_weather","arguments":"{\"city\":"}}]}}]}`,
						`{"id":"local-glm","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Seoul\"}"}}]}}]}`,
						`{"id":"local-glm","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":8}}`,
					} {
						fmt.Fprintf(w, "data: %s\n\n", frame)
						w.(http.Flusher).Flush()
					}
					fmt.Fprint(w, "data: [DONE]\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if len(body.Messages) > 0 && body.Messages[len(body.Messages)-1].Role == "tool" {
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Seoul: clear."},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4}}`)
					return
				}
				fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Checking weather.","reasoning_content":"Use supplied city.","tool_calls":[{"id":"call_weather","type":"function","function":{"name":"lookup_weather","arguments":"{\"city\":\"Seoul\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":8}}`)
			}))
			defer srv.Close()
			cfg := Config{Format: llm.FormatOpenAI, BaseURL: srv.URL + endpoint.basePath, APIKey: "offline-test-key", Model: "glm-5.3", ThinkingType: "enabled", ReasoningEffort: "max", Stream: true, Retry: RetryConfig{ConnectAttempts: -1, EmptyAttempts: -1}}
			provider, err := cfg.NewProvider()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			req := llm.CompletionRequest{System: []string{"Use the supplied weather lookup tool."}, Messages: []llm.Message{llm.UserText("Weather in Seoul?")}, MaxTokens: 4096, Tools: []llm.ToolSchema{{Name: "lookup_weather", Description: "Look up weather for a city.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}}}}}
			checkWire := func(wire glmWireRequest, stream bool) {
				t.Helper()
				if wire.Model != "glm-5.3" || wire.Thinking.Type != "enabled" || wire.ReasoningEffort != "max" || wire.Stream != stream || wire.MaxTokens != 4096 {
					t.Fatalf("wrong GLM request settings: %+v", wire)
				}
				if len(wire.Tools) != 1 || wire.Tools[0].Type != "function" || wire.Tools[0].Function.Name != "lookup_weather" || wire.Tools[0].Function.Parameters["type"] != "object" {
					t.Fatalf("function declaration missing: %+v", wire.Tools)
				}
				if len(wire.Messages) < 2 || wire.Messages[0].Role != "system" || wire.Messages[1].Content != "Weather in Seoul?" {
					t.Fatalf("system/user content changed: %+v", wire.Messages)
				}
			}
			checkReply := func(msg llm.Message, stop string, usage llm.Usage) {
				t.Helper()
				if msg.Text() != "Checking weather." || stop != "tool_use" {
					t.Fatalf("text/stop=%q/%q", msg.Text(), stop)
				}
				var reasoning string
				for _, block := range msg.Content {
					if block.Type == llm.BlockThinking {
						reasoning += block.Thinking
					}
				}
				if reasoning != "Use supplied city." {
					t.Fatalf("reasoning_content lost: %q", reasoning)
				}
				calls := msg.ToolUses()
				if len(calls) != 1 || calls[0].ID != "call_weather" || calls[0].Name != "lookup_weather" || string(calls[0].Input) != `{"city":"Seoul"}` {
					t.Fatalf("function call changed: %+v", calls)
				}
				if usage.InputTokens != 12 || usage.OutputTokens != 8 {
					t.Fatalf("usage=%+v", usage)
				}
			}
			first, stop, usage, err := provider.Complete(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			checkWire(<-received, false)
			checkReply(first, stop, usage)
			// Return the function result and ensure the previous reasoning and call ID
			// survive serialization, as required for a reasoning-enabled tool round trip.
			follow := req
			follow.Messages = append(append([]llm.Message(nil), req.Messages...), first, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText("call_weather", "clear", false)}})
			final, stop, _, err := provider.Complete(ctx, follow)
			if err != nil {
				t.Fatal(err)
			}
			wire := <-received
			checkWire(wire, false)
			if len(wire.Messages) != 4 || wire.Messages[2].ReasoningContent != "Use supplied city." || len(wire.Messages[2].ToolCalls) != 1 || wire.Messages[2].ToolCalls[0].ID != "call_weather" || wire.Messages[3].Role != "tool" || wire.Messages[3].ToolCallID != "call_weather" || wire.Messages[3].Content != "clear" {
				t.Fatalf("tool round-trip history changed: %+v", wire.Messages)
			}
			if final.Text() != "Seoul: clear." || stop != "end_turn" {
				t.Fatalf("final=%q stop=%q", final.Text(), stop)
			}
			var acc llm.Accumulator
			for event, err := range provider.Stream(ctx, req) {
				if err != nil {
					t.Fatal(err)
				}
				acc.Add(event)
			}
			checkWire(<-received, true)
			checkReply(acc.Message(), acc.StopReason, acc.Usage)
		})
	}
}

// Auxiliary calls such as compaction and approval review request disabled thinking.
// GLM-5.3 rejects that mode; other models must retain their requested override.
func TestGLM53ThinkingRequirement(t *testing.T) {
	for _, model := range []string{"glm-5.3", "glm-5.2", "ordinary-compatible-model"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				received := make(chan glmWireRequest, 1)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var wire glmWireRequest
					if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
						t.Errorf("decode: %v", err)
						http.Error(w, "invalid body", 400)
						return
					}
					received <- wire
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
					}
				}))
				defer srv.Close()
				cfg := Config{Format: llm.FormatOpenAI, BaseURL: srv.URL, APIKey: "offline-test-key", Model: model, ThinkingType: "enabled", ReasoningEffort: "max", Retry: RetryConfig{ConnectAttempts: -1, EmptyAttempts: -1}}
				p, err := cfg.NewProvider()
				if err != nil {
					t.Fatal(err)
				}
				req := llm.CompletionRequest{Messages: []llm.Message{llm.UserText("Summarize supplied context.")}, Thinking: "disabled"}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if stream {
					for _, err := range p.Stream(ctx, req) {
						if err != nil {
							t.Fatal(err)
						}
					}
				} else {
					if _, _, _, err := p.Complete(ctx, req); err != nil {
						t.Fatal(err)
					}
				}
				wire := <-received
				want := "disabled"
				if model == "glm-5.3" {
					want = "enabled"
				}
				if wire.Thinking.Type != want {
					t.Errorf("thinking.type=%q, want %q", wire.Thinking.Type, want)
				}
				if wire.ReasoningEffort != "max" || wire.Model != model || wire.Stream != stream {
					t.Fatalf("unrelated request settings changed: %+v", wire)
				}
				if req.Thinking != "disabled" {
					t.Fatal("caller request was mutated")
				}
			})
		}
	}
}
