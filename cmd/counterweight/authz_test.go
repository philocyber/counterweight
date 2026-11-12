package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/authz"
)

const authzTestToken = "cli-test-admin-credential"

// authzTestPolicy checks one admin route of the server started by runAuthz.
const authzTestPolicy = `version: 1
target: { base_url: "${env:TARGET}", allow_plain_http: true }
personas:
  anonymous: {}
  admin:
    headers: { Authorization: "Bearer ${env:ADMIN_TOKEN}" }
    identity: { request: { method: GET, path: /me }, body_contains: [admin] }
cases:
  - id: admin-area
    request: { method: GET, path: /admin }
    expect:
      anonymous: { deny: 401 }
      admin: { allow: 200 }
`

// runAuthz runs `counterweight authz <args...> <policy file>` against a server
// whose admin route is open to everyone when exposed is set.
func runAuthz(t *testing.T, exposed bool, token string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		admin := r.Header.Get("Authorization") == "Bearer "+authzTestToken
		switch {
		case r.URL.Path == "/admin" && exposed:
			// A leaked token of another user, in a shape the generic redaction knows.
			w.Write([]byte(`{"users":[{"name":"root","token":"other-users-token"}]}`))
		case !admin:
			http.Error(w, "sign in first", http.StatusUnauthorized)
		default:
			w.Write([]byte(`{"role":"admin"}`))
		}
	}))
	defer srv.Close()
	policy := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(policy, []byte(authzTestPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"TARGET": srv.URL, "ADMIN_TOKEN": token}
	var so, se bytes.Buffer
	code = authzCommand(append(args, policy), func(k string) string { return env[k] }, &so, &se)
	return code, so.String(), se.String()
}

func TestAuthzRunExitCodes(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name    string
		exposed bool
		token   string
		code    int
		result  string
	}{
		{"pass", false, authzTestToken, 0, authz.Pass},
		{"violation", true, authzTestToken, 1, authz.Fail},
		{"expired credential", false, "expired-token-value", 2, authz.ResultError},
		{"credential not set", false, "", 2, authz.ResultError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".json")
			code, stdout, stderr := runAuthz(t, tc.exposed, tc.token, "run", "-o", out)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\n%s%s", code, tc.code, stdout, stderr)
			}
			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			var rep authz.Report
			if err := json.Unmarshal(raw, &rep); err != nil {
				t.Fatal(err)
			}
			if rep.Schema != authz.ReportSchema || rep.Result != tc.result || rep.ExitCode != tc.code {
				t.Errorf("report = %s", raw)
			}
			if !strings.Contains(stdout, "Result: "+strings.ToUpper(tc.result)) {
				t.Errorf("stdout = %s", stdout)
			}
			for _, secret := range []string{authzTestToken, "expired-token-value", "other-users-token"} {
				if strings.Contains(string(raw)+stdout+stderr, secret) {
					t.Errorf("%q is in the output:\n%s\n%s", secret, raw, stdout)
				}
			}
		})
	}
}

func TestAuthzValidateListsEnvironment(t *testing.T) {
	code, stdout, stderr := runAuthz(t, false, "", "validate")
	if code != 0 || !strings.Contains(stdout, "OK: 2 personas, 1 cases, 2 checks.") ||
		!strings.Contains(stdout, "ADMIN_TOKEN (credential)") || !strings.Contains(stdout, "  TARGET\n") {
		t.Errorf("exit %d\n%s%s", code, stdout, stderr)
	}
}

func TestAuthzRejectsInvalidPolicyAndUsage(t *testing.T) {
	policy := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(policy, []byte(strings.Replace(authzTestPolicy, "admin: { allow: 200 }", "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"run", policy},
		{"validate", policy},
		{"run", filepath.Join(t.TempDir(), "missing.yaml")},
		{"run"},
		{"validate"},
		{"run", "-unknown-flag", policy},
		{"frobnicate"},
		{},
	} {
		var so, se bytes.Buffer
		if code := authzCommand(args, func(string) string { return "" }, &so, &se); code != 2 || se.Len() == 0 {
			t.Errorf("authz %v: exit %d, stderr %q", args, code, se.String())
		}
	}
	var so, se bytes.Buffer
	if code := authzCommand([]string{"help"}, nil, &so, &se); code != 0 || !strings.Contains(so.String(), "Exit status of run") {
		t.Errorf("help: exit %d, %q", code, so.String())
	}
	if !strings.Contains(se.String()+so.String(), "authz") {
		t.Error("usage does not mention the command")
	}
}
