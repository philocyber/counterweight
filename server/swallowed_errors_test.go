package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
)

// A timestamp of 'infinity' cannot be scanned into time.Time or cast to an
// epoch bigint, which gives these tests a real database error for one row.

func TestFindingsListReportsQueryErrorInsteadOfEmptyList(t *testing.T) {
	s, recorded, request := trafficEvidenceServer(t)
	if w := request("GET", "/api/exploration/findings", ""); w.Code != 200 {
		t.Fatalf("healthy list = %d %s", w.Code, w.Body.String())
	}
	if _, err := s.m.pg.Exec(`UPDATE findings SET created_at='infinity' WHERE id=$1`, recorded.FindingID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.m.pg.Exec(`DELETE FROM findings WHERE id=$1`, recorded.FindingID) })
	w := request("GET", "/api/exploration/findings", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failed query = %d %s, want 500", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] == nil {
		t.Fatalf("error body = %s", w.Body.String())
	}
}

func TestTaskListRendersWhenMetricsQueryFails(t *testing.T) {
	s, task := newActivityStreamFixture(t)
	a := s.engine.emitActivity(task, db.Activity{Worker: "worker", Kind: "text", Summary: "step"})
	if a.ID == 0 {
		t.Fatal("activity not persisted")
	}
	if _, err := s.m.pg.Exec(`UPDATE activity SET created_at='infinity' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.m.pg.Exec(`DELETE FROM activity WHERE id=$1`, a.ID) })
	if _, err := s.m.PG().TaskListMetricsAll(); err == nil {
		t.Fatal("fixture did not make the metrics query fail")
	}
	w := httptest.NewRecorder()
	s.listTasks(w, httptest.NewRequest(http.MethodGet, "/api/tasks", nil))
	if w.Code != 200 {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Tasks []TaskDTO `json:"tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, dto := range out.Tasks {
		if dto.ID == task.ID {
			return
		}
	}
	t.Fatalf("task %s missing from list after metrics failure", task.ID)
}

