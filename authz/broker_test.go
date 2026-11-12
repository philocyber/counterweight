package authz

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestBroker(t *testing.T, f *fixture, accounts map[string]Account) (*Broker, *int32) {
	t.Helper()
	var logins int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login":
			atomic.AddInt32(&logins, 1)
		case "/api/echo64": // repeats the credential header encoded, and in a header of its own
			w.Header().Set("Content-Type", "text/plain; received="+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(r.Header.Get("Authorization"))) + " " + hex.EncodeToString([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))))
			return
		case "/api/range": // reflects the credential and honours byte ranges, like a static file server
			page := "token=" + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") + " enc=" + r.Header.Get("Accept-Encoding")
			http.ServeContent(w, r, "page.txt", time.Time{}, strings.NewReader(page))
			return
		case "/api/partial": // a slice nobody asked for
			w.Header().Set("Content-Range", "bytes 0-3/40")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")[:4]))
			return
		case "/api/refresh": // issues a new session the broker has never seen
			_, _ = w.Write([]byte(`{"access_token":"fixture-fresh-session-value","user":"a@tenant-a.test"}`))
			return
		}
		f.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	p, err := Parse([]byte(strings.Replace(taskPolicy, "BASE", srv.URL, 1)))
	if err != nil {
		t.Fatal(err)
	}
	// The extra scrubber stands in for the server's masking of credential shapes.
	maskTokens := regexp.MustCompile(`("access_token":")[^"]*`)
	b, err := NewBroker(p, Options{Accounts: accounts, NoEnv: true, Redact: func(s string) string { return maskTokens.ReplaceAllString(s, "${1}[MASKED]") }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b, &logins
}

func TestBrokerSendsAsAPersonaAndKeepsTheCredentials(t *testing.T) {
	b, logins := newTestBroker(t, &fixture{}, taskAccounts())
	if got := strings.Join(b.Personas(), ","); got != "anonymous,user_a,admin" {
		t.Fatalf("personas = %s", got)
	}
	if b.Role("user_a") != "Tenant A member" || b.Role("admin") != "Platform administrator" || b.Role("anonymous") != "" {
		t.Errorf("roles: %q %q %q", b.Role("user_a"), b.Role("admin"), b.Role("anonymous"))
	}
	if got := strings.Join(b.PersonaAccounts("user_a"), ","); got != "user_a" || len(b.PersonaAccounts("anonymous")) != 0 {
		t.Errorf("accounts behind user_a = %q", got)
	}
	ctx := context.Background()

	// The same request as three personas gives three different answers.
	for persona, status := range map[string]int{"anonymous": 401, "user_a": 403, "admin": 200} {
		a, err := b.Do(ctx, persona, Request{Method: "GET", Path: "/api/admin/users"})
		if err != nil || a.Status != status {
			t.Errorf("%s: status %v, want %d (%v)", persona, a, status, err)
		}
	}
	// A persona signs in once, not on every request.
	if _, err := b.Do(ctx, "user_a", Request{Method: "GET", Path: "/api/me"}); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(logins); n != 1 {
		t.Errorf("user_a signed in %d times for two requests", n)
	}

	// A target that repeats the credentials it received does not hand them to the caller.
	a, err := b.Do(ctx, "user_a", Request{Method: "GET", Path: "/api/echo"})
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(a)
	for _, secret := range fixtureSecrets {
		if strings.Contains(string(js), secret) {
			t.Errorf("the answer holds the credential %q: %s", secret, js)
		}
	}
	if a.Status != 403 || !strings.Contains(a.Body, "[REDACTED]") {
		t.Errorf("echo answer = %s", js)
	}
}

func TestBrokerRefusesWhatWouldMoveCredentials(t *testing.T) {
	var elsewhere int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { atomic.AddInt32(&elsewhere, 1) }))
	defer other.Close()
	f := &fixture{elsewhere: other.URL + "/landing"}
	b, _ := newTestBroker(t, f, taskAccounts())
	ctx := context.Background()

	for name, q := range map[string]Request{
		"authorization header":  {Method: "GET", Path: "/api/me", Headers: map[string]string{"Authorization": "Bearer x"}},
		"cookie header":         {Method: "GET", Path: "/api/me", Headers: map[string]string{"Cookie": "sid=x"}},
		"host header":           {Method: "GET", Path: "/api/me", Headers: map[string]string{"Host": "other.example"}},
		"absolute URL":          {Method: "GET", Path: other.URL + "/x"},
		"protocol-relative URL": {Method: "GET", Path: "//" + strings.TrimPrefix(other.URL, "http://") + "/x"},
		"method":                {Method: "TRACE", Path: "/api/me"},
	} {
		if a, err := b.Do(ctx, "admin", q); err == nil {
			t.Errorf("%s was sent: %+v", name, a)
		}
	}
	if _, err := b.Do(ctx, "nobody", Request{Method: "GET", Path: "/api/me"}); err == nil {
		t.Error("an unknown persona was accepted")
	}
	// A redirect to another host is returned, not followed.
	a, err := b.Do(ctx, "admin", Request{Method: "GET", Path: "/api/redirect"})
	if err != nil || a.Status != 302 || !strings.HasPrefix(a.Location, other.URL) {
		t.Errorf("redirect: %+v %v", a, err)
	}
	if n := atomic.LoadInt32(&elsewhere); n != 0 {
		t.Errorf("another host received %d requests", n)
	}

	// A credential that is wrong stops the persona at sign-in; the error names no secret.
	wrong := taskAccounts()
	wrong["admin"] = Account{Token: "fixture-expired-credential"}
	b, _ = newTestBroker(t, &fixture{}, wrong)
	if _, err := b.Do(ctx, "admin", Request{Method: "GET", Path: "/api/admin/users"}); err == nil || strings.Contains(err.Error(), "fixture-expired") || !strings.Contains(err.Error(), "identity check") {
		t.Errorf("wrong credential: %v", err)
	}
	// The other personas of the same broker still work.
	if a, err := b.Do(ctx, "user_a", Request{Method: "GET", Path: "/api/me"}); err != nil || a.Status != 200 {
		t.Errorf("user_a after admin failed: %+v %v", a, err)
	}
}

