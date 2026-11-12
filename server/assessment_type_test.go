package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
)

func TestAssessmentAPIAndCloneAtomicity(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	original := cloneRepository
	defer func() { cloneRepository = original }()
	clones := 0
	cloneRepository = func(ctx context.Context, opts codesrc.CloneOptions) (string, error) { clones++; return opts.Dest, nil }
	for _, kind := range []string{"", "general", "web_app", "network", "code_review"} {
		body := map[string]any{"assessment_type": kind, "description": "Assessment fixture", "goal": "Review the authorized fixture"}
		if kind == "code_review" {
			body["repository"] = "example/fixture#release"
			body["instructions"] = "Focus on authorization"
		}
		raw, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		s.createTask(rec, httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewReader(raw)))
		if rec.Code != 201 {
			t.Fatalf("%s: %d %s", kind, rec.Code, rec.Body.String())
		}
		var dto TaskDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
			t.Fatal(err)
		}
		normalized, _ := db.NormalizeAssessmentType(kind)
		if dto.AssessmentType != normalized {
			t.Fatalf("type: %+v", dto)
		}
		id, _ := strconv.ParseInt(dto.ID, 10, 64)
		defer m.pg.DeleteTask(id)
		if kind == "code_review" {
			if dto.CoverageEnabled || dto.CodeTarget == nil || dto.CodeTarget.Repository != "example/fixture" || dto.CodeTarget.Ref != "release" || dto.CodeTarget.Mode != "balanced" || dto.CodeTarget.TaskID != id {
				t.Fatalf("review dto: %+v", dto)
			}
			if strings.Contains(rec.Body.String(), "local_path") {
				t.Fatal("clone path exposed in target")
			}
		}
	}
	if clones != 1 {
		t.Fatalf("clones=%d", clones)
	}
	before, _ := m.pg.ListTasks()
	cloneRepository = func(context.Context, codesrc.CloneOptions) (string, error) {
		return "", errors.New("fixture clone failed")
	}
	rec := httptest.NewRecorder()
	s.createTask(rec, httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(`{"assessment_type":"code_review","repository":"example/fixture"}`)))
	if rec.Code != 422 {
		t.Fatalf("failed clone: %d %s", rec.Code, rec.Body.String())
	}
	after, _ := m.pg.ListTasks()
	if len(before) != len(after) {
		t.Fatal("failed clone left a task")
	}
	for _, raw := range []string{`{"assessment_type":"guess"}`, `{"assessment_type":"code_review"}`, `{"assessment_type":"code_review","repository":"https://other.example/a/b"}`, `{"assessment_type":"code_review","repository":"example/fixture","mode":"invalid"}`, `{"assessment_type":"web_app","repository":"example/fixture"}`} {
		rec := httptest.NewRecorder()
		s.createTask(rec, httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(raw)))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", raw, rec.Code, rec.Body.String())
		}
	}
}

func TestAssessmentReportsRespectExplicitTypes(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	for _, kind := range []string{db.AssessmentGeneral, db.AssessmentWebApp, db.AssessmentNetwork, db.AssessmentCodeReview} {
		opts := db.TaskCreateOptions{AssessmentType: kind}
		if kind == db.AssessmentCodeReview {
			opts.CodeTarget = &db.TaskCodeTarget{Repository: "example/fixture", URL: "https://github.com/example/fixture", CommitSHA: "abc", Mode: "light"}
		}
		task, err := m.CreateTaskWithOptions("Code review example/fixture", "whitebox review example/fixture", opts)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := strconv.ParseInt(task.ID, 10, 64)
		defer m.pg.DeleteTask(id)
		cr := s.taskCodeReview(task)
		if kind == db.AssessmentCodeReview {
			if cr == nil || cr.Repository != "example/fixture" || cr.Inferred {
				t.Fatalf("review metadata: %+v", cr)
			}
		} else if cr != nil {
			t.Fatalf("explicit %s inferred from text", kind)
		}
	}
	legacy := &Task{ID: "0", Description: "Whitebox code review example/fixture"}
	if cr := s.taskCodeReview(legacy); cr == nil || !cr.Inferred {
		t.Fatalf("legacy fallback: %+v", cr)
	}
}

func TestAssessmentTypeHTTPRules(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	task, err := m.CreateTask("Fixture", "Review the authorized fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	defer m.pg.DeleteTask(id)
	request := func(raw string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+task.ID+"/assessment-type", strings.NewReader(raw))
		req.SetPathValue("id", task.ID)
		rec := httptest.NewRecorder()
		s.changeAssessmentType(rec, req)
		return rec
	}
	if rec := request(`{"assessment_type":"network"}`); rec.Code != 200 {
		t.Fatalf("prestart: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := m.pg.StampFirstRun(id, 0); err != nil {
		t.Fatal(err)
	}
	if rec := request(`{"assessment_type":"web_app"}`); rec.Code != 409 {
		t.Fatalf("started: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := m.pg.Exec(`UPDATE tasks SET assessment_type='general',assessment_type_explicit=false,status='done' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	task.updateLifecycle(func(state *taskLifecycleState) {
		state.Status = "done"
		state.AssessmentType = "general"
		state.AssessmentTypeExplicit = false
	})
	if rec := request(`{"assessment_type":"code_review","reclassify":true,"repository":"example/fixture"}`); rec.Code != 200 {
		t.Fatalf("historical: %d %s", rec.Code, rec.Body.String())
	}
}