func TestJiraSyncContinuesPastOneFailingLink(t *testing.T) {
	s, recorded, request := trafficEvidenceServer(t)
	fake, srv := newFakeJira(t)
	finding, err := s.m.pg.GetFinding(recorded.FindingID)
	if err != nil || finding.TaskID == nil {
		t.Fatalf("fixture finding: %v", err)
	}
	tid := *finding.TaskID
	t.Cleanup(func() {
		_, _ = s.m.pg.Exec(`DELETE FROM jira_links WHERE site=$1`, srv.URL)
		_, _ = s.m.pg.Exec(`DELETE FROM settings WHERE key=$1 OR key=$2`, settingJiraConfig, db.TaskCodeReviewKey(tid))
	})
	broken := recorded.FindingID
	healthy, err := s.m.pg.AddFinding(tid, 0, "sqli", "SQL injection in search", "high", "Unparameterized query.", "", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := request("PUT", "/api/integrations/jira/credentials", `{"site":"`+srv.URL+`","email":"appsec@example.com","token":"jira-test-token"}`); w.Code != 200 {
		t.Fatalf("save credentials: %d %s", w.Code, w.Body.String())
	}
	if w := request("PUT", "/api/integrations/jira/config", `{"project_key":"sec","issue_type_id":"10001","sync":true}`); w.Code != 200 {
		t.Fatalf("save config: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", fmt.Sprintf("/api/integrations/jira/tasks/%d/export", tid), `{"scope":"all"}`); w.Code != 200 {
		t.Fatalf("export task: %d %s", w.Code, w.Body.String())
	}

	// A code-review task reads each finding's lifecycle; make that read fail for one finding.
	if err := s.m.pg.SetSetting(db.TaskCodeReviewKey(tid), `{"repository":"example/app"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.Exec(`INSERT INTO finding_retests(finding_id, snapshot, created_at) VALUES ($1, '{}', 'infinity')`, broken); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{broken, healthy} {
		if _, err := s.m.pg.SetFindingStatus(id, db.FindingFixed); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.syncJira(context.Background()); err != nil {
		t.Fatalf("one failing link aborted the pass: %v", err)
	}
	brokenLink, _ := s.m.pg.JiraFindingLink(context.Background(), broken, srv.URL)
	healthyLink, _ := s.m.pg.JiraFindingLink(context.Background(), healthy, srv.URL)
	if brokenLink == nil || healthyLink == nil {
		t.Fatal("export did not link both findings")
	}
	if got := fake.comments[healthyLink.IssueKey]; len(got) != 1 || !strings.Contains(got[0], "marked fixed") {
		t.Fatalf("healthy link comments = %v", got)
	}
	if got := fake.comments[brokenLink.IssueKey]; len(got) != 0 {
		t.Fatalf("failing link was updated: %v", got)
	}
}

func TestJiraSyncContinuesPastFailedStateWrite(t *testing.T) {
	s, recorded, request := trafficEvidenceServer(t)
	fake, srv := newFakeJira(t)
	finding, err := s.m.pg.GetFinding(recorded.FindingID)
	if err != nil || finding.TaskID == nil {
		t.Fatalf("fixture finding: %v", err)
	}
	tid := *finding.TaskID
	t.Cleanup(func() {
		_, _ = s.m.pg.Exec(`DROP TRIGGER IF EXISTS cw_test_refuse_link_state ON jira_links`)
		_, _ = s.m.pg.Exec(`DROP FUNCTION IF EXISTS cw_test_refuse_link_state()`)
		_, _ = s.m.pg.Exec(`DELETE FROM jira_links WHERE site=$1`, srv.URL)
		_, _ = s.m.pg.Exec(`DELETE FROM settings WHERE key=$1`, settingJiraConfig)
	})
	if _, err := s.m.pg.AddFinding(tid, 0, "sqli", "SQL injection in search", "high", "Unparameterized query.", "", "test", nil); err != nil {
		t.Fatal(err)
	}
	if w := request("PUT", "/api/integrations/jira/credentials", `{"site":"`+srv.URL+`","email":"appsec@example.com","token":"jira-test-token"}`); w.Code != 200 {
		t.Fatalf("save credentials: %d %s", w.Code, w.Body.String())
	}
	if w := request("PUT", "/api/integrations/jira/config", `{"project_key":"sec","issue_type_id":"10001","sync":true}`); w.Code != 200 {
		t.Fatalf("save config: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", fmt.Sprintf("/api/integrations/jira/tasks/%d/export", tid), `{"scope":"all"}`); w.Code != 200 {
		t.Fatalf("export task: %d %s", w.Code, w.Body.String())
	}
	// Break the link the sync visits first, so a pass that stops on it would
	// never reach the healthy one.
	links, err := s.m.pg.JiraFindingLinks(context.Background(), srv.URL)
	if err != nil || len(links) != 2 || links[0].FindingID == nil || links[1].FindingID == nil {
		t.Fatalf("finding links = %v, %v", links, err)
	}
	brokenLink, healthyLink := links[0], links[1]
	broken, healthy := *brokenLink.FindingID, *healthyLink.FindingID

	// Refuse every state write on the broken link's row.
	if _, err := s.m.pg.Exec(fmt.Sprintf(`CREATE FUNCTION cw_test_refuse_link_state() RETURNS trigger AS $$
BEGIN
	IF OLD.id = %d THEN RAISE EXCEPTION 'test: link state write refused'; END IF;
	RETURN NEW;
END $$ LANGUAGE plpgsql`, brokenLink.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.Exec(`CREATE TRIGGER cw_test_refuse_link_state BEFORE UPDATE ON jira_links
		FOR EACH ROW EXECUTE FUNCTION cw_test_refuse_link_state()`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{broken, healthy} {
		if _, err := s.m.pg.SetFindingStatus(id, db.FindingFixed); err != nil {
			t.Fatal(err)
		}
	}

	updated, err := s.syncJira(context.Background())
	if err == nil || !strings.Contains(err.Error(), "link state write refused") {
		t.Fatalf("sync error = %v, want the refused state write reported", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1 (the healthy link)", updated)
	}
	if got := fake.comments[healthyLink.IssueKey]; len(got) != 1 || !strings.Contains(got[0], "marked fixed") {
		t.Fatalf("healthy link comments = %v", got)
	}
	after, _ := s.m.pg.JiraFindingLink(context.Background(), healthy, srv.URL)
	if after == nil || after.LastState == healthyLink.LastState {
		t.Fatalf("healthy link state not saved: before=%+v after=%+v", healthyLink, after)
	}
}
