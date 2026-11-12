package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

func TestTaskReportPreservesNegativeAndIncompleteEvidence(t *testing.T) {
	md := Markdown(Input{
		Title: "Synthetic exercise", GeneratedAt: time.Unix(0, 0),
		Facts: []*db.Node{
			{ID: 1, State: db.StateOrigin, Payload: json.RawMessage(`{"summary":"ROOT_CONTEXT_NOT_A_TEST"}`)},
			{ID: 2, State: "confirmed", Payload: json.RawMessage(`{"summary":"No issue reproduced for expired demo token"}`)},
		},
		Intents:  []*db.Node{{ID: 3, State: "blocked", BlockedReason: "Connection timed out", Payload: json.RawMessage(`{"summary":"Session check interrupted"}`)}},
		Coverage: &db.Coverage{Denominator: 3, Tested: 1}, Truncated: true,
	})
	for _, text := range []string{"No issue reproduced for expired demo token", "Session check interrupted", "Connection timed out", "Record 3 — blocked", "**1 / 3**", "not a completed security test suite", "Record limit reached", "Unrecorded checks cannot be inferred as passed"} {
		if !strings.Contains(md, text) {
			t.Errorf("missing evidence or limit %q", text)
		}
	}
	if strings.Contains(md, "ROOT_CONTEXT_NOT_A_TEST") {
		t.Fatal("task origin mislabeled as test conclusion")
	}
}

func TestTaskReportDoesNotInventResultsWhenEmpty(t *testing.T) {
	md := Markdown(Input{})
	for _, text := range []string{"No scope rows", "No exploration conclusions", "No exploration attempts", "not proof that an asset is safe"} {
		if !strings.Contains(md, text) {
			t.Errorf("missing empty-state limit %q", text)
		}
	}
}

func TestTaskReportUsesRegisteredFindingsWithoutPromotingReviewStatus(t *testing.T) {
	md := Markdown(Input{
		Findings: []*db.Node{{ID: 99, Payload: json.RawMessage(`{"name":"STALE_GRAPH_ONLY"}`)}},
		FindingRecords: []*db.DBFinding{
			{ID: 1, Name: "Synthetic finding needing validation", Severity: "high", Status: "pending", Evidence: "SIMULATED request evidence"},
			{ID: 2, Name: "Rejected hypothesis", Severity: "low", Status: "false_positive"},
		},
	})
	for _, text := range []string{"Registered findings: **2**", "Review status: Pending review", "Review status: False positive", "SIMULATED request evidence"} {
		if !strings.Contains(md, text) {
			t.Errorf("missing registered finding metadata %q", text)
		}
	}
	if strings.Contains(md, "STALE_GRAPH_ONLY") || strings.Contains(md, "Confirmed findings: **2**") {
		t.Fatal("report confused registered records with analyst confirmation")
	}
}
