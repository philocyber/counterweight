package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func assessmentDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func assessmentTask(t *testing.T, d *DB, opts TaskCreateOptions) *Task {
	t.Helper()
	task, err := d.CreateTaskWithOptions("Assessment fixture", "Review the authorized fixture", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	return task
}

func TestAssessmentSchemaAndBackfill(t *testing.T) {
	d := assessmentDB(t)
	for range 2 {
		if _, err := d.Exec(schemaSQL); err != nil {
			t.Fatal(err)
		}
	}
	task := assessmentTask(t, d, TaskCreateOptions{})
	key := TaskCodeReviewKey(task.ID)
	if err := d.SetSetting(key, `{"repository":"example/fixture","ref":"release","commit":"abc123","mode":"light"}`); err != nil {
		t.Fatal(err)
	}
	before, err := d.GetTask(task.ID)
	if err != nil || before.CodeTarget == nil || before.AssessmentType != AssessmentCodeReview || before.CoverageEnabled {
		t.Fatalf("legacy read: %+v, %v", before, err)
	}
	for range 2 {
		if err := d.migrateLegacyCodeReviewTargets(); err != nil {
			t.Fatal(err)
		}
	}
	if _, exists, err := d.GetSetting(key); err != nil || exists {
		t.Fatalf("legacy key remains: %v, %v", exists, err)
	}
	after, err := d.GetTask(task.ID)
	if err != nil || after.CodeTarget == nil || after.CodeTarget.CommitSHA != "abc123" || after.AssessmentType != AssessmentCodeReview || after.CoverageEnabled {
		t.Fatalf("migrated: %+v, %v", after, err)
	}
	list, err := d.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item.ID == task.ID && (item.CodeTarget == nil || item.AssessmentType != AssessmentCodeReview) {
			t.Fatalf("list target missing: %+v", item)
		}
	}
}

