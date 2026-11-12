package server

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

// Detect and continue empty turns (thinking only, without text or tools); see steerHooks.Stop.

func assistantThinking(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.BlockThinking, Thinking: text, Signature: "sig"},
	}}
}

func TestIsThinkingOnlyTurn(t *testing.T) {
	toolUse := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.BlockThinking, Thinking: "scan ports first"},
		{Type: llm.BlockToolUse, ID: "t1", Name: "run_nuclei"},
	}}
	cases := []struct {
		name string
		msgs []llm.Message
		want bool
	}{
		{"thinking only", []llm.Message{llm.UserText("begin"), assistantThinking("let me think")}, true},
		{"thinking and tool", []llm.Message{llm.UserText("begin"), toolUse}, false},
		{"thinking and text", []llm.Message{assistantThinking("let me think"), {
			Role:    llm.RoleAssistant,
			Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "x"}, llm.TextBlock("conclusion")},
		}}, false},
		{"whitespace-only text", []llm.Message{{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "x"}, llm.TextBlock("  \n ")},
		}}, true},
		{"completely empty assistant turn", []llm.Message{{Role: llm.RoleAssistant}}, true},
		// Tool results use the user role; inspect the preceding assistant instead of misclassifying the latest message.
		{"last message is a tool result", []llm.Message{toolUse, {
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: "t1"}},
		}}, false},
		{"no assistant message", []llm.Message{llm.UserText("begin")}, false},
		{"empty history", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isThinkingOnlyTurn(c.msgs); got != c.want {
				t.Fatalf("isThinkingOnlyTurn = %v, want %v", got, c.want)
			}
		})
	}
}

// fakeHooks is a programmable inner HookRunner that verifies steerHooks respects inner decisions.
type fakeHooks struct {
	prevent  bool
	blocking []string
	msg      string
}

func (f fakeHooks) PreToolUse(context.Context, string, []byte) (bool, string, []byte) {
	return false, "", nil
}
func (f fakeHooks) PostToolUse(context.Context, string, []byte, []byte, bool) {}
func (f fakeHooks) Stop(context.Context, []llm.Message) (bool, []string, string) {
	return f.prevent, f.blocking, f.msg
}

func TestSteerHooksStopNudgesEmptyTurn(t *testing.T) {
	empty := []llm.Message{assistantThinking("I should enumerate subdomains first")}

	t.Run("empty turn receives continuation instruction", func(t *testing.T) {
		h := steerHooks{nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges, label: "worker-1 · #1"}
		prevent, blocking, _ := h.Stop(context.Background(), empty)
		if prevent {
			t.Fatal("Empty turn must not force a stop")
		}
		if len(blocking) != 1 || blocking[0] != emptyTurnNudge {
			t.Fatalf("blocking = %v, want [emptyTurnNudge]", blocking)
		}
	})

	t.Run("do not intervene when text or tools are present", func(t *testing.T) {
		h := steerHooks{nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges}
		normal := []llm.Message{{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentBlock{llm.TextBlock("scan complete, no open ports found")},
		}}
		if _, blocking, _ := h.Stop(context.Background(), normal); blocking != nil {
			t.Fatalf("Normal completion misclassified as an empty turn: %v", blocking)
		}
		if n := h.nudges.Load(); n != 0 {
			t.Fatalf("No intervention must consume no quota, got %d", n)
		}
	})

	t.Run("allow completion after reaching the limit", func(t *testing.T) {
		const limit = 5 // The user configured five empty-response retries.
		h := steerHooks{nudges: &atomic.Int64{}, limit: limit}
		for i := 1; i <= limit; i++ {
			if _, blocking, _ := h.Stop(context.Background(), empty); len(blocking) != 1 {
				t.Fatalf("Attempt %d must remain within quota, blocking = %v", i, blocking)
			}
		}
		if _, blocking, _ := h.Stop(context.Background(), empty); blocking != nil {
			t.Fatalf("Still injecting after the limit: %v", blocking)
		}
	})

	// An empty-response retry setting of -1 disables this layer; emptyTurnNudgeLimit resolves it to zero.
	t.Run("do not intervene when disabled", func(t *testing.T) {
		h := steerHooks{nudges: &atomic.Int64{}, limit: 0}
		if _, blocking, _ := h.Stop(context.Background(), empty); blocking != nil {
			t.Fatalf("Still injecting while disabled: %v", blocking)
		}
	})

	t.Run("do not override an inner hard stop", func(t *testing.T) {
		h := steerHooks{inner: fakeHooks{prevent: true, msg: "guard refused to end the turn"}, nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges}
		prevent, blocking, msg := h.Stop(context.Background(), empty)
		if !prevent || msg != "guard refused to end the turn" || blocking != nil {
			t.Fatalf("Inner hard stop was changed: prevent=%v blocking=%v msg=%q", prevent, blocking, msg)
		}
		if n := h.nudges.Load(); n != 0 {
			t.Fatalf("Deferring to inner must consume no quota, got %d", n)
		}
	})

	t.Run("do not duplicate an inner continuation", func(t *testing.T) {
		h := steerHooks{inner: fakeHooks{blocking: []string{"guard continuation reason"}}, nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges}
		_, blocking, _ := h.Stop(context.Background(), empty)
		if len(blocking) != 1 || blocking[0] != "guard continuation reason" {
			t.Fatalf("Inner continuation message was changed: %v", blocking)
		}
	})

	t.Run("preserve behavior without a counter", func(t *testing.T) {
		h := steerHooks{limit: defaultEmptyTurnNudges} // For example, a future caller might omit nudges.
		if _, blocking, _ := h.Stop(context.Background(), empty); blocking != nil {
			t.Fatalf("Must not inject without a counter: %v", blocking)
		}
	})
}
