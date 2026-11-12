package authz

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// taskPolicy is a policy as it is written inside a task: the address is
// literal and the credentials come from the task's test accounts.
const taskPolicy = `
version: 1
target: { base_url: "BASE", allow_plain_http: true }
personas:
  anonymous: {}
  user_a:
    login:
      request: { method: POST, path: /api/login, json: { email: "${account:user_a.username}", password: "${account:user_a.password}" } }
      token: { json_pointer: /token, prefix: "Bearer " }
    identity: { request: { method: GET, path: /api/me }, body_contains: ["${account:user_a.username}"] }
  admin:
    role: Platform administrator
    headers: { Authorization: "Bearer ${account:admin.token}" }
    identity: { request: { method: GET, path: /api/me }, body_contains: ['"role":"admin"'] }
cases:
  - id: admin-users
    request: { method: GET, path: /api/admin/users }
    expect: { anonymous: { deny: 401 }, user_a: { deny: 403 }, admin: { allow: 200 } }
`

func taskAccounts() map[string]Account {
	return map[string]Account{
		"user_a": {Role: "Tenant A member", Username: "a@tenant-a.test", Password: pwA},
		"admin":  {Role: "Ignored: the policy names the role", Token: adminToken},
	}
}

// runTask runs taskPolicy the way a server does: accounts, and no environment.
func runTask(t *testing.T, f *fixture, policy string, opt Options) (*Report, *int32) {
	t.Helper()
	return runTaskContext(t, context.Background(), f, policy, opt)
}

func runTaskContext(t *testing.T, ctx context.Context, f *fixture, policy string, opt Options) (*Report, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		f.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	p, err := Parse([]byte(strings.Replace(policy, "BASE", srv.URL, 1)))
	if err != nil {
		t.Fatalf("policy does not validate:\n%v", err)
	}
	opt.NoEnv = true
	opt.Getenv = func(string) string { t.Error("the environment was read"); return "leaked" }
	rep := Run(ctx, p, opt)
	assertNoSecrets(t, rep)
	return rep, &hits
}

func TestTaskAccountsAndRoleNames(t *testing.T) {
	rep, _ := runTask(t, &fixture{adminOpen: true}, taskPolicy, Options{Accounts: taskAccounts()})
	wantExit(t, rep, 1, Fail)
	roles := map[string]string{}
	for _, p := range rep.Personas {
		roles[p.Name] = p.Role
	}
	if roles["anonymous"] != "" || roles["user_a"] != "Tenant A member" || roles["admin"] != "Platform administrator" {
		t.Errorf("roles = %v", roles)
	}
	var text bytes.Buffer
	rep.WriteText(&text)
	for _, want := range []string{"Tenant A member (user_a) (identity verified)", "Platform administrator (admin)", "FAIL as Tenant A member (user_a): expected a denial"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text output lacks %q:\n%s", want, text.String())
		}
	}
	// The username names the account in the output; it is not removed like a secret.
	policy := strings.Replace(taskPolicy, `body_contains: ["${account:user_a.username}"]`, `body_contains: ["${account:user_a.username}-nobody"]`, 1)
	rep, _ = runTask(t, &fixture{}, policy, Options{Accounts: taskAccounts()})
	wantExit(t, rep, 2, ResultError)
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "a@tenant-a.test-nobody") {
		t.Errorf("errors = %v", rep.Errors)
	}
}

func TestTaskReferenceProblemsSendNothing(t *testing.T) {
	withEnv := strings.Replace(taskPolicy, "${account:admin.token}", "${env:PATH}", 1)
	noPassword := taskAccounts()
	noPassword["user_a"] = Account{Role: "Tenant A member", Username: "a@tenant-a.test"}
	padded := taskAccounts()
	padded["admin"] = Account{Token: adminToken + "\n"}
	for _, tc := range []struct {
		name     string
		policy   string
		accounts map[string]Account
		want     string
	}{
		{"server environment", withEnv, taskAccounts(), "cannot read the server's environment (PATH)"},
		{"account not defined", taskPolicy, map[string]Account{"user_a": taskAccounts()["user_a"]}, "test accounts not defined in this task: admin"},
		{"field not set", taskPolicy, noPassword, "test account fields not set: user_a.password"},
		{"secret with a trailing newline", taskPolicy, padded, "whitespace or a control character (often a trailing newline in the stored secret): admin.token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep, hits := runTask(t, &fixture{}, tc.policy, Options{Accounts: tc.accounts})
			wantExit(t, rep, 2, ResultError)
			if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], tc.want) || atomic.LoadInt32(hits) != 0 {
				t.Errorf("errors = %v, requests sent = %d", rep.Errors, atomic.LoadInt32(hits))
			}
		})
	}

	// On the command line there are no accounts, and the message says so.
	p, err := Parse([]byte(strings.Replace(taskPolicy, "BASE", "http://127.0.0.1:9", 1)))
	if err != nil {
		t.Fatal(err)
	}
	rep := Run(context.Background(), p, Options{Getenv: func(string) string { return "" }})
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "exist only when the policy runs inside a Counterweight task") {
		t.Errorf("errors = %v", rep.Errors)
	}
	// The references a server has to satisfy before it starts a run.
	var refs []string
	for _, r := range p.Refs() {
		refs = append(refs, r.Env+r.Account+"."+r.Field)
	}
	if got := strings.Join(refs, " "); got != "admin.token user_a.password user_a.username" {
		t.Errorf("refs = %s", got)
	}
}

func TestDestinationChecksOfTheServer(t *testing.T) {
	refused := errors.New("destination blocked by asset policy")
	// The URL check refuses before anything is sent.
	rep, hits := runTask(t, &fixture{}, taskPolicy, Options{Accounts: taskAccounts(), CheckURL: func(u *url.URL) error {
		if u.Path == "" || u.Host == "" {
			t.Errorf("the check must see the full destination, got %q", u)
		}
		return refused
	}})
	wantExit(t, rep, 2, ResultError)
	if atomic.LoadInt32(hits) != 0 || len(rep.Errors) != 2 || !strings.Contains(rep.Errors[0], "blocked by asset policy") {
		t.Errorf("errors = %v, requests sent = %d", rep.Errors, atomic.LoadInt32(hits))
	}
	// The peer check sees the address a connection would really go to.
	var seen []string
	rep, hits = runTask(t, &fixture{}, taskPolicy, Options{Accounts: taskAccounts(), CheckPeer: func(ip net.IP) error {
		seen = append(seen, ip.String())
		return refused
	}})
	wantExit(t, rep, 2, ResultError)
	if atomic.LoadInt32(hits) != 0 || len(seen) == 0 || seen[0] != "127.0.0.1" {
		t.Errorf("peers seen = %v, requests sent = %d, errors = %v", seen, atomic.LoadInt32(hits), rep.Errors)
	}
	// Both allow: the run goes through.
	rep, _ = runTask(t, &fixture{}, taskPolicy, Options{Accounts: taskAccounts(), CheckURL: func(*url.URL) error { return nil }, CheckPeer: func(net.IP) error { return nil }})
	wantExit(t, rep, 0, Pass)
}
