package report

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/authz"
	"github.com/Philocyber/counterweight/db"
)

func sampleAuthzRun(roles int) *authz.Report {
	r := &authz.Report{
		Target: "https://staging.example.com", StartedAt: time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC),
		Result: authz.Fail, ExitCode: 1, Counts: authz.Counts{Cases: 2, Checks: 2 * roles, Passed: 2*roles - 2, Failed: 1, Inconclusive: 1},
	}
	for i := 0; i < roles; i++ {
		p := authz.PersonaResult{Name: fmt.Sprintf("role_%d", i), Role: fmt.Sprintf("Role number %d", i), Authenticated: true, IdentityVerified: true}
		if i == 0 {
			p = authz.PersonaResult{Name: "anonymous"} // no role name: the persona name is shown
		}
		r.Personas = append(r.Personas, p)
	}
	for _, id := range []string{"admin-users", "doc-a-read"} {
		c := authz.CaseResult{ID: id, Method: "GET", Path: "/api/" + id, Result: authz.Pass}
		for i, p := range r.Personas {
			ch := authz.CheckResult{Persona: p.Name, Expect: "deny", ExpectedStatus: []int{401, 403}, Status: 403, Result: authz.Pass}
			if i == 1 && id == "admin-users" {
				ch.Status, ch.Result, ch.Kind = 200, authz.Fail, "access_granted"
				ch.Detail, ch.Excerpt = "expected a denial (401 or 403), the target answered 200", `{"users":[{"email":"victim@tenant-b.test"}]}`
			}
			if i == 1 && id == "doc-a-read" {
				ch.Status, ch.Result, ch.Kind, ch.Detail = 0, authz.Inconclusive, "request_failed", "no answer: connection refused"
			}
			c.Checks = append(c.Checks, ch)
		}
		r.Cases = append(r.Cases, c)
	}
	return r
}

func TestAuthorizationMatrixInTheTaskReport(t *testing.T) {
	in := Input{Status: "done", GeneratedAt: time.Unix(0, 0), FindingRecords: []*db.DBFinding{}, Authz: sampleAuthzRun(3)}
	md := Markdown(in)
	for _, want := range []string{
		"## Authorization matrix",
		"run on 2026-11-10 09:30 UTC against https://staging.example.com without a model",
		"Result: FAIL (6 checks: 4 passed, 1 failed, 1 inconclusive)",
		"| Case | Request | anonymous | Role number 1 | Role number 2 |",
		"| admin-users | `GET /api/admin-users` | deny 401/403, got 403: pass | deny 401/403, got 200: FAIL | deny 401/403, got 403: pass |",
		"deny 401/403, got no answer: INCONCLUSIVE",
		"- admin-users, as Role number 1: FAIL (access_granted). expected a denial (401 or 403), the target answered 200",
		"- doc-a-read, as Role number 1: INCONCLUSIVE (request_failed). no answer: connection refused",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the report lacks %q", want)
		}
	}
	// The excerpt of a failed check is data of the target; the report leaves it out.
	if strings.Contains(md, "victim@tenant-b.test") {
		t.Error("the report shows a response excerpt")
	}
	// Operator text cannot start a link, a heading or a new table cell.
	hostile := sampleAuthzRun(3)
	hostile.Personas[1].Role = "Evil [role](http://evil.example) | x"
	hostile.Errors = []string{"persona admin: ## not a heading <b>"}
	md = Markdown(Input{Status: "done", GeneratedAt: time.Unix(0, 0), FindingRecords: []*db.DBFinding{}, Authz: hostile})
	for _, bad := range []string{"[role](http://evil.example)", "example) | x |", "<b>"} {
		if strings.Contains(md, bad) {
			t.Errorf("operator text reached the report as Markdown: %q", bad)
		}
	}
	// A task without a run has no such section.
	in.Authz = nil
	if strings.Contains(Markdown(in), "Authorization matrix") {
		t.Error("section rendered without a run")
	}
}

func TestAuthorizationMatrixRendersInPDF(t *testing.T) {
	base := Input{Status: "done", GeneratedAt: time.Unix(0, 0), FindingRecords: []*db.DBFinding{}}
	plain, err := PDF(base, PDFOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pages := func(pdf []byte) int { return len(regexp.MustCompile(`/Type /Page\b`).FindAll(pdf, -1)) }
	// Few roles render as a matrix, many as one row per check; both must fit the page.
	long := sampleAuthzRun(3)
	long.Personas[2].Role = strings.Repeat("Very long role name ", 4)
	if order, shown := authzRoles(long); authzMatrixFits(order, shown) {
		t.Error("a role name too long for a column heading must not be drawn as a matrix column")
	}
	for _, roles := range []int{3, authzMatrixRoles + 3} {
		in := base
		in.Authz = sampleAuthzRun(roles)
		out, err := PDF(in, PDFOptions{})
		if err != nil {
			t.Fatalf("%d roles: %v", roles, err)
		}
		if !bytes.HasPrefix(out, []byte("%PDF-")) || pages(out) <= pages(plain) {
			t.Errorf("%d roles: the section added no page (%d vs %d)", roles, pages(out), pages(plain))
		}
	}
}
