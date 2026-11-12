package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
	"golang.org/x/crypto/bcrypt"
)

const sessionFixturePassword = "session fixture password"

// sessionFixture returns a server backed by the disposable test database with a
// known administrator password. Prior credential state is restored afterwards.
func sessionFixture(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("COUNTERWEIGHT_PG_DSN")
	if !strings.Contains(dsn, "_test?") {
		t.Skip("requires a disposable *_test database")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	prior, existed, err := pg.GetSetting(authPassKey)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(sessionFixturePassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := pg.SetSetting(authPassKey, string(hash)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = pg.SetSetting(authPassKey, prior)
		} else {
			_, _ = pg.Exec(`DELETE FROM settings WHERE key=$1`, authPassKey)
		}
		_, _ = pg.Exec(`DELETE FROM settings WHERE key=$1`, legacyRevokedKey)
		_, _ = pg.Exec(`DELETE FROM auth_sessions`)
	})
	_, _ = pg.Exec(`DELETE FROM settings WHERE key=$1`, legacyRevokedKey)
	_, _ = pg.Exec(`DELETE FROM auth_sessions`)
	return &Server{m: &Manager{pg: pg}, jwtKey: []byte(strings.Repeat("s", 32))}
}

func sessionLogin(t *testing.T, s *Server) string {
	t.Helper()
	return revocationLogin(t, s, sessionFixturePassword)
}

// protectedStatus calls a normal API route through the real handler chain.
func protectedStatus(s *Server, target, token string) int {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(w, r)
	return w.Code
}

