package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// Credentials of the fixture. None of them may ever show up in a report.
const (
	pwA        = "pw-a-3f9c2e71"
	pwB        = "pw-b-8d1a6b40"
	adminToken = "fixture-admin-credential"
	tokenA     = "fixture-user-a-bearer"
	sessionB   = "sid-b-0a9d3c5e77"
)

var fixtureSecrets = []string{pwA, pwB, adminToken, tokenA, sessionB}

type who struct{ email, tenant, role string }

// fixture is a small two-tenant API. Its fields plant the flaws and failures
// the runner has to catch.
type fixture struct {
	mu            sync.Mutex
	adminOpen     bool   // any signed-in user reaches the admin route
	crossRead     bool   // any signed-in user reads tenant A's document
	crossWrite    bool   // any signed-in user changes tenant A's document
	docsGone      bool   // the document route answers 404 to everyone
	docsError     bool   // the document route answers 500
	formOpen      bool   // the form accepts the submission of any signed-in user
	listOpen      bool   // the list shows tenant A's rows to any signed-in user
	revokeAdminOn string // the admin token stops working after a request to this path
	adminRevoked  bool
	elsewhere     string // where /api/redirect points
}

func (f *fixture) who(r *http.Request) *who {
	switch auth := r.Header.Get("Authorization"); {
	case auth == "Bearer "+tokenA:
		return &who{"a@tenant-a.test", "a", "user"}
	case auth == "Bearer "+adminToken && !f.adminRevoked:
		return &who{"root@tenant-a.test", "a", "admin"}
	}
	if c, err := r.Cookie("sid"); err == nil && c.Value == sessionB {
		return &who{"b@tenant-b.test", "b", "user"}
	}
	return nil
}

