package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestAuthRejectsPasswordsShorterThanMinimum(t *testing.T) {
	// Without a database, a password that passes validation reaches the database
	// step and gets 503; a rejected one gets 400 before it.
	s := &Server{m: &Manager{}, jwtKey: []byte(strings.Repeat("p", 32))}
	session, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		password string
		want     int
	}{
		{"", http.StatusBadRequest},
		{"a", http.StatusBadRequest},
		{"seven77", http.StatusBadRequest},
		{"ñññññññ", http.StatusBadRequest}, // 7 characters, 14 bytes
		{"eight888", http.StatusServiceUnavailable},
		{"ññññññññ", http.StatusServiceUnavailable},
		{strings.Repeat("x", 72), http.StatusServiceUnavailable},
		{strings.Repeat("x", 73), http.StatusBadRequest},
	}
	for _, tc := range cases {
		w := revocationPost(t, s, "/api/auth/init", map[string]string{"password": tc.password, "setup_token": bootstrapToken(s.jwtKey)}, "")
		if w.Code != tc.want {
			t.Errorf("init %q: %d %s, want %d", tc.password, w.Code, w.Body.String(), tc.want)
		}
		w = revocationPost(t, s, "/api/auth/change-password", map[string]string{"old_password": "current password", "new_password": tc.password}, session)
		if w.Code != tc.want {
			t.Errorf("change %q: %d %s, want %d", tc.password, w.Code, w.Body.String(), tc.want)
		}
		if tc.want == http.StatusBadRequest && len(tc.password) <= 72 && !strings.Contains(w.Body.String(), "at least 8 characters") {
			t.Errorf("change %q: unexpected message %s", tc.password, w.Body.String())
		}
	}
	// The setup token is still checked before the password policy.
	w := revocationPost(t, s, "/api/auth/init", map[string]string{"password": "a", "setup_token": "wrong"}, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("init without setup token: %d", w.Code)
	}
}
