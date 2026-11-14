package server

import (
	"errors"
	"strings"
	"testing"
)

// A failed planning round leaves the task running and waiting for its next
// round; the task reports why until a round succeeds or the task ends.
func TestPlanningErrorIsReportedWhileTheTaskIsActive(t *testing.T) {
	task := &Task{}
	if got := activePlanningError(task, "running"); got != "" {
		t.Fatalf("new task reports %q", got)
	}
	task.setPlanningError(errors.New("  400 MissingSessionID: x-opencode-session header is required  "))
	if got := activePlanningError(task, "running"); got != "400 MissingSessionID: x-opencode-session header is required" {
		t.Fatalf("running task reports %q", got)
	}
	for _, status := range []string{"done", "failed", "timeout"} {
		if got := activePlanningError(task, status); got != "" {
			t.Fatalf("%s task still reports %q", status, got)
		}
	}
	task.setPlanningError(nil)
	if got := activePlanningError(task, "running"); got != "" {
		t.Fatalf("a successful round left %q", got)
	}

	task.setPlanningError(errors.New(strings.Repeat("é", planningErrorMaxLen+50)))
	if got := []rune(activePlanningError(task, "running")); len(got) != planningErrorMaxLen+1 || got[len(got)-1] != '…' {
		t.Fatalf("long error kept %d runes", len(got))
	}
}
