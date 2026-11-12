package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/execution"
	"github.com/Philocyber/counterweight/report"
)

func executionFixture(t *testing.T) (*Manager, *Engine) {
	t.Helper()
	priorRoots, priorFailure, priorLookup := agent.FileRoots, agent.FileAccessFailed, report.ExecutionLookup
	priorSkills := agent.SkillReadRoot
	agent.SkillReadRoot = ""
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	old, exists, err := m.pg.GetSetting(settingIsolation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if exists {
			_ = m.pg.SetSetting(settingIsolation, old)
		} else {
			_, _ = m.pg.Exec(`DELETE FROM settings WHERE key='isolation'`)
		}
		_ = m.Close()
		agent.FileRoots = priorRoots
		agent.FileAccessFailed = priorFailure
		agent.SkillReadRoot = priorSkills
		report.ExecutionLookup = priorLookup
	})
	if err = m.pg.SetSetting(settingIsolation, "true"); err != nil {
		t.Fatal(err)
	}
	return m, NewEngine(m)
}
func executionTask(t *testing.T, m *Manager, typ string) *Task {
	t.Helper()
	opts := db.TaskCreateOptions{AssessmentType: typ}
	if typ == db.AssessmentCodeReview {
		clone := filepath.Join(m.dir, "clone-"+strconv.Itoa(len(m.tasks)))
		if err := os.Mkdir(clone, 0755); err != nil {
			t.Fatal(err)
		}
		opts.CodeTarget = &db.TaskCodeTarget{Repository: "example/fixture", Mode: "light", LocalPath: clone}
	}
	task, err := m.CreateTaskWithOptions("execution fixture", "Read only synthetic fixtures", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { id, _ := strconv.ParseInt(task.ID, 10, 64); _ = m.pg.DeleteTask(id) })
	return task
}
func TestExecutionSegmentsPinnedResumeMixedAndAPI(t *testing.T) {
	m, e := executionFixture(t)
	task := executionTask(t, m, db.AssessmentGeneral)
	if !taskDTO(task, "created").Execution.Assumed {
		t.Fatal("legacy host must be assumed")
	}
	if err := e.admitExecution(task); err != nil {
		t.Fatal(err)
	}
	if err := m.pg.SetSetting(settingIsolation, "false"); err != nil {
		t.Fatal(err)
	}
	if err := e.admitExecution(task); err != nil {
		t.Fatal(err)
	}
	first := taskDTO(task, "running").Execution
	if first.Mode != execution.ConfinedFiles || first.Mixed || first.Assumed {
		t.Fatalf("running mode changed: %+v", first)
	}
	e.Pause(task.ID, agent.AbortPausedByUser)
	if err := m.ApplyTaskPause(task.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.SetTaskPaused(task.ID, false); err != nil {
		t.Fatal(err)
	}
	e.Resume(task)
	dto := taskDTO(task, "running")
	if dto.Execution.Mode != execution.Host || !dto.Execution.Mixed || dto.Execution.Assumed {
		t.Fatalf("resume: %+v", dto.Execution)
	}
	raw, _ := json.Marshal(dto)
	if !strings.Contains(string(raw), `"execution":{"mode":"host"`) {
		t.Fatal("missing task execution object")
	}
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	var count int
	if err := m.pg.QueryRow(`SELECT count(*) FROM task_execution_segments WHERE task_id=$1`, id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("segments: %d, %v", count, err)
	}
	if _, err := m.pg.Exec(`UPDATE task_execution_segments SET mode='host' WHERE task_id=$1`, id); err == nil {
		t.Fatal("segment was mutable")
	}
	snapshot, err := m.pg.SnapshotTaskArchive(id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snapshot.Tables["task_execution_segments"]), "confined-files") {
		t.Fatal("archive omitted execution segments")
	}
}
func TestExecutionFailClosedStartAndRoots(t *testing.T) {
	m, e := executionFixture(t)
	task := executionTask(t, m, db.AssessmentGeneral)
	e.execution = &execution.Controller{} // A probe that has not passed cannot admit work.
	e.Run(context.Background(), task)
	if !e.IsPaused(task.ID) || e.Started(task.ID) || !task.lifecycleSnapshot().Paused {
		t.Fatal("failed self-test started task")
	}
	dto := taskDTO(task, "running")
	if dto.ApprovalState.Active() == nil || dto.ApprovalState.Active().Reason == "" || dto.Status != "paused" {
		t.Fatalf("pause reason missing: %+v", dto)
	}
	if !dto.Execution.Assumed {
		t.Fatal("failed admission recorded an execution segment")
	}
	e.execution = execution.NewController()
	review := executionTask(t, m, db.AssessmentCodeReview)
	id, _ := strconv.ParseInt(review.ID, 10, 64)
	if _, err := m.pg.Exec(`UPDATE task_code_targets SET local_path=$2 WHERE task_id=$1`, id, filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal(err)
	}
	if err := e.admitExecution(review); err == nil || !e.IsPaused(review.ID) {
		t.Fatal("missing roots did not fail closed")
	}
}
func TestExecutionRootsAllAssessmentRoles(t *testing.T) {
	m, e := executionFixture(t)
	for _, typ := range []string{db.AssessmentGeneral, db.AssessmentWebApp, db.AssessmentNetwork, db.AssessmentCodeReview} {
		task := executionTask(t, m, typ)
		taskDir := filepath.Join(m.dir, "tasks", task.ID)
		for _, key := range []string{"planner", "worker", "mainagent", db.CodeReviewPlannerAgentKey, db.CodeReviewWorkerAgentKey, db.CodeVerifierAgentKey, db.FindingRetestAgentKey} {
			roots, err := e.taskRoots(task, key, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(roots.Read, taskDir) {
				t.Fatalf("%s/%s lost task uploads", typ, key)
			}
			if typ == db.AssessmentCodeReview {
				id, _ := strconv.ParseInt(task.ID, 10, 64)
				target, _ := m.pg.GetTaskCodeTarget(id)
				if !slices.Contains(roots.Read, target.LocalPath) || len(roots.Write) != 0 {
					t.Fatalf("review roots: %+v", roots)
				}
			} else if isFindingRunAgent(key) && !findingFilesWritable(key) {
				if len(roots.Write) != 0 {
					t.Fatalf("read-only finding roots: %+v", roots)
				}
			} else if !slices.Equal(roots.Write, []string{taskDir}) {
				t.Fatalf("black-box roots: %+v", roots)
			}
		}
	}
	// Create persisted fix/reviewer identities without git commands or a provider.
	task := executionTask(t, m, db.AssessmentCodeReview)
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	target, _ := m.pg.GetTaskCodeTarget(id)
	fid, err := m.pg.AddFinding(id, 0, "fixture", "fixture", "high", "fixture", "fixture", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.pg.SetFindingStatus(fid, db.FindingConfirmed); err != nil {
		t.Fatal(err)
	}
	fix, conv, _, err := m.pg.CreateFindingMitigation(t.Context(), fid, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.pg.StartFindingRetest(t.Context(), fix.ID); err != nil {
		t.Fatal(err)
	}
	wt := fixWorktreePath(target.LocalPath, fix.ID)
	if err = os.Mkdir(wt, 0755); err != nil {
		t.Fatal(err)
	}
	roots, err := e.taskRoots(task, db.CodeFixerAgentKey, conv.ID)
	if err != nil || !slices.Equal(roots.Write, []string{wt}) || !slices.Contains(roots.Read, target.LocalPath) {
		t.Fatalf("fixer roots: %+v %v", roots, err)
	}
	if _, err = m.pg.Exec(`UPDATE finding_retests SET status='completed',verdict='patched',patch_diff='fixture' WHERE id=$1`, fix.ID); err != nil {
		t.Fatal(err)
	}
	_, reviewConv, _, err := m.pg.CreatePatchReview(t.Context(), fix.ID)
	if err != nil {
		t.Fatal(err)
	}
	roots, err = e.taskRoots(task, db.PatchReviewerAgentKey, reviewConv.ID)
	if err != nil || len(roots.Write) != 0 || !slices.Contains(roots.Read, wt) {
		t.Fatalf("reviewer roots: %+v %v", roots, err)
	}
}
func TestExecutionStatusRecheckAndSettings(t *testing.T) {
	m, e := executionFixture(t)
	s := &Server{m: m, engine: e}
	previous := http.NewServeMux()
	previous.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"workers": 3}) })
	mux := s.registerExecutionRoutes(previous)
	request := func(method, path, body string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	if got := request("GET", "/api/settings", ""); got["isolation"] != true || got["workers"] != float64(3) {
		t.Fatal(got)
	}
	ready := request("GET", "/api/execution/status", "")
	if ready["status"] != "ready" || ready["mode"] != "confined-files" || ready["last_self_test"] == "" {
		t.Fatal(ready)
	}
	request("PUT", "/api/settings", `{"isolation":false}`)
	if got := request("POST", "/api/execution/recheck", ""); got["status"] != "off" || got["mode"] != "host" {
		t.Fatal(got)
	}
	request("PUT", "/api/settings", `{"isolation":true}`)
	if got := request("POST", "/api/execution/recheck", ""); got["status"] != "ready" {
		t.Fatal(got)
	}
}
func TestExecutionDraftUploadRoots(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "drafts", "draft-fixture", "uploads")
	roots := draftUploadRoots(dir, "Inspect "+filepath.Join(p, "evidence.txt"))
	if !slices.Equal(roots, []string{p}) {
		t.Fatalf("draft upload not readable: %v", roots)
	}
	if roots = draftUploadRoots(dir, "Inspect unrelated file.txt"); len(roots) != 0 {
		t.Fatal(roots)
	}
}