func sessionCount(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.m.pg.QueryRow(`SELECT count(*) FROM auth_sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mintTicket(t *testing.T, s *Server, token string) string {
	t.Helper()
	return mintTicketFor(t, s, token, "/api/logs/stream")
}

func mintTicketFor(t *testing.T, s *Server, token, path string) string {
	t.Helper()
	w := revocationPost(t, s, "/api/auth/stream-ticket", map[string]string{"path": path}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("stream-ticket status=%d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Ticket == "" || out.ExpiresIn != 60 {
		t.Fatalf("bad ticket response %s: %v", w.Body.String(), err)
	}
	return out.Ticket
}

func TestSessionLogoutRevokesOnlyCallerSession(t *testing.T) {
	s := sessionFixture(t)
	first, second := sessionLogin(t, s), sessionLogin(t, s)
	if sessionCount(t, s) != 2 {
		t.Fatalf("want 2 stored sessions, got %d", sessionCount(t, s))
	}
	if got := protectedStatus(s, "/api/protected", first); got != http.StatusNoContent {
		t.Fatalf("fresh token status=%d", got)
	}
	if w := revocationPost(t, s, "/api/auth/logout", map[string]string{}, first); w.Code != http.StatusOK {
		t.Fatalf("logout status=%d: %s", w.Code, w.Body.String())
	}
	if got := protectedStatus(s, "/api/protected", first); got != http.StatusUnauthorized {
		t.Fatalf("revoked token accepted on the next request: %d", got)
	}
	if w := revocationPost(t, s, "/api/auth/logout", map[string]string{}, first); w.Code != http.StatusUnauthorized {
		t.Fatalf("logout with a revoked token status=%d", w.Code)
	}
	if got := protectedStatus(s, "/api/protected", second); got != http.StatusNoContent {
		t.Fatalf("other session was revoked: %d", got)
	}
	// A server with an empty cache (a restart) must reach the same verdicts from the database.
	restarted := &Server{m: s.m, jwtKey: s.jwtKey}
	if protectedStatus(restarted, "/api/protected", first) != http.StatusUnauthorized ||
		protectedStatus(restarted, "/api/protected", second) != http.StatusNoContent {
		t.Fatal("session state did not survive a restart")
	}
}

func TestSessionLogoutAllRevokesEverySession(t *testing.T) {
	s := sessionFixture(t)
	first, second := sessionLogin(t, s), sessionLogin(t, s)
	legacy, err := signCredentialJWT(s.jwtKey, currentHash(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if protectedStatus(s, "/api/protected", legacy) != http.StatusNoContent {
		t.Fatal("legacy token should work before sign-out everywhere")
	}
	if w := revocationPost(t, s, "/api/auth/logout-all", map[string]string{}, first); w.Code != http.StatusOK {
		t.Fatalf("logout-all status=%d: %s", w.Code, w.Body.String())
	}
	for name, tok := range map[string]string{"caller": first, "other": second, "legacy": legacy} {
		if got := protectedStatus(s, "/api/protected", tok); got != http.StatusUnauthorized {
			t.Fatalf("%s token still accepted after logout-all: %d", name, got)
		}
	}
	if sessionCount(t, s) != 0 {
		t.Fatalf("sessions left after logout-all: %d", sessionCount(t, s))
	}
	if protectedStatus(s, "/api/protected", sessionLogin(t, s)) != http.StatusNoContent {
		t.Fatal("a new login after logout-all must work")
	}
}

func currentHash(t *testing.T, s *Server) string {
	t.Helper()
	hash, _, err := s.m.pg.GetSetting(authPassKey)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestSessionPasswordChangeRevokesAllSessions(t *testing.T) {
	s := sessionFixture(t)
	first, second := sessionLogin(t, s), sessionLogin(t, s)
	w := revocationPost(t, s, "/api/auth/change-password", map[string]string{"old_password": sessionFixturePassword, "new_password": "replacement fixture password"}, first)
	if w.Code != http.StatusOK {
		t.Fatalf("password change status=%d: %s", w.Code, w.Body.String())
	}
	var replacement struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &replacement); err != nil || replacement.Token == "" {
		t.Fatalf("missing replacement token: %v", err)
	}
	for name, tok := range map[string]string{"caller": first, "other": second} {
		if got := protectedStatus(s, "/api/protected", tok); got != http.StatusUnauthorized {
			t.Fatalf("%s token accepted after password change: %d", name, got)
		}
	}
	if got := protectedStatus(s, "/api/protected", replacement.Token); got != http.StatusNoContent {
		t.Fatalf("replacement token rejected: %d", got)
	}
	if sessionCount(t, s) != 1 {
		t.Fatalf("want only the replacement session stored, got %d", sessionCount(t, s))
	}
}

func TestSessionLegacyTokenWithoutSessionID(t *testing.T) {
	s := sessionFixture(t)
	hash := currentHash(t, s)
	legacy, err := signCredentialJWT(s.jwtKey, hash)
	if err != nil {
		t.Fatal(err)
	}
	other, err := signCredentialJWT(s.jwtKey, hash)
	if err != nil {
		t.Fatal(err)
	}
	// Without a session id the token still works, as it did before the upgrade.
	if got := protectedStatus(s, "/api/protected", legacy); got != http.StatusNoContent {
		t.Fatalf("legacy token rejected: %d", got)
	}
	if sessionCount(t, s) != 0 {
		t.Fatal("legacy token must not need a stored session")
	}
	if w := revocationPost(t, s, "/api/auth/logout", map[string]string{}, legacy); w.Code != http.StatusOK {
		t.Fatalf("legacy logout status=%d: %s", w.Code, w.Body.String())
	}
	if got := protectedStatus(s, "/api/protected", legacy); got != http.StatusUnauthorized {
		t.Fatalf("legacy token accepted after sign-out: %d", got)
	}
	if got := protectedStatus(s, "/api/protected", other); got != http.StatusUnauthorized {
		t.Fatalf("sign-out of a legacy token must retire the other legacy tokens: %d", got)
	}
	if got := protectedStatus(s, "/api/protected", sessionLogin(t, s)); got != http.StatusNoContent {
		t.Fatalf("session token rejected after a legacy sign-out: %d", got)
	}
}

func TestSessionExpiredRowRejected(t *testing.T) {
	s := sessionFixture(t)
	token := sessionLogin(t, s)
	if _, err := s.m.pg.Exec(`UPDATE auth_sessions SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	fresh := &Server{m: s.m, jwtKey: s.jwtKey}
	if got := protectedStatus(fresh, "/api/protected", token); got != http.StatusUnauthorized {
		t.Fatalf("expired session accepted: %d", got)
	}
}

func TestSessionStoresShortUserAgentLabel(t *testing.T) {
	s := sessionFixture(t)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"COUNTERWEIGHT","password":"`+sessionFixturePassword+`"}`))
	r.Header.Set("User-Agent", strings.Repeat("a", 300))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login status=%d", w.Code)
	}
	var label string
	if err := s.m.pg.QueryRow(`SELECT user_agent FROM auth_sessions`).Scan(&label); err != nil || len(label) != maxUserAgentLabel {
		t.Fatalf("label %d bytes, err=%v", len(label), err)
	}
}

func TestStreamTicketIsSingleUseAndStreamOnly(t *testing.T) {
	s := sessionFixture(t)
	token := sessionLogin(t, s)
	if w := revocationPost(t, s, "/api/auth/stream-ticket", map[string]string{"path": "/api/logs/stream"}, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("ticket issued without credentials: %d", w.Code)
	}
	ticket := mintTicket(t, s, token)
	// A ticket opens no ordinary API route, and does not get consumed by trying one.
	if got := protectedStatus(s, "/api/stats?ticket="+ticket, ""); got != http.StatusUnauthorized {
		t.Fatalf("ticket authorized a non-stream route: %d", got)
	}
	if got := protectedStatus(s, "/api/stats?ticket="+ticket, token); got != http.StatusNoContent {
		t.Fatalf("bearer on non-stream route status=%d", got)
	}
	if got := protectedStatus(s, "/api/logs/stream?ticket="+ticket, ""); got != http.StatusNoContent {
		t.Fatalf("ticket rejected on a stream route: %d", got)
	}
	if got := protectedStatus(s, "/api/logs/stream?ticket="+ticket, ""); got != http.StatusUnauthorized {
		t.Fatalf("ticket reused: %d", got)
	}
	if got := protectedStatus(s, "/api/logs/stream?ticket=forged", ""); got != http.StatusUnauthorized {
		t.Fatalf("forged ticket accepted: %d", got)
	}
}

func TestStreamTicketIsBoundToItsStreamAndTask(t *testing.T) {
	s := sessionFixture(t)
	token := sessionLogin(t, s)
	for _, bad := range []string{"", "api/logs/stream", "//example.test/api/logs/stream", "/api/stats", "/api/logs/stream/extra", "/api/../api/logs/stream", "https://example.test/api/logs/stream"} {
		if w := revocationPost(t, s, "/api/auth/stream-ticket", map[string]string{"path": bad}, token); w.Code != http.StatusBadRequest {
			t.Fatalf("path %q accepted: %d", bad, w.Code)
		}
	}
	// Another stream route is refused, and the failed attempt spends the ticket.
	ticket := mintTicketFor(t, s, token, "/api/logs/stream?since=0")
	if got := protectedStatus(s, "/api/update/stream?ticket="+ticket, ""); got != http.StatusUnauthorized {
		t.Fatalf("ticket opened another stream: %d", got)
	}
	if got := protectedStatus(s, "/api/logs/stream?ticket="+ticket, ""); got != http.StatusUnauthorized {
		t.Fatalf("refused ticket was not consumed: %d", got)
	}
	// Reconnects change since, so only the path and the task are bound.
	activity := "/api/exploration/activity/stream?task=5&since=0"
	ticket = mintTicketFor(t, s, token, activity)
	if got := protectedStatus(s, "/api/exploration/activity/stream?task=6&since=0&ticket="+ticket, ""); got != http.StatusUnauthorized {
		t.Fatalf("ticket opened another task: %d", got)
	}
	ticket = mintTicketFor(t, s, token, activity)
	if got := protectedStatus(s, "/api/exploration/activity/stream?since=0&ticket="+ticket, ""); got != http.StatusUnauthorized {
		t.Fatalf("task-bound ticket opened a stream without the task: %d", got)
	}
	ticket = mintTicketFor(t, s, token, activity)
	if got := protectedStatus(s, "/api/exploration/activity/stream?since=9&task=5&ticket="+ticket, ""); got != http.StatusNoContent {
		t.Fatalf("ticket rejected on its own stream and task: %d", got)
	}
	ticket = mintTicketFor(t, s, token, "/api/exploration/activity/stream")
	if got := protectedStatus(s, "/api/exploration/activity/stream?task=5&ticket="+ticket, ""); got != http.StatusUnauthorized {
		t.Fatalf("ticket without a task opened a task stream: %d", got)
	}
	ticket = mintTicketFor(t, s, token, "/api/side-questions/abc/events")
	if got := protectedStatus(s, "/api/side-questions/xyz/events?ticket="+ticket, ""); got != http.StatusUnauthorized {
		t.Fatalf("ticket opened another side question: %d", got)
	}
}

func TestStreamTicketExpires(t *testing.T) {
	s := sessionFixture(t)
	ticket := mintTicket(t, s, sessionLogin(t, s))
	s.sessions.mu.Lock()
	for k, v := range s.sessions.tickets {
		v.expires = time.Now().Add(-time.Second)
		s.sessions.tickets[k] = v
	}
	s.sessions.mu.Unlock()
	if got := protectedStatus(s, "/api/logs/stream?ticket="+ticket, ""); got != http.StatusUnauthorized {
		t.Fatalf("expired ticket accepted: %d", got)
	}
}

func TestStreamTicketBoundToSession(t *testing.T) {
	s := sessionFixture(t)
	token := sessionLogin(t, s)
	ticket := mintTicket(t, s, token)
	if w := revocationPost(t, s, "/api/auth/logout", map[string]string{}, token); w.Code != http.StatusOK {
		t.Fatalf("logout status=%d", w.Code)
	}
	if got := protectedStatus(s, "/api/logs/stream?ticket="+ticket, ""); got != http.StatusUnauthorized {
		t.Fatalf("ticket outlived its session: %d", got)
	}
}

func TestStreamQueryTokenNoLongerAuthenticates(t *testing.T) {
	s := sessionFixture(t)
	token := sessionLogin(t, s)
	for _, target := range []string{"/api/stats?token=", "/api/logs/stream?token="} {
		if got := protectedStatus(s, target+token, ""); got != http.StatusUnauthorized {
			t.Fatalf("%s authenticated: %d", target, got)
		}
	}
}

func TestStreamEndsAfterSessionRevoked(t *testing.T) {
	s := sessionFixture(t)
	old := streamRecheckInterval
	streamRecheckInterval = 50 * time.Millisecond
	t.Cleanup(func() { streamRecheckInterval = old })
	server := httptest.NewServer(s.Handler())
	t.Cleanup(server.Close)

	token := sessionLogin(t, s)
	ticket := mintTicket(t, s, token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/logs/stream?since=0&ticket="+ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if w := revocationPost(t, s, "/api/auth/logout", map[string]string{}, token); w.Code != http.StatusOK {
		t.Fatalf("logout status=%d", w.Code)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(resp.Body)
		for {
			if _, err := reader.ReadString('\n'); err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream stayed open after its session was revoked")
	}
}

func TestAuthTicketCapIsBounded(t *testing.T) {
	s := sessionFixture(t)
	token := sessionLogin(t, s)
	for i := 0; i < maxStreamTickets; i++ {
		mintTicket(t, s, token)
	}
	if w := revocationPost(t, s, "/api/auth/stream-ticket", map[string]string{"path": "/api/logs/stream"}, token); w.Code != http.StatusTooManyRequests {
		t.Fatalf("ticket store is unbounded: %d", w.Code)
	}
}
