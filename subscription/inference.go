package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/Autumn-27/norma/llm"
)

type provider struct {
	manager                  *Manager
	id, model, effort, cyber string
}

func (m *Manager) Provider(id, model, effort, cyber string) (llm.Provider, error) {
	if _, err := m.Account(id); err != nil {
		return nil, err
	}
	if cyber == "" {
		cyber = "standard"
	}
	if cyber != "standard" && cyber != "daybreakBlue" && cyber != "daybreakRed" {
		return nil, errors.New("Invalid Codex access program")
	}
	return &provider{m, id, model, effort, cyber}, nil
}
func (p *provider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

// Translate the host-owned history into Responses items. Each native thread is
// ephemeral, so compaction, retries and tool execution remain owned by Counterweight.
func history(req llm.CompletionRequest) ([]any, string) {
	items := []any{}
	input := "Continue the conversation using the tool results above."
	messages := req.Messages
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		onlyText := last.Role == llm.RoleUser && len(last.Content) > 0
		for _, b := range last.Content {
			if b.Type != llm.BlockText {
				onlyText = false
			}
		}
		if onlyText {
			input = last.Text()
			messages = messages[:len(messages)-1]
		}
	}
	for _, msg := range messages {
		content := []any{}
		flush := func() {
			if len(content) > 0 {
				items = append(items, map[string]any{"type": "message", "role": msg.Role, "content": content})
				content = []any{}
			}
		}
		for _, b := range msg.Content {
			switch b.Type {
			case llm.BlockText:
				kind := "input_text"
				if msg.Role == llm.RoleAssistant {
					kind = "output_text"
				}
				content = append(content, map[string]any{"type": kind, "text": b.Text})
			case llm.BlockToolUse:
				flush()
				args := b.Input
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				items = append(items, map[string]any{"type": "function_call", "call_id": b.ID, "name": "counterweight_" + b.Name, "arguments": string(args)})
			case llm.BlockToolResult:
				flush()
				var text strings.Builder
				for _, c := range b.Content {
					if c.Type == llm.BlockText {
						text.WriteString(c.Text)
					}
				}
				items = append(items, map[string]any{"type": "function_call_output", "call_id": b.ToolUseID, "output": text.String()})
			}
		}
		flush()
	}
	return items, input
}
func (p *provider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		release, err := p.manager.acquire(ctx, p.id)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		defer release()
		dir, err := p.manager.directory(p.id)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		c, err := p.manager.launch(ctx, dir)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		defer c.close()
		tools := []any{}
		allowed := map[string]string{}
		for _, tool := range req.Tools {
			name := "counterweight_" + tool.Name
			allowed[name] = tool.Name
			tools = append(tools, map[string]any{"type": "function", "name": name, "description": tool.Description, "inputSchema": tool.InputSchema})
		}
		params := map[string]any{"model": p.model, "modelProvider": "openai", "allowProviderModelFallback": false, "ephemeral": true, "sandbox": "read-only", "approvalPolicy": "never", "cwd": dir, "baseInstructions": strings.Join(req.System, "\n\n"), "developerInstructions": "Use only the supplied counterweight_ tools. The host executes them and enforces its permissions. Do not use built-in filesystem, command, network, or agent tools.", "dynamicTools": tools}
		result, err := c.call(ctx, "thread/start", params)
		if err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		var thread struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if json.Unmarshal(result, &thread) != nil || thread.Thread.ID == "" {
			yield(llm.StreamEvent{}, errors.New("Codex did not create a thread"))
			return
		}
		items, input := history(req)
		if len(items) > 0 {
			if _, err = c.call(ctx, "thread/inject_items", map[string]any{"threadId": thread.Thread.ID, "items": items}); err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
		}
		turn := map[string]any{"threadId": thread.Thread.ID, "input": []any{map[string]any{"type": "text", "text": input}}, "cyberAccessProgram": p.cyber}
		if p.effort != "" {
			turn["effort"] = p.effort
		}
		if _, err = c.call(ctx, "turn/start", turn); err != nil {
			yield(llm.StreamEvent{}, err)
			return
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageStart}, nil) {
			return
		}
		usage := llm.Usage{}
		textSeen := false
		for {
			event, err := c.next(ctx)
			if err != nil {
				yield(llm.StreamEvent{}, err)
				return
			}
			if len(event.ID) > 0 {
				if event.Method != "item/tool/call" {
					c.deny(event)
					yield(llm.StreamEvent{}, fmt.Errorf("Codex requested an unsupported host action: %s", event.Method))
					return
				}
				var call struct {
					Tool      string          `json:"tool"`
					CallID    string          `json:"callId"`
					Arguments json.RawMessage `json:"arguments"`
				}
				if json.Unmarshal(event.Params, &call) != nil || allowed[call.Tool] == "" || call.CallID == "" || !json.Valid(call.Arguments) {
					c.deny(event)
					yield(llm.StreamEvent{}, errors.New("Codex requested an unknown or invalid Counterweight tool"))
					return
				}
				if !yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: call.CallID, ToolName: allowed[call.Tool]}, nil) {
					return
				}
				if !yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(call.Arguments)}, nil) {
					return
				}
				if !yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use", Usage: usage}, nil) {
					return
				}
				yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
				return
			}
			switch event.Method {
			case "error":
				var v struct {
					WillRetry bool `json:"willRetry"`
					Error     struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if json.Unmarshal(event.Params, &v) != nil || !v.WillRetry {
					message := "Codex inference failed"
					if v.Error.Message != "" {
						message += ": " + v.Error.Message
					}
					yield(llm.StreamEvent{}, errors.New(message))
					return
				}

			case "item/agentMessage/delta":
				var v struct {
					Delta string `json:"delta"`
				}
				if json.Unmarshal(event.Params, &v) == nil && v.Delta != "" {
					textSeen = true
					if !yield(llm.StreamEvent{Type: llm.SETextDelta, Text: v.Delta}, nil) {
						return
					}
				}
			case "item/reasoning/summaryTextDelta":
				var v struct {
					Delta string `json:"delta"`
				}
				if json.Unmarshal(event.Params, &v) == nil {
					if !yield(llm.StreamEvent{Type: llm.SEThinkingDelta, Text: v.Delta}, nil) {
						return
					}
				}
			case "thread/tokenUsage/updated":
				var v struct {
					TokenUsage struct {
						Last struct {
							Input  int `json:"inputTokens"`
							Output int `json:"outputTokens"`
							Cached int `json:"cachedInputTokens"`
						} `json:"last"`
					} `json:"tokenUsage"`
				}
				if json.Unmarshal(event.Params, &v) == nil {
					usage = llm.Usage{InputTokens: v.TokenUsage.Last.Input, OutputTokens: v.TokenUsage.Last.Output, CacheReadTokens: v.TokenUsage.Last.Cached}
				}
			case "item/started":
				var v struct {
					Item struct {
						Type string `json:"type"`
					} `json:"item"`
				}
				_ = json.Unmarshal(event.Params, &v)
				switch v.Item.Type {
				case "commandExecution", "fileChange", "mcpToolCall", "collabToolCall", "webSearch":
					yield(llm.StreamEvent{}, errors.New("Codex attempted a built-in action outside Counterweight tools"))
					return
				}
			case "turn/completed":
				var v struct {
					Turn struct {
						Status string `json:"status"`
						Error  *struct {
							Message string `json:"message"`
						} `json:"error"`
					} `json:"turn"`
				}
				if json.Unmarshal(event.Params, &v) != nil || v.Turn.Status != "completed" {
					msg := "Codex turn did not complete"
					if v.Turn.Error != nil {
						msg += ": " + v.Turn.Error.Message
					}
					yield(llm.StreamEvent{}, errors.New(msg))
					return
				}
				if !textSeen {
					yield(llm.StreamEvent{}, errors.New("Codex completed without a reply or tool call"))
					return
				}
				if !yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn", Usage: usage}, nil) {
					return
				}
				yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
				return
			}
		}
	}
}
