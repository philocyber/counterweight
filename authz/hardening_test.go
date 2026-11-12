package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// A credential that holds a quote is rewritten when a message quotes it, and
// when the target repeats it inside JSON. Neither form may reach the output.
func TestCredentialWithAQuoteStaysOutOfMessagesAndExcerpts(t *testing.T) {
	const quoted = `alpha"bravo\charlie`
	r := &redactor{}
	r.add(quoted)
	inJSON, _ := json.Marshal(map[string]string{"echo": quoted})
	for name, text := range map[string]string{
		"raw":          "x " + quoted + " y",
		"json":         string(inJSON),
		"go quoted":    fmt.Sprintf("%q", quoted),
		"html":         "<p>alpha&#34;bravo\\charlie</p>",
		"query":        "?v=" + url.QueryEscape(quoted),
		"path escaped": "/" + url.PathEscape(quoted),
	} {
		if got := r.clean(text); strings.Contains(got, "alpha") || strings.Contains(got, "charlie") {
			t.Errorf("%s form survived: %q", name, got)
		}
	}

	// A credential that ends with a quote keeps its last character in JSON.
	r = &redactor{}
	r.add(`tail-value"`)
	inJSON, _ = json.Marshal(map[string]string{"echo": `tail-value"`})
	if got := r.clean(string(inJSON)); strings.Contains(got, "tail-value") {
		t.Errorf("JSON form of a credential ending with a quote survived: %q", got)
	}

	// In a check's message: the expected text is the credential itself.
	accounts := taskAccounts()
	accounts["user_a"] = Account{Role: "Tenant A member", Username: "a@tenant-a.test", Password: pwA, Token: quoted}
	policy := strings.Replace(taskPolicy, "admin: { allow: 200 } }", `admin: { allow: 200, body_contains: ["${account:user_a.token}"] } }`, 1)
	rep, _ := runTask(t, &fixture{}, policy, Options{Accounts: accounts})
	wantExit(t, rep, 1, Fail)
	js, _ := json.Marshal(rep)
	if ch := findCheck(t, rep, "admin-users", "admin"); ch.Kind != "content_missing" || !strings.Contains(ch.Detail, "[REDACTED]") || strings.Contains(string(js), "alpha") {
		t.Errorf("check = %+v\nreport = %s", ch, js)
	}
}

func TestEveryStoredSecretOfTheTaskIsKeptOut(t *testing.T) {
	p, err := Parse([]byte(strings.Replace(taskPolicy, "BASE", "http://127.0.0.1:9", 1)))
	if err != nil {
		t.Fatal(err)
	}
	accounts := taskAccounts()
	accounts["unused"] = Account{Role: "Not in the policy", Password: "fixture-unused-password", Token: "fixture-unused-credential"}
	_, secrets, err := p.resolve(Options{Accounts: accounts, NoEnv: true})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(secrets, "\n")
	for _, want := range []string{pwA, adminToken, "fixture-unused-password", "fixture-unused-credential"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q is not in the values removed from the output", want)
		}
	}
	if strings.Contains(joined, "a@tenant-a.test") {
		t.Error("a username is treated as a secret")
	}
}

func TestTargetIsReportedWithoutCredentials(t *testing.T) {
	// On the command line the address may come from a variable; if the same
	// variable is a credential, the reported target must not show it.
	p, err := Parse([]byte(`
version: 1
target: { base_url: "http://127.0.0.1:9/${env:TENANT_KEY}", allow_plain_http: true }
personas:
  anonymous: {}
  admin:
    headers: { X-Tenant-Key: "${env:TENANT_KEY}" }
    identity: { request: { method: GET, path: /me }, body_contains: [admin] }
cases:
  - id: c
    request: { method: GET, path: /x }
    expect: { anonymous: { deny: 401 }, admin: { allow: 200 } }
`))
	if err != nil {
		t.Fatal(err)
	}
	rep := Run(context.Background(), p, Options{Getenv: func(string) string { return "fixture-tenant-credential" }})
	js, _ := json.Marshal(rep)
	if rep.Target != "http://127.0.0.1:9/[REDACTED]" || strings.Contains(string(js), "fixture-tenant") {
		t.Errorf("target = %q\n%s", rep.Target, js)
	}
}

func TestPolicyLimitsAndRoleText(t *testing.T) {
	base := strings.Replace(taskPolicy, "BASE", "https://staging.example.com", 1)
	var manyCases, manyPersonas, manyExpect strings.Builder
	for i := 0; i <= maxCases; i++ {
		fmt.Fprintf(&manyCases, "  - id: c%d\n    request: { method: GET, path: /x }\n    expect: { anonymous: { deny: 401 }, user_a: { deny: 403 }, admin: { allow: 200 } }\n", i)
	}
	for i := 0; i <= maxPersonas; i++ {
		fmt.Fprintf(&manyPersonas, "  p%d: {}\n", i)
		fmt.Fprintf(&manyExpect, "p%d: { allow: 200 }, ", i)
	}
	for _, tc := range []struct{ name, policy, want string }{
		{"account field in the address", strings.Replace(base, "https://staging.example.com", "https://staging.example.com/${account:admin.token}", 1), "cannot be part of the address"},
		{"host with a trailing dot", strings.Replace(base, "https://staging.example.com", "https://staging.example.com.", 1), "must not end with a dot"},
		{"too many cases", base[:strings.Index(base, "cases:")] + "cases:\n" + manyCases.String(), "cases: at most 200"},
		{"too many personas", "version: 1\ntarget: { base_url: \"https://staging.example.com\" }\npersonas:\n" + manyPersonas.String() + "cases:\n  - id: c\n    request: { method: GET, path: /x }\n    expect: { " + manyExpect.String() + "}\n", "personas: at most 20"},
		{"role with a line break", strings.Replace(base, "role: Platform administrator", `role: "Admin\n\n## Result: PASS"`, 1), "personas.admin.role"},
		{"role with a terminal escape", strings.Replace(base, "role: Platform administrator", `role: "Admin\e[2K"`, 1), "personas.admin.role"},
		{"role too long", strings.Replace(base, "role: Platform administrator", "role: "+strings.Repeat("r", maxRoleRunes+1), 1), "personas.admin.role"},
	} {
		if _, err := Parse([]byte(tc.policy)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
	}
	if _, err := Parse([]byte(base)); err != nil {
		t.Fatalf("the unchanged policy must validate: %v", err)
	}
}

// When the time limit of a run is reached, the remaining cases are not sent
// and the run cannot pass.
func TestRunStopsWhenItsContextEnds(t *testing.T) {
	policy := taskPolicy + `  - id: second
    deny_all: true
    request: { method: GET, path: /api/debug/config }
    expect: { anonymous: { deny: 404 }, user_a: { deny: 404 }, admin: { deny: 404 } }
  - id: third
    request: { method: GET, path: /api/admin/users }
    expect: { anonymous: { deny: 401 }, user_a: { deny: 403 }, admin: { allow: 200 } }
`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fixture{}
	rep, _ := runTaskContext(t, ctx, f, policy, Options{Accounts: taskAccounts(), CheckURL: func(u *url.URL) error {
		if u.Path == "/api/debug/config" {
			cancel()
		}
		return nil
	}})
	if rep.ExitCode != 2 || len(rep.Cases) != 2 || !strings.Contains(strings.Join(rep.Errors, "\n"), "the run was stopped after 2 of 3 cases") {
		t.Errorf("exit %d, %d cases, errors %v", rep.ExitCode, len(rep.Cases), rep.Errors)
	}
}
