package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/jira"
)

// fakeJira is an in-memory Jira Cloud site covering the calls Counterweight makes.
type fakeJira struct {
	mu          sync.Mutex
	created     []map[string]any // fields of each created issue, in order
	comments    map[string][]string
	transitions map[string]string // issue key -> transition id posted
	links       map[string]string // issue key -> remote link globalId
}

func newFakeJira(t *testing.T) (*fakeJira, *httptest.Server) {
	jira.AllowLoopbackHTTP = true
	f := &fakeJira{comments: map[string][]string{}, transitions: map[string]string{}, links: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeJira) serve(w http.ResponseWriter, r *http.Request) {
	if u, p, ok := r.BasicAuth(); !ok || u != "appsec@example.com" || p != "jira-test-token" {
		w.WriteHeader(401)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	field := func(id string, required bool, allowed ...map[string]string) map[string]any {
		return map[string]any{"fieldId": id, "name": id, "required": required, "allowedValues": allowed}
	}
	switch {
	case path == "/rest/api/3/myself":
		_ = json.NewEncoder(w).Encode(map[string]string{"accountId": "acc-1", "displayName": "AppSec Bot"})
	case path == "/rest/api/3/issue/createmeta/SEC/issuetypes":
		_, _ = io.WriteString(w, `{"issueTypes":[{"id":"10000","name":"Epic","hierarchyLevel":1},{"id":"10001","name":"Bug","hierarchyLevel":0}]}`)
	case strings.HasPrefix(path, "/rest/api/3/issue/createmeta/SEC/issuetypes/"):
		fields := []any{field("summary", true), field("issuetype", true), field("project", true), field("description", false),
			field("labels", false), field("duedate", false),
			field("priority", false, map[string]string{"id": "1", "name": "Highest"}, map[string]string{"id": "2", "name": "High"})}
		if strings.HasSuffix(path, "/10001") {
			fields = append(fields, field("parent", false))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"fields": fields, "total": len(fields)})
	case path == "/rest/api/3/issue" && r.Method == http.MethodPost:
		var body struct {
			Fields map[string]any `json:"fields"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body.Fields)
		n := len(f.created)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": strconv.Itoa(10000 + n), "key": fmt.Sprintf("SEC-%d", n)})
	case strings.HasSuffix(path, "/remotelink"):
		var body struct {
			GlobalID string `json:"globalId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.links[strings.Split(path, "/")[5]] = body.GlobalID
		w.WriteHeader(201)
	case strings.HasSuffix(path, "/comment"):
		raw, _ := io.ReadAll(r.Body)
		key := strings.Split(path, "/")[5]
		f.comments[key] = append(f.comments[key], string(raw))
		w.WriteHeader(201)
	case strings.HasSuffix(path, "/transitions") && r.Method == http.MethodGet:
		_, _ = io.WriteString(w, `{"transitions":[{"id":"21","name":"Start","to":{"name":"In Progress","statusCategory":{"key":"indeterminate"}}},{"id":"31","name":"Close","to":{"name":"Done","statusCategory":{"key":"done"}}}]}`)
	case strings.HasSuffix(path, "/transitions"):
		var body struct {
			Transition struct {
				ID string `json:"id"`
			} `json:"transition"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.transitions[strings.Split(path, "/")[5]] = body.Transition.ID
		w.WriteHeader(204)
	case strings.HasPrefix(path, "/rest/api/3/issue/SEC-"):
		key := strings.TrimPrefix(path, "/rest/api/3/issue/")
		n, _ := strconv.Atoi(strings.TrimPrefix(key, "SEC-"))
		if n < 1 || n > len(f.created) {
			w.WriteHeader(404)
			return
		}
		_, _ = io.WriteString(w, `{"key":"`+key+`","fields":{"status":{"name":"To Do","statusCategory":{"key":"new"}}}}`)
	default:
		w.WriteHeader(404)
	}
}

func TestJiraExportsTaskAsEpicAndSyncsFindingChanges(t *testing.T) {
	s, recorded, request := trafficEvidenceServer(t)
	fake, srv := newFakeJira(t)
	t.Cleanup(func() {
		_, _ = s.m.pg.Exec(`DELETE FROM jira_links WHERE site=$1`, srv.URL)
		_, _ = s.m.pg.Exec(`DELETE FROM settings WHERE key=$1`, settingJiraConfig)
	})
	finding, err := s.m.pg.GetFinding(recorded.FindingID)
	if err != nil || finding.TaskID == nil {
		t.Fatalf("fixture finding: %v", err)
	}
	tid := *finding.TaskID
	evidence := "Trace (confirmed by source trace, not executed):\n1. entrypoint app/app.py:10: GET /search reads q.\n2. sink app/app.py:14: q is formatted into SQL.\nRemediation: bind q as a parameter."
	second, err := s.m.pg.AddFinding(tid, 0, "sqli", "SQL injection in search", "high", "The search query reaches SQL unparameterized.", evidence, "test", nil)
	if err != nil {
		t.Fatal(err)
	}

	if w := request("PUT", "/api/integrations/jira/credentials", `{"site":"`+srv.URL+`","email":"appsec@example.com","token":"jira-test-token"}`); w.Code != 200 {
		t.Fatalf("save credentials: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/integrations/jira", ""); w.Code != 200 || strings.Contains(w.Body.String(), "jira-test-token") ||
		!strings.Contains(w.Body.String(), `"account":"AppSec Bot"`) {
		t.Fatalf("status leaks the token or misses the account: %s", w.Body.String())
	}
	if w := request("PUT", "/api/integrations/jira/config", `{"project_key":"sec","issue_type_id":"10001","priorities":{"high":"2"},"sla_days":{"high":30},"labels":["App Sec Team"],"sync":true}`); w.Code != 200 {
		t.Fatalf("save config: %d %s", w.Code, w.Body.String())
	}

	w := request("POST", fmt.Sprintf("/api/integrations/jira/tasks/%d/export", tid), `{"scope":"all"}`)
	if w.Code != 200 {
		t.Fatalf("export task: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Epic    *jiraLinkView    `json:"epic"`
		Results []jiraTaskResult `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Epic == nil || out.Epic.Key != "SEC-1" || !out.Epic.Created || len(out.Results) != 2 {
		t.Fatalf("export result = %s", w.Body.String())
	}
	if len(fake.created) != 3 {
		t.Fatalf("created %d issues, want epic + 2", len(fake.created))
	}
	epic := fake.created[0]
	if epic["issuetype"].(map[string]any)["id"] != "10000" || !strings.HasPrefix(epic["summary"].(string), "[Security] ") {
		t.Fatalf("epic fields = %v", epic)
	}
	var sqli map[string]any
	for _, c := range fake.created[1:] {
		if parent, _ := c["parent"].(map[string]any); parent["key"] != "SEC-1" {
			t.Fatalf("child issue without the epic as parent: %v", c)
		}
		if strings.Contains(c["summary"].(string), "SQL injection") {
			sqli = c
		}
	}
	if sqli == nil || sqli["summary"] != "[High] SQL injection in search" {
		t.Fatalf("sqli issue missing: %v", fake.created)
	}
	if p, _ := sqli["priority"].(map[string]any); p["id"] != "2" {
		t.Fatalf("priority not mapped: %v", sqli["priority"])
	}
	if due, _ := sqli["duedate"].(string); len(due) != 10 {
		t.Fatalf("due date not set: %v", sqli["duedate"])
	}
	labels, _ := json.Marshal(sqli["labels"])
	for _, want := range []string{`"severity-high"`, `"cwe-89"`, `"app-sec-team"`} {
		if !strings.Contains(string(labels), want) {
			t.Errorf("labels %s missing %s", labels, want)
		}
	}
	desc, _ := json.Marshal(sqli["description"])
	for _, want := range []string{`"type":"panel"`, "How it happens", "How to fix", "bind q as a parameter", "How to verify the fix"} {
		if !strings.Contains(string(desc), want) {
			t.Errorf("description missing %q", want)
		}
	}

	// A second export files nothing new.
	if w := request("POST", fmt.Sprintf("/api/integrations/jira/tasks/%d/export", tid), `{"scope":"all"}`); w.Code != 200 || len(fake.created) != 3 {
		t.Fatalf("re-export created duplicates: %d issues, %s", len(fake.created), w.Body.String())
	}
	if w := request("POST", fmt.Sprintf("/api/integrations/jira/findings/%d/export", second), ""); w.Code != 200 || len(fake.created) != 3 ||
		!strings.Contains(w.Body.String(), `"created":false`) {
		t.Fatalf("single export duplicated: %s", w.Body.String())
	}
	if w := request("GET", fmt.Sprintf("/api/integrations/jira/tasks/%d/links?refresh=1", tid), ""); w.Code != 200 ||
		!strings.Contains(w.Body.String(), strconv.FormatInt(second, 10)) || !strings.Contains(w.Body.String(), `"status_name":"To Do"`) {
		t.Fatalf("links = %s", w.Body.String())
	}

	// Marking the finding fixed comments once and moves the issue to Done.
	if _, err := s.m.pg.SetFindingStatus(second, db.FindingFixed); err != nil {
		t.Fatal(err)
	}
	link, _ := s.m.pg.JiraFindingLink(context.Background(), second, srv.URL)
	for i := 0; i < 2; i++ {
		if _, err := s.syncJira(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := fake.comments[link.IssueKey]; len(got) != 1 || !strings.Contains(got[0], "marked fixed") {
		t.Fatalf("comments on %s = %v", link.IssueKey, got)
	}
	if fake.transitions[link.IssueKey] != "31" {
		t.Fatalf("transition = %q, want Done (31)", fake.transitions[link.IssueKey])
	}

	// The task is bound to its epic's space: another space is refused.
	third, err := s.m.pg.AddFinding(tid, 0, "xss", "Reflected XSS", "medium", "Unescaped output.", "", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if w := request("POST", fmt.Sprintf("/api/integrations/jira/findings/%d/export", third), `{"project_key":"OPS","issue_type_id":"10001"}`); w.Code != 400 ||
		!strings.Contains(w.Body.String(), "filed in the Jira space SEC") {
		t.Fatalf("export to another space = %d %s", w.Code, w.Body.String())
	}
	// A security level the space cannot apply fails the export instead of filing an open issue.
	created := len(fake.created)
	if w := request("POST", fmt.Sprintf("/api/integrations/jira/findings/%d/export", third), `{"security_level_id":"10100"}`); w.Code == 200 ||
		!strings.Contains(w.Body.String(), "security level is not available") || len(fake.created) != created {
		t.Fatalf("restricted export = %d %s", w.Code, w.Body.String())
	}
	// Changing the site requires its token again.
	if w := request("PUT", "/api/integrations/jira/credentials", `{"site":"https://other.atlassian.net","email":"appsec@example.com","token":""}`); w.Code != 400 {
		t.Fatalf("site change without token = %d %s", w.Code, w.Body.String())
	}
}

func TestJiraConfigRejectsBadInput(t *testing.T) {
	for _, cfg := range []jiraConfig{
		{ProjectKey: "bad key!"},
		{IssueTypeID: "abc"},
		{SLADays: map[string]int{"high": -1}},
		{Priorities: map[string]string{"high": "x"}},
	} {
		if _, err := validateJiraConfig(cfg); err == nil {
			t.Errorf("accepted %+v", cfg)
		}
	}
	cfg, err := validateJiraConfig(jiraConfig{ProjectKey: " sec ", Labels: []string{"Team A", "", "x"}, SLADays: map[string]int{"low": 0, "high": 30}})
	if err != nil || cfg.ProjectKey != "SEC" || strings.Join(cfg.Labels, ",") != "team-a,x" || len(cfg.SLADays) != 1 {
		t.Fatalf("normalized config = %+v, %v", cfg, err)
	}
}
