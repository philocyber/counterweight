package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/execution"
)

func TestContainerIsolationSettingCompatibility(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{{`true`, execution.ConfinedFiles}, {`false`, "off"}, {`"confined-files"`, execution.ConfinedFiles}, {`"off"`, "off"}, {`"container"`, execution.Container}} {
		mode, err := parseIsolationSetting(json.RawMessage(tc.raw))
		if err != nil || mode == nil || *mode != tc.want {
			t.Fatalf("%s: %v %v", tc.raw, mode, err)
		}
	}
	if mode, err := parseIsolationSetting(json.RawMessage(`null`)); err != nil || mode != nil {
		t.Fatal("null changed the mode")
	}
	for _, bad := range []string{`1`, `{}`, `"invalid"`} {
		if _, err := parseIsolationSetting(json.RawMessage(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestContainerAdmissionPersistsPauseAndNeverFallsBack(t *testing.T) {
	m, e := executionFixture(t)
	if err := m.pg.SetSetting(settingIsolation, execution.Container); err != nil {
		t.Fatal(err)
	}
	e.execution = &execution.Controller{}
	task := executionTask(t, m, db.AssessmentGeneral)
	if err := e.admitExecution(task); err == nil {
		t.Fatal("unready container admitted")
	}
	if !e.IsPaused(task.ID) || !task.lifecycleSnapshot().Paused {
		t.Fatal("not paused")
	}
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	var reason string
	if err := m.pg.QueryRow(`SELECT reason FROM task_approval_holds WHERE task_id=$1 AND ended_at IS NULL`, id).Scan(&reason); err != nil || !strings.Contains(reason, "execution isolation") {
		t.Fatalf("pause reason: %q %v", reason, err)
	}
	var count int
	if err := m.pg.QueryRow(`SELECT count(*) FROM task_execution_segments WHERE task_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("fallback recorded: %d %v", count, err)
	}
}

func TestContainerExecutionRecordAndArchive(t *testing.T) {
	m, _ := executionFixture(t)
	task := executionTask(t, m, db.AssessmentGeneral)
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	if err := m.pg.AppendContainerExecutionSegment(id, execution.Container, "fixture", "sha256:immutable-fixture"); err != nil {
		t.Fatal(err)
	}
	info := taskExecutionInfo(task)
	if info.Mode != execution.Container || info.ImageDigest != "sha256:immutable-fixture" || !info.Capabilities.CredentialsIsolated || !info.Capabilities.ProcessesConfined || info.Capabilities.NetworkConfined {
		t.Fatalf("record: %+v", info)
	}
	if _, err := m.pg.Exec(`UPDATE task_execution_segments SET image_digest='changed' WHERE task_id=$1`, id); err == nil {
		t.Fatal("digest mutable")
	}
	snapshot, err := m.pg.SnapshotTaskArchive(id)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(snapshot.Tables["task_execution_segments"])
	if !strings.Contains(raw, "sha256:immutable-fixture") || !strings.Contains(raw, `"credentials_isolated":true`) {
		t.Fatalf("archive lost attestation: %s", raw)
	}
}

func TestContainerSettingsRefuseUnreadyBackend(t *testing.T) {
	m, e := executionFixture(t)
	e.execution = &execution.Controller{}
	s := &Server{m: m, engine: e}
	prior := http.NewServeMux()
	prior.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{}) })
	mux := s.registerExecutionRoutes(prior)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{"isolation_mode":"container"}`)))
	if w.Code != http.StatusConflict || e.isolationMode() != execution.ConfinedFiles {
		t.Fatalf("selection: %d %s", w.Code, w.Body.String())
	}
	if err := m.pg.SetSetting(settingIsolation, execution.Container); err != nil {
		t.Fatal(err)
	}
	if err := e.checkExecution(executionTask(t, m, db.AssessmentGeneral)); err == nil {
		t.Fatal("resume fell back to host")
	}
}