func TestBrokerAnswerHidesEncodedAndNewCredentials(t *testing.T) {
	b, _ := newTestBroker(t, &fixture{}, taskAccounts())
	ctx := context.Background()
	for _, persona := range []string{"user_a", "admin"} {
		a, err := b.Do(ctx, persona, Request{Method: "GET", Path: "/api/echo64"})
		if err != nil {
			t.Fatal(err)
		}
		js, _ := json.Marshal(a)
		raw, _ := base64.StdEncoding.DecodeString(strings.Fields(a.Body)[0])
		for _, secret := range fixtureSecrets {
			if strings.Contains(string(js), secret) || strings.Contains(string(raw), secret) || strings.Contains(string(js), hex.EncodeToString([]byte(secret))) {
				t.Errorf("%s: the answer gives the credential %q back in an encoded form: %s", persona, secret, js)
			}
		}
		// The content type keeps its media type and loses the text the target put in it.
		if a.ContentType != "text/plain" {
			t.Errorf("%s: content type = %q", persona, a.ContentType)
		}
	}
	// A session the target issues during a request is masked by shape.
	a, err := b.Do(ctx, "user_a", Request{Method: "POST", Path: "/api/refresh"})
	if err != nil || strings.Contains(a.Body, "fixture-fresh-session-value") || !strings.Contains(a.Body, "[MASKED]") || !strings.Contains(a.Body, "a@tenant-a.test") {
		t.Errorf("refresh answer: %+v %v", a, err)
	}
}

