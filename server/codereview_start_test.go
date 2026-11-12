package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// A review started from plain language must be the same kind of task as one
// started by /code-review: the recorded target (which turns on verification and
// mitigation) plus the operator's extra instructions in the goal.
func TestStartCodeReviewTaskRecordsTargetAndInstructions(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("isolated PostgreSQL unavailable: %v", err)
	}
	defer m.Close()
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	repo, _ := codesrc.ParseRepo("acme/fixture")
	task, err := s.startCodeReviewTask(repo, codesrc.ModeLight, "/work/src", "abc123", "  stop after three confirmed vulnerabilities ", locale.En)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	defer m.pg.DeleteTask(id)
	defer m.pg.Exec(`DELETE FROM settings WHERE key=$1`, db.TaskCodeReviewKey(id))

	if !strings.HasSuffix(task.Goal, "Additional operator instructions: stop after three confirmed vulnerabilities") ||
		!strings.Contains(task.Goal, "/work/src") {
		t.Fatalf("goal=%q", task.Goal)
	}
	target, err := m.pg.GetTaskCodeTarget(id)
	if err != nil || target == nil || target.Repository != "acme/fixture" || target.Mode != "light" || target.CommitSHA != "abc123" || target.LocalPath != "/work/src" || taskAssessmentType(task) != db.AssessmentCodeReview {
		t.Fatalf("target=%+v err=%v", target, err)
	}
}

// Bad input is refused before anything is cloned or created.
func TestStartCodeReviewToolRejectsBadInput(t *testing.T) {
	s := &Server{}
	for _, in := range []map[string]string{
		{"repository": "https://gitlab.com/a/b"},
		{"repository": "a/b", "mode": "super"},
	} {
		raw, _ := json.Marshal(in)
		res, err := s.toolStartCodeReview(locale.En).Call(context.Background(), raw, nil)
		if err != nil || !res.IsError {
			t.Fatalf("%v: err=%v result=%s", in, err, res.Flatten())
		}
	}
}
