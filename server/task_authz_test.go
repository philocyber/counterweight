package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/sealed"
)

// Credentials of the authorization-check tests. None may appear in an API
// response, a stored report or the database in plaintext.
const (
	authzAdminBearer  = "fixture-admin-bearer-value"
	authzUserPassword = "fixture-user-password"
	authzIssuedBearer = "fixture-issued-session-value"
)

var authzSecrets = []string{authzAdminBearer, authzUserPassword, authzIssuedBearer}

type authzFixture struct {
	t      *testing.T
	s      *Server
	sess   string
	taskID int64
	target *httptest.Server
	hits   atomic.Int32
	// adminOpen plants the flaw: any signed-in user reaches the admin route.
	adminOpen atomic.Bool
	// probe is the last X-Probe header the target received.
	probe atomic.Value
}

func newAuthzFixture(t *testing.T) *authzFixture {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("isolated PostgreSQL unavailable: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	s.jwtKey = []byte(strings.Repeat("k", 32))
	f := &authzFixture{t: t, s: s}
	if f.sess, err = signTestSession(t, s); err != nil {
		t.Fatal(err)
	}
	f.taskID = f.newTask()
	f.target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if v := r.Header.Get("X-Probe"); v != "" {
			f.probe.Store(v)
		}
		auth := r.Header.Get("Authorization")
		admin, user := auth == "Bearer "+authzAdminBearer, auth == "Bearer "+authzIssuedBearer
		switch {
		case r.URL.Path == "/api/login":
			var in struct{ Email, Password string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Email != "b@tenant-b.test" || in.Password != authzUserPassword {
				http.Error(w, "bad credentials", http.StatusUnauthorized)
				return
			}
			fmt.Fprintf(w, `{"token":%q}`, authzIssuedBearer)
		case !admin && !user:
			http.Error(w, "sign in first", http.StatusUnauthorized)
		case r.URL.Path == "/api/me" && admin:
			fmt.Fprint(w, `{"email":"root@tenant-a.test","role":"admin"}`)
		case r.URL.Path == "/api/me":
			fmt.Fprint(w, `{"email":"b@tenant-b.test","role":"user"}`)
		case r.URL.Path == "/api/admin/users" && (admin || f.adminOpen.Load()):
			// What a real leak looks like: another user's session material.
			fmt.Fprintf(w, `{"users":[{"email":"b@tenant-b.test","session":%q}]}`, authzIssuedBearer)
		default:
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(f.target.Close)
	return f
}

func (f *authzFixture) newTask() int64 {
	f.t.Helper()
	task, err := f.s.m.pg.CreateTask("authorization check fixture", "check stored accounts over the API", nil, 0, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = f.s.m.pg.DeleteTask(task.ID) })
	return task.ID
}

func (f *authzFixture) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var raw []byte
	switch b := body.(type) {
	case nil:
	case string:
		raw = []byte(b)
	default:
		raw, _ = json.Marshal(b)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Host = "cw.example.test"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	for _, secret := range authzSecrets {
		if strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), sealed.Prefix) {
			f.t.Errorf("%s %s returned a credential: %s", method, path, w.Body.String())
		}
	}
	return w
}

func (f *authzFixture) path(suffix string) string {
	return fmt.Sprintf("/api/tasks/%d/authz%s", f.taskID, suffix)
}

func (f *authzFixture) policy() string {
	return strings.ReplaceAll(`version: 1
target: { base_url: "BASE", allow_plain_http: true }
personas:
  anonymous: {}
  tenant_b_user:
    login:
      request: { method: POST, path: /api/login, json: { email: "${account:tenant_b_user.username}", password: "${account:tenant_b_user.password}" } }
      token: { json_pointer: /token, prefix: "Bearer " }
    identity: { request: { method: GET, path: /api/me }, body_contains: ["${account:tenant_b_user.username}"] }
  platform_admin:
    headers: { Authorization: "Bearer ${account:platform_admin.token}" }
    identity: { request: { method: GET, path: /api/me }, body_contains: ['"role":"admin"'] }
cases:
  - id: admin-users
    request: { method: GET, path: /api/admin/users }
    expect: { anonymous: { deny: 401 }, tenant_b_user: { deny: 403 }, platform_admin: { allow: 200 } }
`, "BASE", f.target.URL)
}

