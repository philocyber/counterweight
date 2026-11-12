package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
)

func TestContinuationRepeatsApprovedHoldOnly(t *testing.T) {
	input := json.RawMessage(`{"url":"https://example.test"}`)
	messages := []llm.Message{
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{
			Type: llm.BlockToolUse, ID: "call-1", Name: "WebFetch", Input: input,
		}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{
			Type: llm.BlockToolResult, ToolUseID: "call-1", IsError: true,
			Content: []llm.ContentBlock{{Type: llm.BlockText, Text: intercept.ApprovalDeferredMessage(locale.En)}},
		}}},
	}
	approved, ok := continuationAfterApprovalHold(locale.En, messages, intercept.ApprovalResume{
		Grants: []db.TaskToolGrant{{Scope: "all"}},
	})
	if !ok || !strings.Contains(approved, "Repeat that exact call") {
		t.Fatalf("approved resume: ok=%v text=%q", ok, approved)
	}
	denied, ok := continuationAfterApprovalHold(locale.En, messages, intercept.ApprovalResume{})
	if !ok || !strings.Contains(denied, "was not approved") || strings.Contains(denied, "Repeat that exact call") {
		t.Fatalf("unapproved resume: ok=%v text=%q", ok, denied)
	}
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{
		Type: llm.BlockToolResult, ToolUseID: "call-2",
		Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "fetched"}},
	}}})
	if _, ok := continuationAfterApprovalHold(locale.En, messages, intercept.ApprovalResume{Grants: []db.TaskToolGrant{{Scope: "all"}}}); ok {
		t.Fatal("a later tool result was treated as the approval pause")
	}
}
