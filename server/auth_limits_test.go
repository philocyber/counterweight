package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
	"golang.org/x/crypto/bcrypt"
)

func TestDecodeAuthRequestBounds(t *testing.T) {
	valid := `{"password":"valid password"}`
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"normal", valid, http.StatusOK},
		{"exact byte limit", valid + strings.Repeat(" ", maxAuthRequestBytes-len(valid)), http.StatusOK},
		{"oversized credential", `{"password":"` + strings.Repeat("x", maxAuthRequestBytes) + `"}`, http.StatusRequestEntityTooLarge},
		{"oversized unknown field", `{"ignored":"` + strings.Repeat("x", maxAuthRequestBytes) + `"}`, http.StatusRequestEntityTooLarge},
		{"oversized trailing whitespace", valid + strings.Repeat(" ", maxAuthRequestBytes), http.StatusRequestEntityTooLarge},
		{"second value", valid + `{}`, http.StatusBadRequest},
		{"malformed", `{"password":`, http.StatusBadRequest},
		{"empty", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			var body struct {
				Password string `json:"password"`
			}
			ok := decodeAuthRequest(w, r, &body)
			if w.Code != tc.want || ok != (tc.want == http.StatusOK) {
				t.Fatalf("accepted=%v status=%d, want status=%d: %s", ok, w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestAuthEnrollmentAndPasswordFlowPG(t *testing.T) {
	// This test changes administrator credentials. Require an explicitly named
	// disposable database instead of using the application's configured database.
	dsn := os.Getenv("COUNTERWEIGHT_PG_DSN")
	if !strings.Contains(dsn, "/counterweight_server_security?") {
		t.Skip("requires the disposable counterweight_server_security database")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(`DELETE FROM settings WHERE key=$1`, authPassKey)
		_ = pg.Close()
	})
	if _, err := pg.Exec(`DELETE FROM settings WHERE key=$1`, authPassKey); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{pg: pg}, jwtKey: []byte(strings.Repeat("k", 32))}
	h := s.Handler()
	do := func(path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{"", "wrong setup token", bootstrapToken([]byte(strings.Repeat("z", 32)))} {
		got := do("/api/auth/init", fmt.Sprintf(`{"password":"untrusted enrollment","setup_token":%q}`, token), "")
		if got.Code != http.StatusForbidden {
			t.Fatalf("untrusted enrollment returned %d, want 403", got.Code)
		}
	}
	if _, initialized, err := pg.GetSetting(authPassKey); err != nil || initialized {
		t.Fatalf("untrusted enrollment changed the password: initialized=%v err=%v", initialized, err)
	}
	type attempt struct {
		password string
		response *httptest.ResponseRecorder
	}
	results := make(chan attempt, 8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			password := fmt.Sprintf("test enrollment %d", index)
			results <- attempt{password, do("/api/auth/init", fmt.Sprintf(`{"password":%q,"setup_token":%q}`, password, bootstrapToken(s.jwtKey)), "")}
		}(i)
	}
	close(start)
	workers.Wait()
	close(results)
	var winner attempt
	successes := 0
	for result := range results {
		switch result.response.Code {
		case http.StatusOK:
			winner = result
			successes++
		case http.StatusForbidden, http.StatusTooManyRequests:
			// Losing enrollment requests are either rejected atomically or declined
			// before hashing by the explicit two-job password concurrency budget.
		default:
			t.Fatalf("enrollment returned %d: %s", result.response.Code, result.response.Body.String())
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful enrollments, want exactly one", successes)
	}
	hash, _, err := pg.GetSetting(authPassKey)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(winner.password)) != nil {
		t.Fatalf("stored password does not belong to the sole winner: %v", err)
	}
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(winner.response.Body.Bytes(), &session); err != nil || !verifyCredentialJWT(session.Token, s.jwtKey, hash) {
		t.Fatalf("enrollment did not return a valid session: %v", err)
	}
	login := do("/api/auth/login", fmt.Sprintf(`{"username":"COUNTERWEIGHT","password":%q}`, winner.password), "")
	if login.Code != http.StatusOK {
		t.Fatalf("normal login: %d %s", login.Code, login.Body.String())
	}
	change := do("/api/auth/change-password", fmt.Sprintf(`{"old_password":%q,"new_password":"replacement password"}`, winner.password), session.Token)
	if change.Code != http.StatusOK {
		t.Fatalf("password change: %d %s", change.Code, change.Body.String())
	}
	if got := do("/api/auth/login", fmt.Sprintf(`{"username":"COUNTERWEIGHT","password":%q}`, winner.password), ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("previous password remains accepted: %d", got.Code)
	}
	if got := do("/api/auth/login", `{"username":"COUNTERWEIGHT","password":"replacement password"}`, ""); got.Code != http.StatusOK {
		t.Fatalf("replacement password login: %d %s", got.Code, got.Body.String())
	}
}

func TestAuthRoutesRejectOversizedBodiesBeforeDatabaseWork(t *testing.T) {
	s := &Server{m: &Manager{}, jwtKey: []byte(strings.Repeat("k", 32))}
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/auth/init", "/api/auth/login", "/api/auth/change-password"} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"password":"`+strings.Repeat("x", maxAuthRequestBytes)+`"}`))
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			withLocale(s.Handler()).ServeHTTP(w, r)
			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status=%d, want 413: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAuthLoginPasswordByteBoundary(t *testing.T) {
	s := &Server{m: &Manager{}}
	for _, tc := range []struct {
		name     string
		password string
		want     int
	}{
		{"72 ASCII bytes", strings.Repeat("a", 72), http.StatusUnauthorized},
		{"73 ASCII bytes", strings.Repeat("a", 73), http.StatusBadRequest},
		{"72 UTF-8 bytes", strings.Repeat("€", 24), http.StatusUnauthorized},
		{"75 UTF-8 bytes", strings.Repeat("€", 25), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"username": "wrong user", "password": tc.password})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(string(body)))
			w := httptest.NewRecorder()
			s.authLogin(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestAuthBodyDeadlineBoundsSlowRequest(t *testing.T) {
	s := httptest.NewServer(withLocale(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body any
		if decodeAuthRequest(w, r, &body) {
			w.WriteHeader(http.StatusOK)
		}
	})))
	defer s.Close()
	conn, err := net.Dial("tcp", s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(authBodyReadTimeout + 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Send complete headers and an unfinished body. A header timeout cannot
	// terminate this request, so the authentication-specific deadline must.
	if _, err := fmt.Fprintf(conn, "POST /api/auth/login HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\nConnection: close\r\n\r\n{\"password\":\""); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("status=%d, want 408", response.StatusCode)
	}
}