func TestAssessmentCreationAndTypeChanges(t *testing.T) {
	d := assessmentDB(t)
	for _, kind := range []string{"", AssessmentGeneral, AssessmentWebApp, AssessmentNetwork, AssessmentCodeReview} {
		t.Run(kind, func(t *testing.T) {
			opts := TaskCreateOptions{AssessmentType: kind}
			if kind == AssessmentCodeReview {
				opts.CodeTarget = &TaskCodeTarget{Repository: "example/fixture", URL: "https://github.com/example/fixture", Mode: "balanced", LocalPath: "/review/src"}
			}
			task := assessmentTask(t, d, opts)
			got, err := d.GetTask(task.ID)
			want, _ := NormalizeAssessmentType(kind)
			if err != nil || got.AssessmentType != want || !got.AssessmentTypeExplicit {
				t.Fatalf("task: %+v, %v", got, err)
			}
			if kind == AssessmentCodeReview && (got.CoverageEnabled || got.CodeTarget == nil) {
				t.Fatalf("review: %+v", got)
			}
		})
	}
	task := assessmentTask(t, d, TaskCreateOptions{})
	if _, err := d.ChangeTaskAssessmentType(task.ID, AssessmentTypeChange{Type: AssessmentNetwork}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.StampFirstRun(task.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ChangeTaskAssessmentType(task.ID, AssessmentTypeChange{Type: AssessmentWebApp}); !errors.Is(err, ErrTaskAlreadyStarted) {
		t.Fatalf("started change: %v", err)
	}
	if _, err := d.ChangeTaskAssessmentType(task.ID, AssessmentTypeChange{Type: AssessmentCodeReview, Reclassify: true}); !errors.Is(err, ErrReclassifyNotGeneral) {
		t.Fatalf("non-general reclassify: %v", err)
	}
	if _, err := d.Exec(`UPDATE tasks SET assessment_type='general',assessment_type_explicit=false,status='done' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	got, err := d.ChangeTaskAssessmentType(task.ID, AssessmentTypeChange{Type: AssessmentCodeReview, Reclassify: true, Target: &TaskCodeTarget{Repository: "example/fixture", URL: "https://github.com/example/fixture"}})
	if err != nil || got.AssessmentType != AssessmentCodeReview || got.CoverageEnabled || got.CodeTarget == nil {
		t.Fatalf("reclassify: %+v, %v", got, err)
	}
	if _, err := d.CreateTaskWithOptions("invalid", "goal", TaskCreateOptions{AssessmentType: "automatic"}); !errors.Is(err, ErrAssessmentTypeInvalid) {
		t.Fatalf("invalid type: %v", err)
	}
	if _, err := d.CreateTaskWithOptions("invalid", "goal", TaskCreateOptions{AssessmentType: AssessmentCodeReview}); !errors.Is(err, ErrCodeTargetRequired) {
		t.Fatalf("missing repository: %v", err)
	}
}

func TestAssessmentArchiveRoundTrip(t *testing.T) {
	d := assessmentDB(t)
	// The archive snapshot reads tables that normal server startup creates.
	if err := d.EnsureLLMRecordsTable(); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureLLMUsageTable(); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			task := assessmentTask(t, d, TaskCreateOptions{AssessmentType: AssessmentCodeReview, CodeTarget: &TaskCodeTarget{Repository: "example/fixture", URL: "https://github.com/example/fixture", Ref: "release", CommitSHA: "abc", Mode: "light", LocalPath: "/review/src"}})
			if err := d.SetPaused(task.ID, true); err != nil {
				t.Fatal(err)
			}
			archive, err := d.QueueTaskArchive(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			job, err := d.ClaimTaskArchiveJob(t.Context())
			if err != nil || job == nil || job.ID != archive.ID {
				t.Fatalf("claim: %+v, %v", job, err)
			}
			snapshot, err := d.SnapshotTaskArchive(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				var rows []map[string]any
				if err := json.Unmarshal(snapshot.Tables["tasks"], &rows); err != nil {
					t.Fatal(err)
				}
				delete(rows[0], "assessment_type")
				delete(rows[0], "assessment_type_explicit")
				snapshot.Tables["tasks"], _ = json.Marshal(rows)
				delete(snapshot.Tables, "task_code_targets")
			}
			if err := d.CompleteTaskArchive(archive.ID, snapshot, "/archive/fixture.tar.zst", "abc", 100, 50); err != nil {
				t.Fatal(err)
			}
			if _, err := d.QueueTaskArchiveRestore(archive.ID); err != nil {
				t.Fatal(err)
			}
			job, err = d.ClaimTaskArchiveJob(t.Context())
			if err != nil || job == nil || job.State != Restoring {
				t.Fatalf("restore claim: %+v, %v", job, err)
			}
			if _, err := d.RestoreTaskArchive(archive.ID, snapshot, 0); err != nil {
				t.Fatal(err)
			}
			got, err := d.GetTask(task.ID)
			if err != nil || got == nil {
				t.Fatalf("restored: %+v, %v", got, err)
			}
			if legacy {
				if got.AssessmentType != AssessmentGeneral || got.CodeTarget != nil {
					t.Fatalf("legacy restore: %+v", got)
				}
			} else if got.AssessmentType != AssessmentCodeReview || got.CodeTarget == nil || got.CodeTarget.CommitSHA != "abc" || got.CodeTarget.LocalPath != "/review/src" || got.CoverageEnabled {
				t.Fatalf("review restore: %+v", got)
			}
			if err := d.CompleteTaskArchiveRestore(archive.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAssessmentTemplateRoundTrip(t *testing.T) {
	d := assessmentDB(t)
	template, err := d.CreateTaskTemplate(TaskTemplateInput{Name: fmt.Sprintf("Assessment fixture %d", time.Now().UnixNano()), Description: "Fixture", Goal: "Focus on authorization", AssessmentType: AssessmentCodeReview, Repository: "example/fixture#release", Mode: "light"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTaskTemplate(template.ID)
	got, err := d.GetTaskTemplate(template.ID)
	if err != nil || got.AssessmentType != AssessmentCodeReview || got.Repository != "example/fixture#release" || got.Mode != "light" {
		t.Fatalf("template: %+v %v", got, err)
	}
	kind, repository, mode := AssessmentNetwork, "", ""
	got, err = d.PatchTaskTemplate(template.ID, TaskTemplatePatch{AssessmentType: &kind, Repository: &repository, Mode: &mode})
	if err != nil || got.AssessmentType != AssessmentNetwork || got.Repository != "" || got.Mode != "" {
		t.Fatalf("template patch: %+v %v", got, err)
	}
}
