package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
)

func TestCrossTaskToolsStayWithinTheRunBinding(t *testing.T) {
	s, initial, _ := trafficEvidenceServer(t)
	pg := s.m.pg
	own, _ := pg.GetFinding(initial.FindingID)
	ownID := *own.TaskID

	other, err := s.m.CreateTask("other task", "other", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	otherID, _ := strconv.ParseInt(other.ID, 10, 64)
	t.Cleanup(func() { pg.Exec(`DELETE FROM task_archives WHERE task_id=$1`, otherID); pg.DeleteTask(otherID) })
	foreign, err := s.evidenceStore().Record(context.Background(), db.RecordFindingInput{TaskID: otherID, ExplorationID: other.ExpID, Worker: "test", VulnClass: "TEST", Summary: "foreign", Name: "Foreign", Severity: "low"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	conv, err := pg.CreateConversation("reporter", "binding fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Exec(`DELETE FROM conversations WHERE id=$1`, conv.ID) })
	if err := pg.BindConversationTasks(conv.ID, []int64{ownID}); err != nil {
		t.Fatal(err)
	}
	bound := intercept.WithConvID(context.Background(), conv.ID)
	byRunInfo := agent.WithRunInfo(context.Background(), agent.RunInfo{TaskID: ownID})
	unbound := context.Background()

	reportOf := func(id int64) string {
		f, _ := pg.GetFinding(id)
		return f.Report
	}
	for name, ctx := range map[string]context.Context{"conversation binding": bound, "run identity": byRunInfo} {
		// Own task: allowed, also when the id is spelled with leading zeros.
		workflowCall(t, ctx, s.toolListTaskFindings(), map[string]any{"task_id": fmt.Sprint(ownID)}, false)
		workflowCall(t, ctx, s.toolGetTaskGraph(), map[string]any{"task_id": "0" + fmt.Sprint(ownID)}, false)
		workflowCall(t, ctx, s.toolUpdateFindingReport(), map[string]any{"finding_id": initial.NodeID, "report": "own report"}, false)

		// Another task: every cross-task tool refuses.
		foreignTask := map[string]any{"task_id": other.ID}
		workflowCall(t, ctx, s.toolListTaskFindings(), foreignTask, true)
		workflowCall(t, ctx, s.toolGetTaskGraph(), foreignTask, true)
		workflowCall(t, ctx, s.toolListWorkerTraces(), foreignTask, true)
		workflowCall(t, ctx, s.toolGetTaskNodeDetail(), map[string]any{"task_id": other.ID, "id": foreign.NodeID}, true)
		workflowCall(t, ctx, s.toolAddHint(), map[string]any{"task_id": other.ID, "text": "do something"}, true)
		workflowCall(t, ctx, s.toolPauseTask(), foreignTask, true)
		workflowCall(t, ctx, s.toolSpawnTask(), map[string]any{"description": "child", "goal": "reuse", "share_context": true, "source_task_ids": []string{other.ID}}, true)
		workflowCall(t, ctx, s.toolSpawnTask(), map[string]any{"description": "child", "goal": "attach", "parent_ref": other.ID}, true)
		if listed, err := s.toolListTasks().Call(ctx, json.RawMessage(`{}`), nil); err != nil || listed.IsError || strings.Contains(listed.Flatten(), "other task") {
			t.Fatalf("%s lists another task: %v %s", name, err, listed.Flatten())
		}
		workflowCall(t, ctx, s.toolListTaskFindings(), map[string]any{"task_id": " 0" + other.ID}, true)
		workflowCall(t, ctx, s.toolUpdateFindingReport(), map[string]any{"finding_id": foreign.NodeID, "report": "overwritten by " + name}, true)
		if got := reportOf(foreign.FindingID); got != "" {
			t.Fatalf("%s rewrote another task's report: %q", name, got)
		}
	}
	if reportOf(initial.FindingID) != "own report" {
		t.Fatal("bound task's own report was not written")
	}

	// A run with no binding, such as the operator's assistant, keeps its reach.
	workflowCall(t, unbound, s.toolListTaskFindings(), map[string]any{"task_id": other.ID}, false)
	if listed, err := s.toolListTasks().Call(unbound, json.RawMessage(`{}`), nil); err != nil || !strings.Contains(listed.Flatten(), "other task") {
		t.Fatalf("unbound run no longer lists every task: %v", err)
	}
	workflowCall(t, unbound, s.toolUpdateFindingReport(), map[string]any{"finding_id": foreign.NodeID, "report": "operator edit"}, false)
	if reportOf(foreign.FindingID) != "operator edit" {
		t.Fatal("unbound run lost its reach")
	}
}
