package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerSetsSecurityHeaders(t *testing.T) {
	h := (&Server{m: &Manager{}, jwtKey: []byte(strings.Repeat("k", 32))}).Handler()
	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": consoleContentSecurityPolicy,
	}
	// A public API route, a rejected (unauthenticated) API route and the console.
	for _, path := range []string{"/api/health", "/api/stats", "/"} {
		for _, target := range []string{"http://example.test" + path, "https://example.test" + path} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
			for k, v := range want {
				if got := w.Header().Get(k); got != v {
					t.Errorf("%s: %s=%q, want %q", target, k, got, v)
				}
			}
			hsts := w.Header().Get("Strict-Transport-Security")
			if strings.HasPrefix(target, "https://") {
				if hsts != "max-age=31536000" {
					t.Errorf("%s: HSTS=%q", target, hsts)
				}
			} else if hsts != "" {
				t.Errorf("%s: HSTS sent over plain HTTP: %q", target, hsts)
			}
		}
	}
	if !strings.Contains(consoleContentSecurityPolicy, "img-src 'self' data: blob:") || !strings.Contains(consoleContentSecurityPolicy, "frame-ancestors 'none'") {
		t.Fatalf("unexpected policy %q", consoleContentSecurityPolicy)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("health: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
}
