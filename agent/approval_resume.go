package agent

import (
	"strconv"
	"strings"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
)

func continuationAfterApprovalHold(lang locale.Lang, messages []llm.Message, resume intercept.ApprovalResume) (string, bool) {
	tool, input, paused := lastDeferredCall(messages)
	if !paused {
		return "", false
	}
	// An approval for a deferred tool is recorded under that tool and its own
	// parameters, not under the wrapper that carried the call.
	if tool == actool.ExecuteExtraToolName {
		if inner, params, ok := extraToolCall(input); ok {
			tool, input = inner, params
		}
	}
	if resume.Allows(tool, input) {
		return locale.Text(lang, "The operator approved the tool call and continued the task. Repeat that exact call now with the same arguments. Its previous result was only the approval wait, not the scan pause and not a target response. Do not record that wait as a fact, finding, or finished assessment."), true
	}
	return locale.Text(lang, "The operator continued the task, but the waiting tool call was not approved. Do not retry or bypass it. Do not record the wait as a target response or a finished assessment."), true
}

func approvalHoldContinuation(lang locale.Lang, messages []llm.Message, ts *db.ExplorationStore, taskID int64) (string, bool) {
	if taskID <= 0 || ts == nil {
		return "", false
	}
	if _, _, paused := lastDeferredCall(messages); !paused {
		return "", false
	}
	key := strconv.FormatInt(taskID, 10)
	grants, err := ts.ListTaskToolGrants(key)
	if err != nil {
		return "", false
	}
	replays, err := ts.ListUnconsumedApprovalReplays(key)
	if err != nil {
		return "", false
	}
	resume := intercept.ApprovalResume{Grants: grants, Replays: make([]intercept.ExactReplay, 0, len(replays))}
	for _, replay := range replays {
		resume.Replays = append(resume.Replays, intercept.ExactReplay{Tool: replay.ToolName, Input: []byte(replay.ToolInput)})
	}
	return continuationAfterApprovalHold(lang, messages, resume)
}

// lastDeferredCall returns the tool call behind the latest approval-hold result.
// A later tool result means the pause was already left behind.
func lastDeferredCall(messages []llm.Message) (tool string, input []byte, paused bool) {
	var id string
	found := false
	for i := len(messages) - 1; i >= 0 && !found; i-- {
		for j := len(messages[i].Content) - 1; j >= 0; j-- {
			block := messages[i].Content[j]
			if block.Type != llm.BlockToolResult {
				continue
			}
			if !containsDeferredMarker(block) {
				return "", nil, false
			}
			id = block.ToolUseID
			found = true
			break
		}
	}
	if !found {
		return "", nil, false
	}
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == llm.BlockToolUse && block.ID == id {
				return block.Name, block.Input, true
			}
		}
	}
	return "", nil, true
}

func containsDeferredMarker(block llm.ContentBlock) bool {
	if block.Type == llm.BlockText && strings.Contains(block.Text, intercept.ApprovalDeferredMarker) {
		return true
	}
	for _, child := range block.Content {
		if containsDeferredMarker(child) {
			return true
		}
	}
	return false
}
