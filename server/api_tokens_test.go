package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func (f *prFixture) createToken(t *testing.T, sess string, scopes ...string) (secret string, id float64) {
	t.Helper()
	w := f.do("POST", "/api/api-tokens", sess, map[string]any{"name": "ci", "scopes": scopes})
	if w.Code != 201 {
		t.Fatalf("create token: %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	secret, _ = body["token"].(string)
	meta := body["api_token"].(map[string]any)
	id = meta["id"].(float64)
	t.Cleanup(func() { _, _ = f.s.m.pg.Exec(`DELETE FROM api_tokens WHERE id=$1`, int64(id)) })
	return secret, id
}

func TestAPITokenCreatedOnceAndStoredAsHash(t *testing.T) {
	f := newPRFixture(t)
	sess := f.session(t)
	secret, id := f.createToken(t, sess, "reviews:read", "reviews:write")
	if !strings.HasPrefix(secret, "cwt_") || !wellFormedAPIToken(secret) {
		t.Fatalf("secret shape: %q", secret)
	}
	var stored string
	if err := f.s.m.pg.QueryRow(`SELECT token_hash FROM api_tokens WHERE id=$1`, int64(id)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == secret || stored != hashAPIToken(secret) || strings.Contains(stored, "cwt_") {
		t.Fatalf("stored value is not the hash: %q", stored)
	}
	// Listing never returns the secret or its hash.
	w := f.do("GET", "/api/api-tokens", sess, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), stored) || !strings.Contains(w.Body.String(), `"hint":"cwt_`) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	for name, body := range map[string]map[string]any{
		"no scopes":   {"name": "x", "scopes": []string{}},
		"other scope": {"name": "x", "scopes": []string{"tasks:write"}},
		"no name":     {"name": "", "scopes": []string{"reviews:read"}},
		"past expiry": {"name": "x", "scopes": []string{"reviews:read"}, "expires_at": "2020-01-01T00:00:00Z"},
		"bad expiry":  {"name": "x", "scopes": []string{"reviews:read"}, "expires_at": "tomorrow"},
	} {
		if w := f.do("POST", "/api/api-tokens", sess, body); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
}

func TestAPITokenScopesAndEndpoints(t *testing.T) {
	f := newPRFixture(t)
	sess := f.session(t)
	apiTokenFailures = failedAttempts{}
	w := f.do("POST", "/api/reviews", sess, map[string]any{"repository": "acme/example", "pull_number": 7, "depth": "light"})
	if w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	id := decodeBody(t, w)["id"].(string)
	f.cleanupTask(t, id)

	readTok, readID := f.createToken(t, sess, "reviews:read")
	writeTok, _ := f.createToken(t, sess, "reviews:write")
	reviewBody := map[string]any{"repository": "acme/example", "pull_number": 7}

	// Every automation endpoint enforces its scope.
	type call struct {
		method, path, tok string
		body              any
		want              int
	}
	for _, c := range []call{
		{"POST", "/api/reviews", writeTok, reviewBody, 200},
		{"POST", "/api/reviews", readTok, reviewBody, 403},
		{"GET", "/api/reviews/" + id, readTok, nil, 200},
		{"GET", "/api/reviews/" + id, writeTok, nil, 403},
		{"GET", "/api/reviews/" + id + "/sarif", readTok, nil, 200},
		{"GET", "/api/reviews/" + id + "/sarif", writeTok, nil, 403},
		{"GET", "/api/reviews/" + id + "/summary", readTok, nil, 200},
		{"GET", "/api/reviews/" + id + "/summary", writeTok, nil, 403},
		// Everything else is closed to tokens, whatever the scope.
		{"GET", "/api/tasks", readTok, nil, 403},
		{"GET", "/api/tasks/" + id, writeTok, nil, 403},
		{"GET", "/api/api-tokens", readTok, nil, 403},
		{"POST", "/api/api-tokens", writeTok, map[string]any{"name": "x", "scopes": []string{"reviews:read"}}, 403},
		{"DELETE", "/api/api-tokens/1", writeTok, nil, 403},
		{"GET", "/api/llm/profiles", readTok, nil, 403},
		{"GET", "/api/reviews/" + id + "/extra", readTok, nil, 403},
		{"GET", "/api/reviews/" + id + "/", readTok, nil, 403},
		{"POST", "/api/reviews/" + id, writeTok, nil, 403},
		{"POST", "/api/auth/change-password", readTok, map[string]any{"old_password": "a", "new_password": "bbbbbbbbb"}, 401},
	} {
		if w := f.do(c.method, c.path, c.tok, c.body); w.Code != c.want {
			t.Errorf("%s %s: %d want %d: %s", c.method, c.path, w.Code, c.want, w.Body.String())
		}
	}

	// A token never becomes a session: it is not a JWT for the session-only routes.
	if w := f.do("POST", "/api/auth/logout", readTok, nil); w.Code == 200 {
		t.Error("a token signed out a session")
	}

	// Session tokens still reach the same endpoints.
	if w := f.do("GET", "/api/reviews/"+id, sess, nil); w.Code != 200 {
		t.Errorf("session: %d", w.Code)
	}

	// Last use is recorded once, then left alone inside the interval.
	var first, second *time.Time
	read := func() *time.Time {
		var ts *time.Time
		if err := f.s.m.pg.QueryRow(`SELECT last_used_at FROM api_tokens WHERE id=$1`, int64(readID)).Scan(&ts); err != nil {
			t.Fatal(err)
		}
		return ts
	}
	first = read()
	time.Sleep(20 * time.Millisecond)
	f.do("GET", "/api/reviews/"+id, readTok, nil)
	second = read()
	if first == nil || !first.Equal(*second) {
		t.Fatalf("last_used_at rewritten on a repeat request: %v -> %v", first, second)
	}

	// Revocation and expiry end access at once.
	if w := f.do("DELETE", "/api/api-tokens/"+strconv.FormatInt(int64(readID), 10), sess, nil); w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	if w := f.do("GET", "/api/reviews/"+id, readTok, nil); w.Code != 401 {
		t.Errorf("revoked token: %d", w.Code)
	}
	if w := f.do("DELETE", "/api/api-tokens/999999999", sess, nil); w.Code != 404 {
		t.Errorf("unknown revoke: %d", w.Code)
	}
	if _, err := f.s.m.pg.Exec(`UPDATE api_tokens SET expires_at=now() - interval '1 minute' WHERE token_hash=$1`, hashAPIToken(writeTok)); err != nil {
		t.Fatal(err)
	}
	if w := f.do("POST", "/api/reviews", writeTok, reviewBody); w.Code != 401 {
		t.Errorf("expired token: %d", w.Code)
	}
}

func TestAPITokenFailuresAreRateLimited(t *testing.T) {
	f := newPRFixture(t)
	apiTokenFailures = failedAttempts{}
	t.Cleanup(func() { apiTokenFailures = failedAttempts{} })
	bad := "cwt_" + strings.Repeat("A", apiTokenSecretLen)
	var last int
	for i := 0; i < 8; i++ {
		r := httptest.NewRequest(http.MethodGet, "/api/reviews/1", nil)
		r.RemoteAddr = "192.0.2.10:5000"
		r.Header.Set("Authorization", "Bearer "+bad)
		w := httptest.NewRecorder()
		f.s.Handler().ServeHTTP(w, r)
		last = w.Code
		if i < 5 && w.Code != 401 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after the burst: %d", last)
	}
	// Another peer is unaffected, and malformed tokens fail without a lookup.
	r := httptest.NewRequest(http.MethodGet, "/api/reviews/1", nil)
	r.RemoteAddr = "198.51.100.11:5000"
	r.Header.Set("Authorization", "Bearer cwt_short")
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("other peer: %d", w.Code)
	}
}

func TestValidAPITokenIsServedWhileItsPeerIsBlocked(t *testing.T) {
	f := newPRFixture(t)
	sess := f.session(t)
	apiTokenFailures = failedAttempts{}
	t.Cleanup(func() { apiTokenFailures = failedAttempts{} })
	good, _ := f.createToken(t, sess, "reviews:read")
	call := func(tok string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/reviews/1", nil)
		r.RemoteAddr = "192.0.2.10:5000"
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		f.s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	bad := "cwt_" + strings.Repeat("A", apiTokenSecretLen)
	for i := 0; i < 8; i++ {
		call(bad)
	}
	if got := call(bad); got != http.StatusTooManyRequests {
		t.Fatalf("invalid token from a blocked peer: %d", got)
	}
	if got := call("cwt_short"); got != http.StatusTooManyRequests {
		t.Fatalf("malformed token from a blocked peer: %d", got)
	}
	if got := call(good); got == http.StatusTooManyRequests || got == http.StatusUnauthorized {
		t.Fatalf("valid token refused from a blocked peer: %d", got)
	}
}
