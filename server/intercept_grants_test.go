package server

import (
	"fmt"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInterceptParameterGrantHTTP(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("test database not configured")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task := "987654321"
	defer d.Exec(`DELETE FROM task_tool_grants WHERE task_id=$1`, task)
	defer d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, task)
	s := &Server{m: &Manager{pg: d, interceptor: intercept.New(d)}, jwtKey: []byte("approval-parameter-fixture-key")}
	token, err := signTestSession(t, s)
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, body string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		return w
	}
	id, err := d.CreateInterceptPending(0, 0, task, "test", "Write", []byte(`{"content":"fixture","file_path":"/tmp/report"}`), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/intercept/pending/%d/decide", id)
	body := `{"decision":"allowed","grant":"input"}`
	if w := do("POST", path, body, false); w.Code != 401 {
		t.Fatalf("unauthorized approval: %d", w.Code)
	}
	if w := do("POST", path, body, true); w.Code != 200 {
		t.Fatalf("approval: %d %s", w.Code, w.Body.String())
	}
	if w := do("GET", "/api/intercept/grants", "", false); w.Code != 401 {
		t.Fatalf("unprotected grants: %d", w.Code)
	}
	if w := do("GET", "/api/intercept/grants", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), "fixture") {
		t.Fatalf("grants list: %d %s", w.Code, w.Body.String())
	}
	grants, err := d.ListTaskToolGrants(task)
	if err != nil || len(grants) != 1 || grants[0].Scope != "input" {
		t.Fatalf("grant missing: %+v %v", grants, err)
	}
	if w := do("POST", path, `{"decision":"allowed","grant":"all"}`, true); w.Code != 409 {
		t.Fatalf("duplicate approval: %d", w.Code)
	}
	grants, err = d.ListTaskToolGrants(task)
	if err != nil || len(grants) != 1 {
		t.Fatal("stale approval widened grant")
	}
	if w := do("DELETE", fmt.Sprintf("/api/intercept/task/%s/grants/%d", task, grants[0].ID), "", true); w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	grants, err = d.ListTaskToolGrants(task)
	if err != nil || len(grants) != 0 {
		t.Fatal("revoked grant remains")
	}
}

func TestInterceptStructuredRuleValidation(t *testing.T) {
	req := interceptRuleReq{Name: "exact tool", MatchTarget: "tool_call", MatchType: "string", Pattern: `{"tool":"Bash","arguments":{"command":"pwd"}}`, Action: "allow"}
	if err := validateInterceptRuleReq(req); err != nil {
		t.Fatal(err)
	}
	req.Pattern = `{"tool":"Bash","arguments":null}`
	if err := validateInterceptRuleReq(req); err == nil {
		t.Fatal("invalid arguments accepted")
	}
}
