package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func originPolicyHandler() (*Server, http.Handler) {
	s := &Server{m: &Manager{}, jwtKey: []byte(strings.Repeat("k", 32))}
	return s, s.Handler()
}

func originRequest(method, target, host string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(`{"username":"x","password":"y"}`))
	r.Host = host
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestCORSOnlyServesTheConsoleOrigin(t *testing.T) {
	_, h := originPolicyHandler()
	cases := []struct {
		name, host, origin string
		wantACAO           bool
	}{
		{"foreign", "127.0.0.1:8787", "https://attacker.example", false},
		{"foreign on loopback port", "127.0.0.1:8787", "http://localhost:4000", false},
		{"same origin", "cw.example.test", "https://cw.example.test", true},
		{"dev server", "127.0.0.1:8787", "http://localhost:3000", true},
		{"dev port against a public host", "cw.example.test", "http://localhost:3000", false},
		{"opaque origin", "127.0.0.1:8787", "null", false},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, originRequest(http.MethodGet, "/api/health", c.host, map[string]string{"Origin": c.origin}))
		got := w.Header().Get("Access-Control-Allow-Origin")
		if c.wantACAO && got != c.origin || !c.wantACAO && got != "" {
			t.Errorf("%s: Access-Control-Allow-Origin=%q", c.name, got)
		}
		pre := httptest.NewRecorder()
		h.ServeHTTP(pre, originRequest(http.MethodOptions, "/api/auth/login", c.host, map[string]string{"Origin": c.origin}))
		if c.wantACAO && pre.Code != http.StatusNoContent || !c.wantACAO && pre.Code != http.StatusForbidden {
			t.Errorf("%s: preflight status %d", c.name, pre.Code)
		}
		if !c.wantACAO && pre.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s: preflight approved", c.name)
		}
	}
	// Clients that send no Origin are unaffected.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, originRequest(http.MethodOptions, "/api/health", "example.test", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("preflight without Origin: %d", w.Code)
	}
}

func TestCredentialEndpointsRefuseCrossSiteBrowsers(t *testing.T) {
	s, h := originPolicyHandler()
	for _, path := range []string{"/api/auth/login", "/api/auth/init", "/api/auth/change-password"} {
		for name, headers := range map[string]map[string]string{
			"sec-fetch cross-site": {"Sec-Fetch-Site": "cross-site"},
			"foreign origin":       {"Origin": "https://attacker.example"},
			"same-site origin":     {"Sec-Fetch-Site": "same-site", "Origin": "http://localhost:4000"},
		} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, originRequest(http.MethodPost, path, "127.0.0.1:8787", headers))
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s: status %d", path, name, w.Code)
			}
		}
	}
	if len(s.authGate.peers) != 0 {
		t.Fatal("refused requests were charged to the login limiter")
	}
	for name, headers := range map[string]map[string]string{
		"no browser headers": nil,
		"same origin":        {"Sec-Fetch-Site": "same-origin", "Origin": "https://cw.example.test"},
		"proxy host rewrite": {"Sec-Fetch-Site": "same-origin", "Origin": "https://public.example"},
		"own origin":         {"Origin": "https://cw.example.test"},
		"dev server":         {"Sec-Fetch-Site": "same-site", "Origin": "http://localhost:3000"},
	} {
		host := "cw.example.test"
		if name == "dev server" {
			host = "localhost:8787"
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, originRequest(http.MethodPost, "/api/auth/login", host, headers))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want the handler's 401", name, w.Code)
		}
	}
}

func TestPlainHTTPLoopbackListenerRefusesRebindingNames(t *testing.T) {
	_, h := originPolicyHandler()
	loopback := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8787}
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 8787}
	calls := 0
	do := func(local net.Addr, host string, useTLS bool, headers map[string]string) int {
		r := originRequest(http.MethodPost, "/api/auth/login", host, headers)
		calls++
		r.RemoteAddr = fmt.Sprintf("192.0.%d.1:4000", calls)
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, local))
		if useTLS {
			r.TLS = &tls.ConnectionState{}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	rebound := map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://attacker.example:8787"}
	if got := do(loopback, "attacker.example:8787", false, rebound); got != http.StatusForbidden {
		t.Errorf("rebound name accepted: %d", got)
	}
	for name, c := range map[string]struct {
		local   net.Addr
		host    string
		tls     bool
		headers map[string]string
	}{
		"loopback name":    {loopback, "localhost:8787", false, map[string]string{"Origin": "http://localhost:8787"}},
		"loopback address": {loopback, "127.0.0.1:8787", false, map[string]string{"Origin": "http://127.0.0.1:8787"}},
		"tls proxy origin": {loopback, "backend:8787", false, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://public.example"}},
		"cli without host": {loopback, "public.example", false, nil},
		"tls listener":     {loopback, "public.example", true, map[string]string{"Origin": "http://public.example"}},
		"network listener": {remote, "public.example", false, map[string]string{"Origin": "http://public.example"}},
	} {
		if got := do(c.local, c.host, c.tls, c.headers); got != http.StatusUnauthorized {
			t.Errorf("%s: status %d", name, got)
		}
	}
}
