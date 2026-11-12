package db

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/codesrc"
)

func changeFixture(head string) *codesrc.ChangeSet {
	return &codesrc.ChangeSet{
		BaseRef: "main", BaseCommit: strings.Repeat("b", 40), HeadRef: "pull/7", HeadCommit: head, MergeBase: strings.Repeat("c", 40),
		PullNumber: 7, PullURL: "https://github.com/acme/example/pull/7", TotalFiles: 1,
		Files: []codesrc.ChangedFile{{Path: "app/db.py", Status: "modified", Ranges: []codesrc.LineRange{{Start: 10, End: 12}}}},
		Diff:  "diff --git a/app/db.py b/app/db.py\n+q = 1\n",
	}
}

func TestChangeScopeIsStoredReturnedAndSurvivesArchiveShape(t *testing.T) {
	d := assessmentDB(t)
	head := strings.Repeat("a", 40)
	task := assessmentTask(t, d, TaskCreateOptions{AssessmentType: AssessmentCodeReview, CodeTarget: &TaskCodeTarget{
		Repository: "Acme/Example", URL: "https://github.com/Acme/Example", Ref: "pull/7", CommitSHA: head, Mode: "light",
		LocalPath: "/review/src", Change: changeFixture(head),
	}})
	if task.CodeTarget == nil || task.CodeTarget.Change == nil || task.CodeTarget.Change.PullNumber != 7 || task.CodeTarget.Change.Diff == "" {
		t.Fatalf("created task target: %+v", task.CodeTarget)
	}
	got, err := d.GetTaskCodeTarget(task.ID)
	if err != nil || got == nil || got.Change == nil {
		t.Fatalf("get: %+v %v", got, err)
	}
	c := got.Change
	if c.HeadCommit != head || c.MergeBase == "" || len(c.Files) != 1 || c.Files[0].Ranges[0].End != 12 || c.Diff != changeFixture(head).Diff {
		t.Fatalf("stored change: %+v", c)
	}

	// The list view carries the summary without the file list.
	listed, err := d.taskCodeTargets([]int64{task.ID})
	if err != nil || listed[task.ID] == nil || listed[task.ID].Change == nil || listed[task.ID].Change.PullNumber != 7 || len(listed[task.ID].Change.Files) != 0 || listed[task.ID].Change.TotalFiles != 1 {
		t.Fatalf("listed: %+v %v", listed[task.ID], err)
	}

	// The diff text and the clone path are never part of the JSON the API returns.
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "diff --git") || strings.Contains(string(raw), "/review/src") {
		t.Fatalf("api JSON leaks the diff or the clone path: %s", raw)
	}

	// Idempotency lookup: same repository (any case) and head; other heads do not match.
	ids, err := d.ChangeReviewTaskIDs("acme/example", head)
	if err != nil || len(ids) != 1 || ids[0] != task.ID {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if ids, _ := d.ChangeReviewTaskIDs("acme/example", strings.Repeat("d", 40)); len(ids) != 0 {
		t.Fatalf("other head matched: %v", ids)
	}
	if ids, _ := d.ChangeReviewTaskIDs("acme/other", head); len(ids) != 0 {
		t.Fatalf("other repository matched: %v", ids)
	}

	// Archive shape: a snapshot row restored through json_populate_recordset keeps the change,
	// and a row from before the feature (no change columns) restores as a whole-repository review.
	var snapshot string
	if err := d.QueryRow(`SELECT json_agg(t)::text FROM task_code_targets t WHERE task_id=$1`, task.ID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`DELETE FROM task_code_targets WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO task_code_targets SELECT * FROM json_populate_recordset(NULL::task_code_targets,$1::json)`, snapshot); err != nil {
		t.Fatal(err)
	}
	if again, err := d.GetTaskCodeTarget(task.ID); err != nil || again.Change == nil || again.Change.Diff == "" || again.Change.HeadCommit != head {
		t.Fatalf("after restore: %+v %v", again, err)
	}
	old := `[{"task_id":` + strconv.FormatInt(task.ID, 10) + `,"repository":"acme/example","url":"https://github.com/acme/example","ref":"","commit_sha":"","mode":"light","local_path":"","created_at":"2026-01-01T00:00:00Z"}]`
	if _, err := d.Exec(`DELETE FROM task_code_targets WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO task_code_targets SELECT * FROM json_populate_recordset(NULL::task_code_targets,$1::json)`, old); err != nil {
		t.Fatalf("a pre-feature archive row must restore: %v", err)
	}
	if again, err := d.GetTaskCodeTarget(task.ID); err != nil || again == nil || again.Change != nil {
		t.Fatalf("pre-feature row: %+v %v", again, err)
	}
}

func TestWholeRepositoryReviewHasNoChange(t *testing.T) {
	d := assessmentDB(t)
	task := assessmentTask(t, d, TaskCreateOptions{AssessmentType: AssessmentCodeReview, CodeTarget: &TaskCodeTarget{
		Repository: "acme/example", URL: "https://github.com/acme/example", Mode: "light",
	}})
	if got, err := d.GetTaskCodeTarget(task.ID); err != nil || got == nil || got.Change != nil {
		t.Fatalf("%+v %v", got, err)
	}
	if ids, _ := d.ChangeReviewTaskIDs("acme/example", ""); len(ids) != 0 {
		t.Fatalf("a whole-repository review matched a change lookup: %v", ids)
	}
}