func (f *fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	defer func() {
		if f.revokeAdminOn != "" && r.URL.Path == f.revokeAdminOn {
			f.adminRevoked = true
		}
	}()
	u, path := f.who(r), r.URL.Path
	switch {
	case r.Method == "POST" && path == "/api/login":
		var in struct{ Email, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Email != "a@tenant-a.test" || in.Password != pwA {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"token":%q}`, tokenA)
	case r.Method == "POST" && path == "/session":
		if r.FormValue("email") != "b@tenant-b.test" || r.FormValue("password") != pwB {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: sessionB})
		w.WriteHeader(http.StatusNoContent)
	case path == "/api/echo": // a careless error page that repeats the credentials it received
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "denied for %s / %s", r.Header.Get("Authorization"), r.Header.Get("Cookie"))
	case path == "/api/redirect":
		http.Redirect(w, r, f.elsewhere, http.StatusFound)
	case path == "/api/debug/config":
		http.NotFound(w, r)
	case path == "/api/form": // a form post: success and denial are both a 302, to different pages
		to := "/login"
		if u != nil && (u.tenant == "a" || f.formOpen) {
			to = "/done"
		}
		http.Redirect(w, r, to, http.StatusFound)
	case u == nil:
		http.Error(w, "sign in first", http.StatusUnauthorized)
	case path == "/api/list": // a list that answers 200 to everyone and filters by tenant
		if u.tenant == "a" || f.listOpen {
			fmt.Fprint(w, `[{"tenant":"a","title":"quarterly plan"}]`)
			return
		}
		fmt.Fprint(w, `[]`)
	case path == "/api/me":
		fmt.Fprintf(w, `{"email":%q,"tenant":%q,"role":%q}`, u.email, u.tenant, u.role)
	case path == "/api/admin/users":
		if u.role != "admin" && !f.adminOpen {
			http.Error(w, "admins only", http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `[{"email":"a@tenant-a.test"},{"email":"b@tenant-b.test"}]`)
	case strings.HasPrefix(path, "/api/docs/"):
		own := u.role == "admin" || u.tenant == "a"
		switch {
		case f.docsError:
			http.Error(w, "database is down", http.StatusInternalServerError)
		case f.docsGone || path != "/api/docs/doc-a1":
			http.NotFound(w, r)
		case r.Method == "GET" && (own || f.crossRead), r.Method == "PUT" && (own || f.crossWrite):
			fmt.Fprint(w, `{"id":"doc-a1","tenant":"a","title":"quarterly plan"}`)
		default:
			http.NotFound(w, r) // hides the document from other tenants
		}
	default:
		http.NotFound(w, r)
	}
}

func examplePolicy(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/example-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// runPolicy starts the fixture and runs the policy against it. env overrides
// the default environment; an empty value unsets a variable.
func runPolicy(t *testing.T, f *fixture, policy string, env map[string]string) *Report {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p, err := Parse([]byte(policy))
	if err != nil {
		t.Fatalf("policy does not validate:\n%v", err)
	}
	vars := map[string]string{
		"AUTHZ_BASE_URL":        srv.URL,
		"AUTHZ_USER_A_PASSWORD": pwA,
		"AUTHZ_USER_B_PASSWORD": pwB,
		"AUTHZ_ADMIN_TOKEN":     adminToken,
		"AUTHZ_DOC_A_ID":        "doc-a1",
	}
	for k, v := range env {
		vars[k] = v
	}
	rep := Run(context.Background(), p, Options{Getenv: func(k string) string { return vars[k] }})
	assertNoSecrets(t, rep)
	return rep
}

// assertNoSecrets checks both output forms of every report a test produces.
func assertNoSecrets(t *testing.T, rep *Report) {
	t.Helper()
	js, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var text bytes.Buffer
	rep.WriteText(&text)
	for _, s := range fixtureSecrets {
		if bytes.Contains(js, []byte(s)) || strings.Contains(text.String(), s) {
			t.Errorf("credential %q leaked into the report:\n%s\n%s", s, js, text.String())
		}
	}
}

func findCheck(t *testing.T, rep *Report, caseID, persona string) CheckResult {
	t.Helper()
	for _, c := range rep.Cases {
		if c.ID != caseID {
			continue
		}
		for _, ch := range c.Checks {
			if ch.Persona == persona {
				return ch
			}
		}
	}
	t.Fatalf("no check %s/%s in the report", caseID, persona)
	return CheckResult{}
}

func wantExit(t *testing.T, rep *Report, code int, result string) {
	t.Helper()
	if rep.ExitCode != code || rep.Result != result {
		var text bytes.Buffer
		rep.WriteText(&text)
		t.Fatalf("exit %d result %q, want exit %d result %q\n%s", rep.ExitCode, rep.Result, code, result, text.String())
	}
}

func TestCorrectTargetPasses(t *testing.T) {
	rep := runPolicy(t, &fixture{}, examplePolicy(t), nil)
	wantExit(t, rep, 0, Pass)
	if rep.Counts != (Counts{Cases: 4, Checks: 16, Passed: 16}) {
		t.Errorf("counts = %+v", rep.Counts)
	}
	for _, p := range rep.Personas {
		if p.Authenticated != (p.Name != "anonymous") || p.IdentityVerified != p.Authenticated {
			t.Errorf("persona %+v", p)
		}
	}
	if got := rep.Cases[1].Path; got != "/api/docs/${env:AUTHZ_DOC_A_ID}" {
		t.Errorf("the report must show the path as written in the policy, got %q", got)
	}
	// Denied personas are sent before allowed ones.
	var order []string
	for _, ch := range rep.Cases[2].Checks {
		order = append(order, ch.Persona)
	}
	if strings.Join(order, ",") != "anonymous,user_b,user_a,admin" {
		t.Errorf("check order = %v", order)
	}
}

func TestPlantedFlawsFail(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fixture       *fixture
		caseID        string
		persona       string
		failed        int
		otherCasePass string
	}{
		{"exposed admin route", &fixture{adminOpen: true}, "admin-users", "user_a", 2, "doc-a-read"},
		{"cross-tenant read", &fixture{crossRead: true}, "doc-a-read", "user_b", 1, "admin-users"},
		{"cross-tenant write", &fixture{crossWrite: true}, "doc-a-update", "user_b", 1, "doc-a-read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := runPolicy(t, tc.fixture, examplePolicy(t), nil)
			wantExit(t, rep, 1, Fail)
			ch := findCheck(t, rep, tc.caseID, tc.persona)
			if ch.Result != Fail || ch.Kind != "access_granted" || ch.Status != 200 || ch.Excerpt == "" {
				t.Errorf("check = %+v", ch)
			}
			if rep.Counts.Failed != tc.failed || rep.Counts.Inconclusive != 0 {
				t.Errorf("counts = %+v", rep.Counts)
			}
			for _, c := range rep.Cases {
				if c.ID == tc.otherCasePass && c.Result != Pass {
					t.Errorf("case %s = %s, want pass", c.ID, c.Result)
				}
			}
		})
	}
}

func TestBadCredentialStopsTheRun(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"expired token":  {"AUTHZ_ADMIN_TOKEN": "adm-expired-000000"},
		"wrong password": {"AUTHZ_USER_A_PASSWORD": "not-the-password"},
		"unset variable": {"AUTHZ_USER_B_PASSWORD": ""},
	} {
		t.Run(name, func(t *testing.T) {
			rep := runPolicy(t, &fixture{adminOpen: true}, examplePolicy(t), env)
			wantExit(t, rep, 2, ResultError)
			if rep.Counts.Checks != 0 || len(rep.Cases) != 0 || len(rep.Errors) == 0 {
				t.Errorf("no case may run after a setup error: %+v %v", rep.Counts, rep.Errors)
			}
			for _, e := range rep.Errors {
				if strings.Contains(e, "adm-expired-000000") || strings.Contains(e, "not-the-password") {
					t.Errorf("error repeats the credential: %s", e)
				}
			}
		})
	}
}

// A token that identifies someone else is as wrong as an expired one.
func TestMisassignedCredentialStopsTheRun(t *testing.T) {
	policy := strings.Replace(examplePolicy(t), `'"role":"admin"'`, `'"email":"b@tenant-b.test"'`, 1)
	rep := runPolicy(t, &fixture{}, policy, nil)
	wantExit(t, rep, 2, ResultError)
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "persona admin") || !strings.Contains(rep.Errors[0], "someone else") {
		t.Errorf("errors = %v", rep.Errors)
	}
}

func TestBrokenEndpointIsNeverAPass(t *testing.T) {
	rep := runPolicy(t, &fixture{docsGone: true}, examplePolicy(t), nil)
	wantExit(t, rep, 1, Fail)
	if ch := findCheck(t, rep, "doc-a-read", "user_a"); ch.Result != Fail || ch.Kind != "access_denied" {
		t.Errorf("owner check = %+v", ch)
	}
	// user_b got the 404 the policy expects, but so did everyone else.
	if ch := findCheck(t, rep, "doc-a-read", "user_b"); ch.Result != Inconclusive || ch.Kind != "no_baseline" {
		t.Errorf("denial without baseline = %+v", ch)
	}
	// anonymous still got its own 401; without a baseline it proves nothing either.
	if ch := findCheck(t, rep, "doc-a-read", "anonymous"); ch.Result != Inconclusive {
		t.Errorf("anonymous = %+v", ch)
	}
}

func TestServerErrorIsInconclusive(t *testing.T) {
	rep := runPolicy(t, &fixture{docsError: true}, examplePolicy(t), nil)
	wantExit(t, rep, 2, Inconclusive)
	if ch := findCheck(t, rep, "doc-a-read", "user_b"); ch.Result != Inconclusive || ch.Kind != "server_error" || ch.Status != 500 {
		t.Errorf("check = %+v", ch)
	}
	if rep.Counts.Failed != 0 || rep.Counts.Passed == 0 {
		t.Errorf("counts = %+v", rep.Counts)
	}
}

func TestSessionLostDuringTheRun(t *testing.T) {
	// The admin token dies on the last case; with adminOpen the run also holds a real violation.
	rep := runPolicy(t, &fixture{adminOpen: true, revokeAdminOn: "/api/debug/config"}, examplePolicy(t), nil)
	wantExit(t, rep, 1, Fail)
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "persona admin") {
		t.Fatalf("errors = %v", rep.Errors)
	}
	if ch := findCheck(t, rep, "doc-a-read", "admin"); ch.Result != Inconclusive || ch.Kind != "session_lost" {
		t.Errorf("admin result must be downgraded: %+v", ch)
	}
	if ch := findCheck(t, rep, "admin-users", "user_a"); ch.Result != Fail || ch.Kind != "access_granted" {
		t.Errorf("another persona's violation must stand: %+v", ch)
	}

	rep = runPolicy(t, &fixture{revokeAdminOn: "/api/debug/config"}, examplePolicy(t), nil)
	wantExit(t, rep, 2, ResultError)

	// Access wrongly granted to the persona that then lost its session stays a
	// failure: a dead session cannot explain it. Its other results are downgraded.
	policy := strings.Replace(probePolicy, "CASE", `request: { method: GET, path: /api/docs/doc-a1 }
    expect: { anonymous: { deny: 401 }, user_a: { allow: 200 }, user_b: { deny: 404 }, admin: { deny: 404 } }
  - id: last
    deny_all: true
    request: { method: GET, path: /api/debug/config }
    expect: { anonymous: { deny: 404 }, user_a: { deny: 404 }, user_b: { deny: 404 }, admin: { deny: 404 } }`, 1)
	rep = runPolicy(t, &fixture{revokeAdminOn: "/api/debug/config"}, policy, nil)
	wantExit(t, rep, 1, Fail)
	if ch := findCheck(t, rep, "probe", "admin"); ch.Result != Fail || ch.Kind != "access_granted" {
		t.Errorf("the lost persona's own violation must stand: %+v", ch)
	}
	if ch := findCheck(t, rep, "last", "admin"); ch.Result != Inconclusive || ch.Kind != "session_lost" {
		t.Errorf("the lost persona's other result must be downgraded: %+v", ch)
	}
}

// A denial whose status is also what access looks like has to be told apart
// by something else, or a granted request passes as denied.
func TestDenialThatSharesItsStatusWithAccess(t *testing.T) {
	form := strings.Replace(probePolicy, "CASE", `allow_write: true
    request: { method: POST, path: /api/form }
    expect:
      anonymous: { deny: 302, location_contains: /login }
      user_a: { allow: 302, location_contains: /done }
      user_b: { deny: 302, location_contains: /login }
      admin: { allow: 302 }`, 1)
	wantExit(t, runPolicy(t, &fixture{}, form, nil), 0, Pass)
	rep := runPolicy(t, &fixture{formOpen: true}, form, nil)
	wantExit(t, rep, 1, Fail)
	if ch := findCheck(t, rep, "probe", "user_b"); ch.Kind != "access_granted" || ch.Status != 302 {
		t.Errorf("check = %+v", ch)
	}
	// Without the redirect target the two answers are the same, and the file is refused.
	if _, err := Parse([]byte(strings.ReplaceAll(form, ", location_contains: /login", ""))); err == nil || !strings.Contains(err.Error(), "cannot be told apart from access") {
		t.Errorf("error = %v", err)
	}

	list := func(marker string) string {
		return strings.Replace(probePolicy, "CASE", `request: { method: GET, path: /api/list }
    expect:
      anonymous: { deny: 401 }
      user_a: { allow: 200 }
      user_b: { deny: 200, body_not_contains: ['`+marker+`'] }
      admin: { allow: 200 }`, 1)
	}
	wantExit(t, runPolicy(t, &fixture{}, list(`"tenant":"a"`), nil), 0, Pass)
	rep = runPolicy(t, &fixture{listOpen: true}, list(`"tenant":"a"`), nil)
	wantExit(t, rep, 1, Fail)
	if ch := findCheck(t, rep, "probe", "user_b"); ch.Kind != "data_exposed" {
		t.Errorf("check = %+v", ch)
	}
	// A text the target never sends (note the space) proves nothing by being
	// absent: the leak must not pass.
	rep = runPolicy(t, &fixture{listOpen: true}, list(`"tenant": "a"`), nil)
	wantExit(t, rep, 2, Inconclusive)
	if ch := findCheck(t, rep, "probe", "user_b"); ch.Kind != "unproven_marker" {
		t.Errorf("check = %+v", ch)
	}
}

func TestOutputCarriesNoResolvedVariable(t *testing.T) {
	const key = "tenant-key-for-the-list" // long enough to be a key, used outside a persona
	// In a check's detail: the text the response must not contain comes from a variable.
	policy := strings.Replace(probePolicy, "CASE", `request: { method: GET, path: "/api/list?key=${env:EXTRA}" }
    expect:
      anonymous: { deny: 401 }
      user_a: { allow: 200, body_not_contains: ["${env:SHOWN}"] }
      user_b: { deny: 200, body_contains: ["[]"] }
      admin: { allow: 200 }`, 1)
	rep := runPolicy(t, &fixture{}, policy, map[string]string{"EXTRA": key, "SHOWN": "quarterly plan"})
	wantExit(t, rep, 1, Fail)
	ch := findCheck(t, rep, "probe", "user_a")
	if ch.Kind != "data_exposed" || !strings.Contains(ch.Detail, "[REDACTED]") || strings.Contains(ch.Detail+ch.Excerpt, "quarterly plan") {
		t.Errorf("check = %+v", ch)
	}

	// In a setup error: the target is gone, and the client's own error would quote the full URL.
	srv := httptest.NewServer(&fixture{})
	srv.Close()
	p, err := Parse([]byte(strings.Replace(policy, "path: /api/login,", `path: "/api/login?key=${env:EXTRA}",`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"AUTHZ_BASE_URL": srv.URL, "AUTHZ_USER_A_PASSWORD": pwA, "AUTHZ_USER_B_PASSWORD": pwB, "AUTHZ_ADMIN_TOKEN": adminToken, "EXTRA": key, "SHOWN": "quarterly plan"}
	rep = Run(context.Background(), p, Options{Getenv: func(k string) string { return vars[k] }})
	wantExit(t, rep, 2, ResultError)
	js, _ := json.Marshal(rep)
	if len(rep.Errors) != 3 || strings.Contains(string(js), key) || strings.Contains(string(js), "/api/login") {
		t.Errorf("report = %s", js)
	}

	// In an identity error: the expected text comes from a variable.
	policy = strings.Replace(policy, `body_contains: ["admin"]`, `body_contains: ["${env:WHOAMI}"]`, 1)
	rep = runPolicy(t, &fixture{}, policy, map[string]string{"EXTRA": key, "SHOWN": "quarterly plan", "WHOAMI": "someone-else-entirely"})
	wantExit(t, rep, 2, ResultError)
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "[REDACTED]") || strings.Contains(rep.Errors[0], "someone-else") {
		t.Errorf("errors = %v", rep.Errors)
	}
}

func TestCredentialThatWouldNotBeSentAsStored(t *testing.T) {
	for name, value := range map[string]string{"trailing newline": adminToken + "\n", "leading space": " " + adminToken, "control character": "fixture\x00admin"} {
		t.Run(name, func(t *testing.T) {
			rep := runPolicy(t, &fixture{}, examplePolicy(t), map[string]string{"AUTHZ_ADMIN_TOKEN": value})
			wantExit(t, rep, 2, ResultError)
			if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "AUTHZ_ADMIN_TOKEN") || !strings.Contains(rep.Errors[0], "whitespace") || len(rep.Personas) != 0 {
				t.Errorf("nothing may be sent: %v %v", rep.Errors, rep.Personas)
			}
		})
	}
}

func TestBaseURLFromEnvironmentIsChecked(t *testing.T) {
	srv := httptest.NewServer(&fixture{})
	defer srv.Close()
	for _, base := range []string{srv.URL + "?", srv.URL + "#x", "ftp://staging.example.com", "https://user:pw@staging.example.com"} {
		rep := runPolicy(t, &fixture{}, examplePolicy(t), map[string]string{"AUTHZ_BASE_URL": base})
		wantExit(t, rep, 2, ResultError)
		if rep.Counts.Checks != 0 || len(rep.Personas) != 0 {
			t.Errorf("%s: nothing may be sent: %+v", base, rep)
		}
	}
}

const probePolicy = `
version: 1
target: { base_url: "${env:AUTHZ_BASE_URL}", allow_plain_http: true }
personas:
  anonymous: {}
  user_a:
    login:
      request: { method: POST, path: /api/login, json: { email: a@tenant-a.test, password: "${env:AUTHZ_USER_A_PASSWORD}" } }
      token: { json_pointer: /token, prefix: "Bearer " }
    identity: { request: { method: GET, path: /api/me }, body_contains: ["a@tenant-a.test"] }
  user_b:
    login:
      request: { method: POST, path: /session, form: { email: b@tenant-b.test, password: "${env:AUTHZ_USER_B_PASSWORD}" } }
      status: 204
    identity: { request: { method: GET, path: /api/me }, body_contains: ["b@tenant-b.test"] }
  admin:
    headers: { Authorization: "Bearer ${env:AUTHZ_ADMIN_TOKEN}" }
    identity: { request: { method: GET, path: /api/me }, body_contains: ["admin"] }
cases:
  - id: probe
    CASE
`

func TestCredentialsEchoedByTheTargetAreRedacted(t *testing.T) {
	policy := strings.Replace(probePolicy, "CASE", `request: { method: GET, path: /api/echo }
    expect: { anonymous: { deny: 401 }, user_a: { allow: 200 }, user_b: { allow: 200 }, admin: { allow: 200 } }`, 1)
	rep := runPolicy(t, &fixture{}, policy, nil) // runPolicy asserts that no credential is in the report
	wantExit(t, rep, 1, Fail)
	for _, persona := range []string{"user_a", "user_b", "admin"} {
		if ch := findCheck(t, rep, "probe", persona); !strings.Contains(ch.Excerpt, "[REDACTED]") {
			t.Errorf("%s excerpt = %q", persona, ch.Excerpt)
		}
	}
}

func TestExtraRedactorIsApplied(t *testing.T) {
	srv := httptest.NewServer(&fixture{})
	defer srv.Close()
	p, err := Parse([]byte(strings.Replace(probePolicy, "CASE", `request: { method: GET, path: /api/admin/users }
    expect: { anonymous: { deny: 401 }, user_a: { allow: 200 }, user_b: { deny: 403 }, admin: { allow: 200 } }`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"AUTHZ_BASE_URL": srv.URL, "AUTHZ_USER_A_PASSWORD": pwA, "AUTHZ_USER_B_PASSWORD": pwB, "AUTHZ_ADMIN_TOKEN": adminToken}
	rep := Run(context.Background(), p, Options{
		Getenv: func(k string) string { return vars[k] },
		Redact: func(s string) string { return strings.ReplaceAll(s, "admins only", "<scrubbed>") },
	})
	if ch := findCheck(t, rep, "probe", "user_a"); !strings.Contains(ch.Excerpt, "<scrubbed>") {
		t.Errorf("excerpt = %q", ch.Excerpt)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var hits int
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer other.Close()
	policy := strings.Replace(probePolicy, "CASE", `deny_all: true
    request: { method: GET, path: /api/redirect }
    expect: { anonymous: { deny: 302 }, user_a: { deny: 302 }, user_b: { deny: 302 }, admin: { deny: 302 } }`, 1)
	rep := runPolicy(t, &fixture{elsewhere: other.URL + "/landing"}, policy, nil)
	wantExit(t, rep, 0, Pass)
	if hits != 0 {
		t.Errorf("the runner followed a redirect to another host %d time(s)", hits)
	}
}

func TestClientIgnoresEnvironmentProxy(t *testing.T) {
	if tr := newClient(defaultTimeout, nil).Transport.(*http.Transport); tr.Proxy != nil {
		t.Error("the runner must not send credentials through a proxy taken from the environment")
	}
}

func TestRequestCannotLeaveTheTarget(t *testing.T) {
	// The path is valid in the policy file; the value of the variable is hostile.
	policy := strings.Replace(probePolicy, "CASE", `request: { method: GET, path: "/${env:AUTHZ_DOC_A_ID}" }
    expect: { anonymous: { deny: 401 }, user_a: { allow: 200 }, user_b: { deny: 404 }, admin: { allow: 200 } }`, 1)
	rep := runPolicy(t, &fixture{}, policy, map[string]string{"AUTHZ_DOC_A_ID": "/other.example/x"})
	wantExit(t, rep, 2, Inconclusive)
	if ch := findCheck(t, rep, "probe", "admin"); ch.Kind != "request_failed" {
		t.Errorf("check = %+v", ch)
	}
}

func TestJudgeBodyAssertions(t *testing.T) {
	r := &runner{red: &redactor{}}
	deny := &Expectation{Deny: Statuses{200}, BodyNotContains: []string{"tenant-a"}}
	allow := &Expectation{Allow: Statuses{200}, BodyContains: []string{"tenant-a"}}
	for _, tc := range []struct {
		name   string
		e      *Expectation
		resp   response
		result string
		kind   string
	}{
		{"soft denial holds", deny, response{status: 200, body: []byte(`[]`)}, Pass, ""},
		{"soft denial leaks", deny, response{status: 200, body: []byte(`[{"owner":"tenant-a"}]`)}, Fail, "data_exposed"},
		{"absence unproven when cut", deny, response{status: 200, body: []byte(`[]`), truncated: true}, Inconclusive, "truncated"},
		{"leak found even when cut", deny, response{status: 200, body: []byte(`tenant-a`), truncated: true}, Fail, "data_exposed"},
		{"allowed content present", allow, response{status: 200, body: []byte(`tenant-a`)}, Pass, ""},
		{"allowed content missing", allow, response{status: 200, body: []byte(`{}`)}, Fail, "content_missing"},
		{"missing may be past the cut", allow, response{status: 200, body: []byte(`{}`), truncated: true}, Inconclusive, "truncated"},
		{"rate limited", allow, response{status: 429}, Inconclusive, "server_error"},
		{"redirect to login", allow, response{status: 302}, Fail, "unexpected_status"},
		{"wrong denial status", &Expectation{Deny: Statuses{403}}, response{status: 404}, Fail, "unexpected_status"},
		{"non-2xx access status", &Expectation{Deny: Statuses{403}}, response{status: 304}, Fail, "access_granted"},
		{"soft denial page shown", &Expectation{Deny: Statuses{200}, BodyContains: []string{"access denied"}}, response{status: 200, body: []byte(`access denied`)}, Pass, ""},
		{"soft denial page absent", &Expectation{Deny: Statuses{200}, BodyContains: []string{"access denied"}}, response{status: 200, body: []byte(`{"id":"doc-a1"}`)}, Fail, "access_granted"},
		{"denial redirect to login", &Expectation{Deny: Statuses{302}, LocationContains: "/login"}, response{status: 302, location: "/login?next=/x"}, Pass, ""},
		{"denial redirect elsewhere", &Expectation{Deny: Statuses{302}, LocationContains: "/login"}, response{status: 302, location: "/done"}, Fail, "unexpected_redirect"},
		{"shared status, redirect elsewhere", &Expectation{Deny: Statuses{304}, LocationContains: "/login"}, response{status: 304, location: "/done"}, Fail, "access_granted"},
		{"allowed redirect elsewhere", &Expectation{Allow: Statuses{304}, LocationContains: "/done"}, response{status: 304, location: "/login"}, Fail, "unexpected_redirect"},
	} {
		got := r.judge("p", tc.e, Statuses{200, 304}, &tc.resp, nil)
		if got.Result != tc.result || got.Kind != tc.kind {
			t.Errorf("%s: got %s/%s, want %s/%s", tc.name, got.Result, got.Kind, tc.result, tc.kind)
		}
	}
}

func TestRedactor(t *testing.T) {
	r := &redactor{}
	r.add("abc")
	r.add("abc+def/ghi")
	if got := r.clean("x abc+def/ghi y abc z abc%2Bdef%2Fghi"); got != "x [REDACTED] y [REDACTED] z [REDACTED]" {
		t.Errorf("clean = %q", got)
	}
	// A credential that straddles the cut must not survive in part: the body
	// is cleaned before it is cut. No prefix of this one is registered.
	r = &redactor{}
	r.add("straddling-value")
	body := strings.Repeat("x", excerptBytes-5) + "straddling-value"
	if got := r.excerpt([]byte(body)); strings.Contains(got, "strad") || !strings.Contains(got, "[RED") {
		t.Errorf("excerpt = %q", got)
	}
	// The forms a value takes on the wire are removed too.
	r = &redactor{}
	r.add(" padded value/1 ")
	if got := r.clean("h: padded value/1|q=padded+value%2F1|p=padded%20value%2F1"); strings.Contains(got, "padded") {
		t.Errorf("clean = %q", got)
	}
}

func TestJSONPointer(t *testing.T) {
	doc := []byte(`{"data":{"tokens":[{"a/b":"first"},{"value":"second"}]},"n":7,"empty":""}`)
	for pointer, want := range map[string]string{"/data/tokens/0/a~1b": "first", "/data/tokens/1/value": "second"} {
		if got, err := jsonPointer(doc, pointer); err != nil || got != want {
			t.Errorf("%s = %q, %v", pointer, got, err)
		}
	}
	for _, pointer := range []string{"/missing", "/data/tokens/9/value", "/n", "/empty", "/n/deeper"} {
		if got, err := jsonPointer(doc, pointer); err == nil {
			t.Errorf("%s = %q, want an error", pointer, got)
		}
	}
	if _, err := jsonPointer([]byte("<html>"), "/token"); err == nil {
		t.Error("a non-JSON response must be an error")
	}
}

func TestExamplePolicyAndEnvVars(t *testing.T) {
	p, err := Parse([]byte(examplePolicy(t)))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.PersonaNames(), ","); got != "anonymous,user_a,user_b,admin" {
		t.Errorf("personas = %s", got)
	}
	var got []string
	for _, v := range p.EnvVars() {
		got = append(got, fmt.Sprintf("%s=%v", v.Name, v.Secret))
	}
	want := "AUTHZ_ADMIN_TOKEN=true,AUTHZ_BASE_URL=false,AUTHZ_DOC_A_ID=false,AUTHZ_USER_A_PASSWORD=true,AUTHZ_USER_B_PASSWORD=true"
	if strings.Join(got, ",") != want {
		t.Errorf("env vars = %v", got)
	}
}

func TestValidationRejects(t *testing.T) {
	base := examplePolicy(t)
	edit := func(old, new string) string {
		t.Helper()
		if !strings.Contains(base, old) {
			t.Fatalf("the example policy no longer contains %q", old)
		}
		return strings.Replace(base, old, new, 1)
	}
	for _, tc := range []struct {
		name, policy, want string
	}{
		{"empty file", "", "empty"},
		{"no cases", base[:strings.Index(base, "cases:")] + "cases: []\n", "empty matrix"},
		{"unknown key", edit("allow_plain_http: true", "allow_plain_htpp: true"), "allow_plain_htpp"},
		{"wrong version", edit("version: 1", "version: 2"), "version"},
		{"plain http by default", "version: 1\ntarget: { base_url: \"http://staging.example.com\" }\npersonas: { anonymous: {} }\ncases: []\n", "unencrypted"},
		{"credentials in the URL", "version: 1\ntarget: { base_url: \"https://u:p@staging.example.com\" }\npersonas: { anonymous: {} }\ncases: []\n", "must not carry credentials"},
		{"literal credential", edit(`"Bearer ${env:AUTHZ_ADMIN_TOKEN}"`, `"Bearer abc123"`), "must come from the environment"},
		{"credential without identity check", edit("    identity:\n      request: { method: GET, path: /api/me }\n      body_contains: ['\"role\":\"admin\"']\n", ""), "personas.admin.identity: required"},
		{"identity without marker", edit("      body_contains: ['\"role\":\"admin\"']\n", ""), "identity.body_contains: required"},
		{"missing persona in a case", edit("      user_b: { deny: 403 }\n", ""), "no expectation for persona user_b"},
		{"agent draft with empty expect", edit("    expect:\n      anonymous: { deny: 401 }\n      user_a: { deny: 403 }\n      user_b: { deny: 403 }\n      admin: { allow: 200 }\n", "    expect:\n      # observed: anonymous 401, user_a 403, user_b 403, admin 200\n"), "no expectation for persona anonymous"},
		{"second document", base + "---\nversion: 7\nfrobnicate: true\n", "more than one YAML document"},
		{"persona named null", edit("  anonymous: {}", "  null: {}"), "persona name must be plain text"},
		{"base URL with an empty query", "version: 1\ntarget: { base_url: \"https://staging.example.com?\" }\npersonas: { anonymous: {} }\ncases: []\n", "must not carry credentials, a query"},
		{"literal text beside the reference", edit(`"Bearer ${env:AUTHZ_ADMIN_TOKEN}"`, `"Bearer literal${env:AUTHZ_ADMIN_TOKEN}"`), "must come from the environment"},
		{"literal login password", edit(`password: "${env:AUTHZ_USER_A_PASSWORD}"`, `password: hunter-two`), "json field password looks like a credential"},
		{"literal login form password", edit(`password: "${env:AUTHZ_USER_B_PASSWORD}"`, `password: hunter-two`), "form field password looks like a credential"},
		{"literal login header", edit("        path: /session\n", "        path: /session\n        headers: { Authorization: \"Basic dXNlcjpwYXNz\" }\n"), "header Authorization looks like a credential"},
		{"fractional status", edit("user_b: { deny: 403 }", "user_b: { deny: 401.7 }"), "status must be a number"},
		{"denial status shared with access", strings.NewReplacer("user_b: { deny: 403 }", "user_b: { deny: 302 }", "admin: { allow: 200 }", "admin: { allow: 302 }").Replace(base), "cannot be told apart from access"},
		{"soft denial where nobody is allowed", edit("      anonymous: { deny: 404 }", "      anonymous: { deny: 200, body_not_contains: [secret] }"), "when no persona is allowed"},
		{"unknown persona in a case", edit("      user_b: { deny: 403 }", "      user_b: { deny: 403 }\n      user_c: { deny: 403 }"), "expect.user_c names a persona that is not defined"},
		{"allow and deny", edit("user_b: { deny: 403 }", "user_b: { deny: 403, allow: 200 }"), "allow or deny, not both"},
		{"neither allow nor deny", edit("user_b: { deny: 403 }", "user_b: { body_contains: [x] }"), "set allow or deny"},
		{"no baseline", edit("admin: { allow: 200 }", "admin: { deny: 403 }"), "a broken endpoint would pass"},
		{"deny_all with an allowed persona", edit("id: admin-users", "id: admin-users\n    deny_all: true"), "deny_all is set but a persona is allowed"},
		{"write without confirmation", edit("    allow_write: true\n", ""), "set allow_write: true"},
		{"denial answered with 200", edit("user_b: { deny: 403 }", "user_b: { deny: 200 }"), "cannot be told apart from access"},
		{"status out of range", edit("user_b: { deny: 403 }", "user_b: { deny: 700 }"), "700 is not an HTTP status"},
		{"status not a number", edit("user_b: { deny: 403 }", "user_b: { deny: forbidden }"), "status must be a number"},
		{"absolute URL as path", edit("path: /api/admin/users", "path: https://other.example/api"), "starts with a single /"},
		{"protocol-relative path", edit("path: /api/admin/users", "path: //other.example/api"), "starts with a single /"},
		{"credential header on a case", edit("request: { method: GET, path: /api/admin/users }", "request: { method: GET, path: /api/admin/users, headers: { Authorization: x } }"), "credentials belong to the persona"},
		{"unknown method", edit("request: { method: GET, path: /api/admin/users }", "request: { method: TRACE, path: /api/admin/users }"), "method: must be one of"},
		{"duplicate case id", edit("id: doc-a-read", "id: admin-users"), "used twice"},
		{"malformed reference", edit("${env:AUTHZ_DOC_A_ID}", "${AUTHZ_DOC_A_ID}"), "malformed reference"},
		{"two bodies", edit("json: { title: authz-check }", "json: { title: authz-check }\n      body: raw"), "only one of json, form and body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.policy))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v\nwant it to contain %q", err, tc.want)
			}
		})
	}
}

// Every problem is reported at once, not one per attempt.
func TestValidationReportsAllProblems(t *testing.T) {
	_, err := Parse([]byte("version: 3\ntarget: {}\npersonas: {}\ncases: []\n"))
	if err == nil || strings.Count(err.Error(), "\n") != 3 {
		t.Errorf("want four problems, got: %v", err)
	}
}
