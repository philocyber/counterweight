package agent

import (
	"testing"

	"github.com/Philocyber/counterweight/sidequestion"
)

// The side_question_sessions table accepts a parent that is either a conversation
// or a task with its exploration, never both.
func TestSideParentForIsConversationOrTask(t *testing.T) {
	cases := []struct {
		name string
		ri   RunInfo
		want sidequestion.Parent
	}{
		{"chat conversation", RunInfo{SessionID: "conv-7"}, sidequestion.Parent{ConversationID: 7}},
		{"conversation running with a task identity", RunInfo{SessionID: "conv-7", TaskID: 2, ExplorationID: 9, IntentID: 4, AssessmentType: "code_review"}, sidequestion.Parent{ConversationID: 7}},
		{"task main agent", RunInfo{TaskID: 2, ExplorationID: 9}, sidequestion.Parent{TaskID: 2, ExplorationID: 9}},
		{"task worker", RunInfo{TaskID: 2, ExplorationID: 9, IntentID: 4}, sidequestion.Parent{TaskID: 2, ExplorationID: 9, IntentID: 4}},
	}
	for _, c := range cases {
		got := sideParentFor(c.ri)
		if got != c.want {
			t.Errorf("%s: parent = %+v, want %+v", c.name, got, c.want)
		}
		if got.ConversationID > 0 && (got.TaskID != 0 || got.ExplorationID != 0 || got.IntentID != 0) {
			t.Errorf("%s: a conversation parent must not carry task fields: %+v", c.name, got)
		}
	}
}