func (f *authzFixture) addAccounts() {
	f.t.Helper()
	for _, body := range []map[string]any{
		{"role": "Tenant B user", "username": "b@tenant-b.test", "password": authzUserPassword},
		{"role": "Platform admin", "token": authzAdminBearer},
	} {
		if w := f.do("POST", f.path("/accounts"), f.sess, body); w.Code != 201 {
			f.t.Fatalf("create account: %d %s", w.Code, w.Body.String())
		}
	}
}

// run starts a run and waits for its stored report.
func (f *authzFixture) run() map[string]any {
	f.t.Helper()
	before := len(decodeBody(f.t, f.do("GET", f.path(""), f.sess, nil))["runs"].([]any))
	if w := f.do("POST", f.path("/runs"), f.sess, nil); w.Code != 202 {
		f.t.Fatalf("start run: %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state := decodeBody(f.t, f.do("GET", f.path(""), f.sess, nil))
		if runs := state["runs"].([]any); state["running"] == false && len(runs) > before {
			id := int64(runs[0].(map[string]any)["id"].(float64))
			return decodeBody(f.t, f.do("GET", f.path(fmt.Sprintf("/runs/%d", id)), f.sess, nil))
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.t.Fatal("the run did not finish")
	return nil
}

func TestTaskAuthzEndToEnd(t *testing.T) {
	f := newAuthzFixture(t)

	state := decodeBody(t, f.do("GET", f.path(""), f.sess, nil))
	if len(state["accounts"].([]any)) != 0 || state["policy"] != "" || state["check"] != nil || state["running"] != false || len(state["runs"].([]any)) != 0 {
		t.Fatalf("empty state: %v", state)
	}
	if w := f.do("POST", f.path("/runs"), f.sess, nil); w.Code != 400 {
		t.Fatalf("run without a policy: %d %s", w.Code, w.Body.String())
	}

	f.addAccounts()
	state = decodeBody(t, f.do("GET", f.path(""), f.sess, nil))
	accounts := state["accounts"].([]any)
	first := accounts[0].(map[string]any)
	if len(accounts) != 2 || first["key"] != "tenant_b_user" || first["role"] != "Tenant B user" || first["has_password"] != true || first["has_token"] != false {
		t.Fatalf("accounts: %v", accounts)
	}
	// At rest the secrets are sealed.
	var plain int
	if err := f.s.m.pg.QueryRow(`SELECT count(*) FROM task_authz_accounts WHERE task_id=$1 AND (password NOT LIKE 'cwenc:%' OR token NOT LIKE 'cwenc:%')`, f.taskID).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("%d secrets stored in plaintext (%v)", plain, err)
	}

	// A policy is saved even when it has problems, and the problems are listed.
	for name, tc := range map[string]struct{ policy, want string }{
		"not a policy":       {"version: 2\n", "version: must be 1"},
		"server environment": {strings.Replace(f.policy(), "${account:platform_admin.token}", "${env:PATH}", 1), "cannot read the server's environment"},
		"unknown account":    {strings.ReplaceAll(f.policy(), "account:platform_admin.token", "account:nobody.token"), "no test account with the key nobody"},
		"field not set":      {strings.Replace(f.policy(), "account:platform_admin.token", "account:tenant_b_user.token", 1), "has no token"},
	} {
		w := f.do("PUT", f.path("/policy"), f.sess, map[string]any{"policy": tc.policy})
		check, _ := decodeBody(t, w)["check"].(map[string]any)
		if w.Code != 200 || check["ok"] != false || !strings.Contains(fmt.Sprint(check["errors"]), tc.want) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
		if w := f.do("POST", f.path("/runs"), f.sess, nil); w.Code != 400 || !strings.Contains(w.Body.String(), `"check"`) {
			t.Errorf("%s: a run started on a policy with problems: %d %s", name, w.Code, w.Body.String())
		}
	}
	if f.hits.Load() != 0 {
		t.Fatalf("validation sent %d requests to the target", f.hits.Load())
	}

	w := f.do("PUT", f.path("/policy"), f.sess, map[string]any{"policy": f.policy()})
	check := decodeBody(t, w)["check"].(map[string]any)
	if w.Code != 200 || check["ok"] != true || check["cases"] != float64(1) || check["checks"] != float64(3) || len(check["personas"].([]any)) != 3 {
		t.Fatalf("valid policy: %d %s", w.Code, w.Body.String())
	}

	// A correct target passes, and every persona carries its role name.
	run := f.run()
	report := run["report"].(map[string]any)
	if run["result"] != "pass" || run["exit_code"] != float64(0) {
		t.Fatalf("run: %v", run)
	}
	roles := map[string]any{}
	for _, p := range report["personas"].([]any) {
		roles[p.(map[string]any)["name"].(string)] = p.(map[string]any)["role"]
	}
	if roles["tenant_b_user"] != "Tenant B user" || roles["platform_admin"] != "Platform admin" || roles["anonymous"] != nil {
		t.Fatalf("roles: %v", roles)
	}

	// The planted flaw fails, and the leaked session in the response is not stored.
	f.adminOpen.Store(true)
	run = f.run()
	if run["result"] != "fail" || run["exit_code"] != float64(1) || !strings.Contains(fmt.Sprint(run["report"]), "access_granted") || !strings.Contains(fmt.Sprint(run["report"]), "[REDACTED]") {
		t.Fatalf("run with the flaw: %v", run)
	}
	var stored string
	if err := f.s.m.pg.QueryRow(`SELECT string_agg(report::text, ' ') FROM task_authz_runs WHERE task_id=$1`, f.taskID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, secret := range authzSecrets {
		if strings.Contains(stored, secret) {
			t.Errorf("a stored report holds the credential %q", secret)
		}
	}
	state = decodeBody(t, f.do("GET", f.path(""), f.sess, nil))
	if runs := state["runs"].([]any); len(runs) != 2 || runs[0].(map[string]any)["result"] != "fail" || runs[0].(map[string]any)["target"] != f.target.URL {
		t.Fatalf("runs: %v", state["runs"])
	}

	// A second run while one is in progress is refused.
	authzInFlight.Store(authzRunKey{f.s, f.taskID}, struct{}{})
	if w := f.do("POST", f.path("/runs"), f.sess, nil); w.Code != 409 {
		t.Errorf("concurrent run: %d %s", w.Code, w.Body.String())
	}
	if decodeBody(t, f.do("GET", f.path(""), f.sess, nil))["running"] != true {
		t.Error("the state does not show the run in progress")
	}
	authzInFlight.Delete(authzRunKey{f.s, f.taskID})
}

func TestTaskAuthzRespectsBlockedTargets(t *testing.T) {
	f := newAuthzFixture(t)
	f.addAccounts()
	if _, err := f.s.m.Assets().CreateTaskInterceptRule(f.taskID, "block", "exact_ip", "127.0.0.1", "", true); err != nil {
		t.Fatal(err)
	}
	w := f.do("PUT", f.path("/policy"), f.sess, map[string]any{"policy": f.policy()})
	check := decodeBody(t, w)["check"].(map[string]any)
	if check["ok"] != false || !strings.Contains(fmt.Sprint(check["errors"]), "target.base_url") {
		t.Fatalf("a blocked target validated: %s", w.Body.String())
	}
	if w := f.do("POST", f.path("/runs"), f.sess, nil); w.Code != 400 {
		t.Fatalf("a run started against a blocked target: %d %s", w.Code, w.Body.String())
	}
	if f.hits.Load() != 0 {
		t.Fatalf("%d requests reached a blocked target", f.hits.Load())
	}
}

func TestTaskAuthzAccessAndInput(t *testing.T) {
	f := newAuthzFixture(t)
	f.addAccounts()
	accountID := int64(decodeBody(t, f.do("GET", f.path(""), f.sess, nil))["accounts"].([]any)[0].(map[string]any)["id"].(float64))
	account := fmt.Sprintf("/accounts/%d", accountID)

	// No session, no access; an automation token has no scope for these endpoints.
	for _, req := range [][2]string{{"GET", ""}, {"POST", "/accounts"}, {"PATCH", account}, {"DELETE", account}, {"PUT", "/policy"}, {"POST", "/runs"}, {"GET", "/runs/1"}} {
		if w := f.do(req[0], f.path(req[1]), "", map[string]any{}); w.Code != 401 {
			t.Errorf("%s %s without a session: %d", req[0], req[1], w.Code)
		}
		if scope := automationScope(httptest.NewRequest(req[0], f.path(req[1]), nil)); scope != "" {
			t.Errorf("%s %s is reachable with the API token scope %q", req[0], req[1], scope)
		}
	}

	// An account is reachable only through its own task.
	other := f.newTask()
	if w := f.do("PATCH", fmt.Sprintf("/api/tasks/%d/authz/accounts/%d", other, accountID), f.sess, map[string]any{"role": "Hijacked"}); w.Code != 404 {
		t.Errorf("update through another task: %d %s", w.Code, w.Body.String())
	}
	if w := f.do("DELETE", fmt.Sprintf("/api/tasks/%d/authz/accounts/%d", other, accountID), f.sess, nil); w.Code != 404 {
		t.Errorf("delete through another task: %d %s", w.Code, w.Body.String())
	}
	if w := f.do("GET", "/api/tasks/999999999/authz", f.sess, nil); w.Code != 404 {
		t.Errorf("unknown task: %d", w.Code)
	}

	// Update: an absent secret is kept, an empty one is removed.
	w := f.do("PATCH", f.path(account), f.sess, map[string]any{"role": "Tenant B member"})
	if got := decodeBody(t, w); w.Code != 200 || got["role"] != "Tenant B member" || got["key"] != "tenant_b_user" || got["has_password"] != true {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	w = f.do("PATCH", f.path(account), f.sess, map[string]any{"password": ""})
	if got := decodeBody(t, w); w.Code != 200 || got["has_password"] != false {
		t.Fatalf("remove password: %d %s", w.Code, w.Body.String())
	}

	// Refused input, with a status the console can explain. A body that cannot
	// be read is never quoted back: it may hold a password.
	for name, tc := range map[string]struct {
		method, path string
		body         any
		code         int
	}{
		"same key":             {"POST", "/accounts", map[string]any{"role": "tenant-b user"}, 409},
		"no role":              {"POST", "/accounts", map[string]any{"username": "x"}, 400},
		"secret with newline":  {"POST", "/accounts", map[string]any{"role": "Padded", "password": authzUserPassword + "\n"}, 400},
		"unknown field":        {"POST", "/accounts", map[string]any{"role": "Extra", "key": "chosen"}, 400},
		"broken body":          {"POST", "/accounts", `{"role":"Broken","password":"` + authzUserPassword, 400},
		"unknown account":      {"PATCH", "/accounts/999999999", map[string]any{"role": "Ghost"}, 404},
		"policy missing":       {"PUT", "/policy", map[string]any{}, 400},
		"policy too large":     {"PUT", "/policy", map[string]any{"policy": strings.Repeat("#", 256<<10+1)}, 413},
		"unknown run":          {"GET", "/runs/999999999", nil, 404},
		"run id not a number":  {"GET", "/runs/abc", nil, 404},
		"account not a number": {"DELETE", "/accounts/abc", nil, 404},
	} {
		if w := f.do(tc.method, f.path(tc.path), f.sess, tc.body); w.Code != tc.code {
			t.Errorf("%s: %d, want %d: %s", name, w.Code, tc.code, w.Body.String())
		}
	}

	// A code review task has no running application to check.
	if _, err := f.s.m.pg.Exec(`UPDATE tasks SET assessment_type='code_review' WHERE id=$1`, other); err != nil {
		t.Fatal(err)
	}
	if w := f.do("GET", fmt.Sprintf("/api/tasks/%d/authz", other), f.sess, nil); w.Code != 400 {
		t.Errorf("code review task: %d %s", w.Code, w.Body.String())
	}

	if w := f.do("DELETE", f.path(account), f.sess, nil); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := f.do("DELETE", f.path(account), f.sess, nil); w.Code != 404 {
		t.Errorf("second delete: %d", w.Code)
	}
}

// Accounts sent with a new task are stored before it starts, sealed like any
// other. A refused account creates no task.
func TestTaskAuthzAccountsWithNewTask(t *testing.T) {
	f := newAuthzFixture(t)
	countTasks := func() (n int) {
		t.Helper()
		if err := f.s.m.pg.QueryRow(`SELECT count(*) FROM tasks`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	task := func(accounts ...map[string]any) map[string]any {
		return map[string]any{"description": "staging shop", "goal": "check access control", "authz_accounts": accounts}
	}

	before := countTasks()
	for name, tc := range map[string]struct {
		body map[string]any
		code int
	}{
		"same key twice":      {task(map[string]any{"role": "Admin"}, map[string]any{"role": "admin"}), 409},
		"no role":             {task(map[string]any{"username": "b@tenant-b.test"}), 400},
		"secret with a space": {task(map[string]any{"role": "Padded", "password": authzUserPassword + " "}), 400},
		"code review":         {map[string]any{"assessment_type": "code_review", "repository": "acme/example", "mode": "light", "authz_accounts": []map[string]any{{"role": "Admin"}}}, 400},
	} {
		if w := f.do("POST", "/api/tasks", f.sess, tc.body); w.Code != tc.code {
			t.Errorf("%s: %d, want %d: %s", name, w.Code, tc.code, w.Body.String())
		}
	}
	if after := countTasks(); after != before {
		t.Fatalf("a refused account left a task behind: %d tasks before, %d after", before, after)
	}

	w := f.do("POST", "/api/tasks", f.sess, task(
		map[string]any{"role": "Tenant B user", "username": "b@tenant-b.test", "password": authzUserPassword, "agent_access": true},
		map[string]any{"role": "Platform admin", "token": authzAdminBearer},
	))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	id := decodeBody(t, w)["id"].(string)
	t.Cleanup(func() { _, _ = f.s.m.DeleteTask(id, DeleteTaskOptions{}) })

	accounts := decodeBody(t, f.do("GET", "/api/tasks/"+id+"/authz", f.sess, nil))["accounts"].([]any)
	if len(accounts) != 2 {
		t.Fatalf("stored accounts: %v", accounts)
	}
	user, admin := accounts[0].(map[string]any), accounts[1].(map[string]any)
	if user["key"] != "tenant_b_user" || user["username"] != "b@tenant-b.test" || user["has_password"] != true || user["agent_access"] != true {
		t.Errorf("first account: %v", user)
	}
	if admin["key"] != "platform_admin" || admin["has_token"] != true || admin["agent_access"] != false {
		t.Errorf("second account: %v", admin)
	}
	var plain int
	if err := f.s.m.pg.QueryRow(`SELECT count(*) FROM task_authz_accounts WHERE task_id=$1 AND (COALESCE(password, 'cwenc:') NOT LIKE 'cwenc:%' OR COALESCE(token, 'cwenc:') NOT LIKE 'cwenc:%')`, id).Scan(&plain); err != nil || plain != 0 {
		t.Errorf("secrets stored in plaintext: %d rows, err %v", plain, err)
	}
}

// The console's matrix editor works on the server's reading of the saved
// policy. A text that is not a policy has none and is edited as text.
func TestTaskAuthzPolicyDocument(t *testing.T) {
	f := newAuthzFixture(t)
	f.addAccounts()
	if doc := decodeBody(t, f.do("GET", f.path(""), f.sess, nil))["document"]; doc != nil {
		t.Errorf("document before a policy is saved: %v", doc)
	}
	personas := func(body map[string]any) []any {
		doc, _ := body["document"].(map[string]any)
		list, _ := doc["personas"].([]any)
		return list
	}
	saved := decodeBody(t, f.do("PUT", f.path("/policy"), f.sess, map[string]any{"policy": f.policy()}))
	loaded := decodeBody(t, f.do("GET", f.path(""), f.sess, nil))
	for name, body := range map[string]map[string]any{"save": saved, "load": loaded} {
		list := personas(body)
		if len(list) != 3 || list[1].(map[string]any)["key"] != "tenant_b_user" {
			t.Fatalf("%s: personas of the document: %v", name, list)
		}
		json := list[1].(map[string]any)["login"].(map[string]any)["request"].(map[string]any)["json"].(map[string]any)
		if json["password"] != "${account:tenant_b_user.password}" {
			t.Errorf("%s: a reference did not stay a reference: %v", name, json)
		}
	}
	// A policy with problems still has a document: the editor must be able to fix it.
	broken := strings.Replace(f.policy(), "deny: 403", "allow: 200", 1) + "  - id: second\n    request: { method: POST, path: /api/x }\n    expect: {}\n"
	body := decodeBody(t, f.do("PUT", f.path("/policy"), f.sess, map[string]any{"policy": broken}))
	if body["check"].(map[string]any)["ok"] != false || len(personas(body)) != 3 {
		t.Errorf("policy with problems: %v", body)
	}
	body = decodeBody(t, f.do("PUT", f.path("/policy"), f.sess, map[string]any{"policy": "personas: [not, a, policy"}))
	if body["document"] != nil {
		t.Errorf("text that is not a policy has a document: %v", body["document"])
	}
}