func TestBrokerIsRevokedByClose(t *testing.T) {
	b, logins := newTestBroker(t, &fixture{}, taskAccounts())
	ctx := context.Background()
	if _, err := b.Do(ctx, "admin", Request{Method: "GET", Path: "/api/me"}); err != nil {
		t.Fatal(err)
	}
	b.Close()
	for _, persona := range []string{"admin", "user_a", "anonymous"} {
		if a, err := b.Do(ctx, persona, Request{Method: "GET", Path: "/api/me"}); !errors.Is(err, ErrBrokerClosed) {
			t.Errorf("%s after Close: %+v %v", persona, a, err)
		}
	}
	if n := atomic.LoadInt32(logins); n != 0 {
		t.Errorf("a closed broker signed in %d times", n)
	}
}

func TestBrokerAnswerIsBounded(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 3*maxAnswerBody)))
	}))
	defer big.Close()
	p, err := Parse([]byte("version: 1\ntarget: { base_url: \"" + big.URL + "\", allow_plain_http: true }\npersonas: { anonymous: {} }\ncases:\n  - id: c\n    request: { method: GET, path: / }\n    expect: { anonymous: { allow: 200 } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBroker(p, Options{NoEnv: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	a, err := b.Do(context.Background(), "anonymous", Request{Method: "GET", Path: "/"})
	if err != nil || len(a.Body) != maxAnswerBody || !a.Truncated {
		t.Errorf("body %d bytes, truncated %v, %v", len(a.Body), a.Truncated, err)
	}
}

// An account counts as used by a persona wherever the persona sends one of
// its fields, also in the request of the identity check.
func TestPersonaAccountsIncludesTheIdentityRequest(t *testing.T) {
	policy := strings.Replace(taskPolicy, "BASE", "https://staging.example.com", 1)
	policy = strings.Replace(policy, "identity: { request: { method: GET, path: /api/me }, body_contains: [\"${account:user_a.username}\"] }",
		"identity: { request: { method: GET, path: \"/api/me?as=${account:admin.token}\" }, body_contains: [\"${account:other.username}\"] }", 1)
	p, err := Parse([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.PersonaAccounts("user_a"), ","); got != "admin,user_a" {
		t.Errorf("accounts behind user_a = %q, want admin,user_a (an expected text is not sent, so other is not one)", got)
	}
	if got := p.PersonaAccounts("anonymous"); len(got) != 0 {
		t.Errorf("accounts behind anonymous = %v", got)
	}
}

func TestBrokerDoesNotReturnPiecesOfTheCredential(t *testing.T) {
	b, _ := newTestBroker(t, &fixture{}, taskAccounts())
	ctx := context.Background()

	// Headers that choose a part or an encoding of the answer are refused.
	for _, h := range []string{"Range", "range", "If-Range", "Accept-Encoding", "If-None-Match", "TE", "Connection", "Content-Length"} {
		if a, err := b.Do(ctx, "user_a", Request{Method: "GET", Path: "/api/range", Headers: map[string]string{h: "bytes=0-3"}}); err == nil {
			t.Errorf("header %s was sent: %+v", h, a)
		}
	}
	// A whole answer asks the target for an unencoded body, and an allowed header still goes through.
	a, err := b.Do(ctx, "user_a", Request{Method: "GET", Path: "/api/range", Headers: map[string]string{"X-Probe": "1", "Accept": "text/plain"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != 200 || !strings.Contains(a.Body, "enc=identity") {
		t.Errorf("answer = %+v", a)
	}
	for _, secret := range fixtureSecrets {
		if strings.Contains(a.Body, secret) {
			t.Errorf("the answer holds the credential %q", secret)
		}
	}
	// A target that answers with a slice on its own is not passed on.
	if a, err := b.Do(ctx, "user_a", Request{Method: "GET", Path: "/api/partial"}); err == nil {
		t.Errorf("a partial answer was returned: %+v", a)
	}
}
