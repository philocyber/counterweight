package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
)

func TestReportFindingNormalisesAndRejectsSeverity(t *testing.T) {
	s, initial, _ := trafficEvidenceServer(t)
	pg := s.m.pg
	ctx := context.Background()
	f, _ := pg.GetFinding(initial.FindingID)
	task := s.m.ResolveTask(fmt.Sprint(*f.TaskID))
	ts := agent.NewToolSet(task.Store, "planner")
	ts.SetTaskID(*f.TaskID)
	ts.SetFindingRecorder(s.evidenceStore())
	tools, _, cleanup := agent.AugmentTools(ctx, "planner", ts.PlannerTools())
	defer cleanup()
	report := workflowTool(t, tools, "report_finding")

	result := workflowCall(t, ctx, report, map[string]any{"vulnclass": "TEST", "severity": "  HIGH ", "summary": "case and spacing"}, false)
	var recorded db.RecordedFinding
	if err := json.Unmarshal([]byte(strings.SplitN(result, "\n", 2)[1]), &recorded); err != nil {
		t.Fatal(err)
	}
	stored, err := pg.GetFinding(recorded.FindingID)
	if err != nil || stored.Severity != "high" {
		t.Fatalf("stored severity = %q, %v", stored.Severity, err)
	}
	var payload string
	if err := pg.QueryRow(`SELECT payload->>'severity' FROM exploration_nodes WHERE id=$1`, recorded.NodeID).Scan(&payload); err != nil || payload != "high" {
		t.Fatalf("node payload severity = %q, %v", payload, err)
	}

	var before int
	pg.QueryRow(`SELECT count(*) FROM findings`).Scan(&before)
	for _, bad := range []string{"urgent", "high\n| x |", "<b>high</b>", ""} {
		workflowCall(t, ctx, report, map[string]any{"vulnclass": "TEST", "severity": bad, "summary": "rejected"}, true)
	}
	var after int
	pg.QueryRow(`SELECT count(*) FROM findings`).Scan(&after)
	if after != before {
		t.Fatalf("rejected calls stored %d findings", after-before)
	}
}
