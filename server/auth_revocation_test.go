package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"golang.org/x/crypto/bcrypt"
)

func revocationFixture(t *testing.T) (*Server, string) {
	t.Helper()
	dsn := os.Getenv("COUNTERWEIGHT_PG_DSN")
	if !strings.Contains(dsn, "/counterweight_server_security?") {
		t.Skip("requires explicitly selected disposable counterweight_server_security database")
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
	t.Cleanup(func() {
		if existed {
			_ = pg.SetSetting(authPassKey, prior)
		} else {
			_, _ = pg.Exec(`DELETE FROM settings WHERE key=$1`, authPassKey)
		}
	})
	const password = "test-only initial password"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := pg.SetSetting(authPassKey, string(hash)); err != nil {
		t.Fatal(err)
	}
	return &Server{m: &Manager{pg: pg}, jwtKey: []byte(strings.Repeat("v", 32))}, password
}

func revocationPost(t *testing.T, s *Server, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func revocationLogin(t *testing.T, s *Server, password string) string {
	t.Helper()
	w := revocationPost(t, s, "/api/auth/login", map[string]string{"username": "COUNTERWEIGHT", "password": password}, "")
	if w.Code != http.StatusOK {
		t.Fatalf("login status=%d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Token == "" {
		t.Fatalf("missing login token: %v", err)
	}
	return response.Token
}

func checkSessionTransports(t *testing.T, s *Server, token string, want int) {
	t.Helper()
	for _, transport := range []string{"bearer", "cookie", "query"} {
		r := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
		switch transport {
		case "bearer":
			r.Header.Set("Authorization", "Bearer "+token)
		case "cookie":
			r.AddCookie(&http.Cookie{Name: "counterweight_token", Value: token})
		case "query":
			q := r.URL.Query()
			q.Set("token", token)
			r.URL.RawQuery = q.Encode()
		}
		w := httptest.NewRecorder()
		s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(w, r)
		transportWant := want
		if transport == "cookie" || transport == "query" {
			// Cookies and URL query strings are not credential transports, even
			// for a fresh session. Only the Bearer header authenticates.
			transportWant = http.StatusUnauthorized
		}
		if w.Code != transportWant {
			t.Fatalf("%s token status=%d, want %d", transport, w.Code, transportWant)
		}
	}
}

func TestCredentialBoundSessionsRevokeOnPasswordChange(t *testing.T) {
	s, password := revocationFixture(t)
	token := revocationLogin(t, s, password)
	checkSessionTransports(t, s, token, http.StatusNoContent)
	legacy, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	checkSessionTransports(t, s, legacy, http.StatusUnauthorized)
	w := revocationPost(t, s, "/api/auth/change-password", map[string]string{"old_password": "wrong", "new_password": "replacement password"}, token)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password accepted: %d", w.Code)
	}
	checkSessionTransports(t, s, token, http.StatusNoContent)
	w = revocationPost(t, s, "/api/auth/change-password", map[string]string{"old_password": password, "new_password": "replacement password"}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("password change failed: %d %s", w.Code, w.Body.String())
	}
	var replacement struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &replacement); err != nil || replacement.Token == "" {
		t.Fatalf("missing replacement token: %v", err)
	}
	checkSessionTransports(t, s, token, http.StatusUnauthorized)
	checkSessionTransports(t, s, replacement.Token, http.StatusNoContent)
	// A reconstructed server with the same installation key uses durable DB state.
	restarted := &Server{m: s.m, jwtKey: s.jwtKey}
	checkSessionTransports(t, restarted, token, http.StatusUnauthorized)
	checkSessionTransports(t, restarted, replacement.Token, http.StatusNoContent)
	w = revocationPost(t, s, "/api/auth/change-password", map[string]string{"old_password": "replacement password", "new_password": "stale overwrite"}, token)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token could change password: %d", w.Code)
	}
	checkSessionTransports(t, s, revocationLogin(t, s, "replacement password"), http.StatusNoContent)

	affected, err := db.Open(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	if err := affected.Close(); err != nil {
		t.Fatal(err)
	}
	unavailable := &Server{m: &Manager{pg: affected}, jwtKey: s.jwtKey}
	checkSessionTransports(t, unavailable, replacement.Token, http.StatusServiceUnavailable)
}

func TestAuthResetScriptRevokesSessions(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_RESET_E2E") != "1" {
		t.Skip("requires explicitly enabled local reset-script integration")
	}
	s, password := revocationFixture(t)
	token := revocationLogin(t, s, password)
	checkSessionTransports(t, s, token, http.StatusNoContent)
	source, err := os.ReadFile(filepath.Join("..", "reset-password.sh"))
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	script := filepath.Join(temp, "reset-password.sh")
	// Git's Windows checkout can use CRLF; only line endings change in this copy.
	if err := os.WriteFile(script, bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n")), 0600); err != nil {
		t.Fatal(err)
	}
	bash := `C:\Program Files\Git\bin\bash.exe`
	command := exec.Command(bash, "--noprofile", "--norc", filepath.ToSlash(script), "-m", "docker", "--exec", "docker", "-c", "counterweight-security-tests-20261010", "-U", "counterweight_test", "-d", "counterweight_server_security", "-y", "-p", password)
	command.Dir = temp // No project .env or production config may enter the fixture.
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("actual reset helper failed: %v %s", err, out)
	}
	// Resetting to the same password still creates a new bcrypt generation.
	checkSessionTransports(t, s, token, http.StatusUnauthorized)
	checkSessionTransports(t, s, revocationLogin(t, s, password), http.StatusNoContent)
}

