package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCookiesCannotAuthorizeAdministratorOperations(t *testing.T) {
	s, password := revocationFixture(t)
	token := revocationLogin(t, s, password)
	for _, path := range []string{"/api/tools/custom/test", "/api/llm/test", "/api/auth/change-password"} {
		for _, origin := range []string{"http://127.0.0.1:3001", "http://attacker.example.test", "", "null"} {
			r := httptest.NewRequest("POST", "http://127.0.0.1:3000"+path, strings.NewReader(`{"old_password":"test-only initial password","new_password":"attacker"}`))
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "text/plain") // A browser simple request.
			r.Header.Set("Sec-Fetch-Site", "same-site")
			r.AddCookie(&http.Cookie{Name: "counterweight_token", Value: token})
			r.AddCookie(&http.Cookie{Name: "counterweight_session", Value: "1"})
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s origin=%q: status=%d body=%s", path, origin, w.Code, w.Body.String())
			}
		}
	}
	// A forged routing marker is equally powerless, including for read routes.
	r := httptest.NewRequest("GET", "/api/protected", nil)
	r.AddCookie(&http.Cookie{Name: "counterweight_session", Value: "1"})
	w := httptest.NewRecorder()
	s.requireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("marker authorized handler") })).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("marker status=%d", w.Code)
	}
	checkSessionTransports(t, s, token, http.StatusNoContent)
}

func TestExplicitSessionTokenCannotBeShadowedByCookie(t *testing.T) {
	s, password := revocationFixture(t)
	token := revocationLogin(t, s, password)
	for _, credential := range []string{"bearer", "query"} {
		r := httptest.NewRequest("GET", "/api/protected", nil)
		r.AddCookie(&http.Cookie{Name: "counterweight_token", Value: "stale-or-attacker-cookie"})
		if credential == "bearer" {
			r.Header.Set("Authorization", "Bearer "+token)
		} else {
			query := r.URL.Query()
			query.Set("token", token)
			r.URL.RawQuery = query.Encode()
		}
		w := httptest.NewRecorder()
		s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("%s shadowed: %d", credential, w.Code)
		}
	}
}
