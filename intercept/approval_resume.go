package intercept

import (
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// ApprovalDeferredMarker identifies a tool result that paused for operator
// approval. It stays in English so a resumed worker can find the paused call.
const ApprovalDeferredMarker = "[Counterweight approval hold]"

// ApprovalDeferredMessage tells the worker the call did not run and was not denied.
// Resume decides whether that exact call should be repeated.
func ApprovalDeferredMessage(lang locale.Lang) string {
	return ApprovalDeferredMarker + locale.Text(lang, " This call did not run because it is waiting for tool approval. This is not the scan pause, and the target did not respond. After the operator approves it and continues the task, repeat this exact tool call with the same arguments if it was approved. Do not change the call to get around the approval, and do not record this wait as a fact, finding, or finished assessment.")
}

// ExactReplay is one approved call still waiting for its single retry.
type ExactReplay struct {
	Tool  string
	Input []byte
}

// ApprovalResume is the operator's decision visible when a paused intent continues.
type ApprovalResume struct {
	Grants  []db.TaskToolGrant
	Replays []ExactReplay
}

// Allows reports whether Resume should repeat this paused call.
// A standing grant for every tool, for this tool, or for these arguments allows it.
// An unused exact approval allows it. Anything else stays unapproved.
func (a ApprovalResume) Allows(tool string, input []byte) bool {
	if GrantAllows(a.Grants, []string{tool}, input) {
		return true
	}
	for _, replay := range a.Replays {
		if tool != "" && replay.Tool != tool {
			continue
		}
		if len(input) == 0 || SameArguments(replay.Input, input) {
			return true
		}
	}
	return false
}