func TestWebPasswordChangeRevokesPreviousSession(t *testing.T) {
	s, password := revocationFixture(t)
	token := revocationLogin(t, s, password)
	checkSessionTransports(t, s, token, http.StatusNoContent)
	w := revocationPost(t, s, "/api/auth/change-password", map[string]string{"old_password": password, "new_password": "web replacement fixture"}, token)
	if w.Code != http.StatusOK {
		t.Fatalf("web password change failed: %d %s", w.Code, w.Body.String())
	}
	checkSessionTransports(t, s, token, http.StatusUnauthorized)
	checkSessionTransports(t, s, revocationLogin(t, s, "web replacement fixture"), http.StatusNoContent)
}

func TestAuthLocalResetScriptRevokesSessions(t *testing.T) {
	backend := os.Getenv("COUNTERWEIGHT_LOCAL_RESET_BACKEND")
	if backend == "" {
		t.Skip("requires explicitly built local backend fixture")
	}
	s, password := revocationFixture(t)
	token := revocationLogin(t, s, password)
	checkSessionTransports(t, s, token, http.StatusNoContent)
	source, err := os.ReadFile(filepath.Join("..", "reset-password.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "reset-password.sh")
	if err := os.WriteFile(script, bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n")), 0600); err != nil {
		t.Fatal(err)
	}
	bash := `C:\Program Files\Git\bin\bash.exe`
	cmd := exec.Command(bash, "--noprofile", "--norc", filepath.ToSlash(script), "-m", "local", "--backend", filepath.ToSlash(backend), "-y", "-p", password)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native script reset failed: %v %s", err, out)
	}
	checkSessionTransports(t, s, token, http.StatusUnauthorized)
	checkSessionTransports(t, s, revocationLogin(t, s, password), http.StatusNoContent)
}

func TestConcurrentPasswordChangesHaveSingleWinner(t *testing.T) {
	s, password := revocationFixture(t)
	token := revocationLogin(t, s, password)
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	var workers sync.WaitGroup
	for _, newPassword := range []string{"first replacement", "second replacement"} {
		workers.Add(1)
		go func(next string) {
			defer workers.Done()
			<-start
			results <- revocationPost(t, s, "/api/auth/change-password", map[string]string{"old_password": password, "new_password": next}, token)
		}(newPassword)
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for response := range results {
		switch response.Code {
		case http.StatusOK:
			winners++
			var session struct {
				Token string `json:"token"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
				t.Fatal(err)
			}
			checkSessionTransports(t, s, session.Token, http.StatusNoContent)
		case http.StatusUnauthorized:
		default:
			t.Fatalf("unexpected concurrent change response: %d %s", response.Code, response.Body.String())
		}
	}
	if winners != 1 {
		t.Fatalf("got %d successful password changes, want one", winners)
	}
	checkSessionTransports(t, s, token, http.StatusUnauthorized)
	// A stale authenticated hash must also fail after an external reset.
	current, _, err := s.m.pg.GetSetting(authPassKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.m.pg.SetSetting(authPassKey, "external reset fixture"); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.m.pg.CompareAndSwapSetting(authPassKey, current, "stale overwrite"); err != nil || changed {
		t.Fatalf("stale credential update accepted: %v %v", changed, err)
	}
	stored, _, err := s.m.pg.GetSetting(authPassKey)
	if err != nil || stored != "external reset fixture" {
		t.Fatalf("external reset lost: %q %v", stored, err)
	}
}
