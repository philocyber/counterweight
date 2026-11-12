package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/Philocyber/counterweight/locale"
)

// devConsolePort is where `next dev` serves the console while it calls the API
// on its own port.
const devConsolePort = "3000"

// requestOrigin parses the Origin header. ok is false when it is absent or not
// a plain scheme://host[:port] value (for example the opaque "null" origin).
func requestOrigin(r *http.Request) (u *url.URL, ok bool) {
	raw := r.Header.Get("Origin")
	if raw == "" {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, false
	}
	return u, true
}

// loopbackName reports whether host (without a port) is localhost or a
// loopback address.
func loopbackName(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// allowedOrigin reports whether the request's Origin is the console's own
// origin, or the local development server (loopback, port 3000) talking to a
// loopback API.
func allowedOrigin(r *http.Request) bool {
	u, ok := requestOrigin(r)
	if !ok {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	return u.Port() == devConsolePort && loopbackName(u.Hostname()) && loopbackName(hostOnly(r.Host))
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.Trim(hostport, "[]")
}

// crossSiteCredentialRequest reports whether a request to a credential endpoint
// comes from a page on another site. Requests with neither Sec-Fetch-Site nor
// Origin (the CLI, scripts, API clients) are not browser requests.
func crossSiteCredentialRequest(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site":
		return true
	case "same-origin", "none":
		return false
	}
	if r.Header.Get("Origin") == "" {
		return false
	}
	return !allowedOrigin(r)
}

func credentialEndpoint(path string) bool {
	switch path {
	case "/api/auth/login", "/api/auth/init", "/api/auth/change-password":
		return true
	}
	return false
}

// rebindingSuspect reports whether a request arrived on a plain-HTTP loopback
// listener for a name that is not a loopback name, from a page served over
// plain HTTP. That is how a page on an attacker's domain reaches a local console
// after rebinding its DNS name to a loopback address: a browser cannot reach a
// plain-HTTP listener from an HTTPS page, and the Origin header cannot be set by
// the page. The console's own public name, when the operator configured one,
// is allowed.
func (s *Server) rebindingSuspect(r *http.Request) bool {
	if r.TLS != nil || loopbackName(hostOnly(r.Host)) {
		return false
	}
	local, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	tcp, ok := local.(*net.TCPAddr)
	if !ok || !tcp.IP.IsLoopback() {
		return false
	}
	if u, ok := requestOrigin(r); !ok || u.Scheme != "http" {
		return false
	}
	if s.m != nil && s.m.pg != nil {
		if base, err := url.Parse(notifyPublicBaseURL(s.m.pg)); err == nil && base.Host != "" && strings.EqualFold(base.Host, r.Host) {
			return false
		}
	}
	return true
}

// guardBrowserRequests refuses, before routing and before any limiter is
// charged, browser requests that are cross-site or that look like DNS
// rebinding. Non-browser clients send no Origin and are unaffected.
func (s *Server) guardBrowserRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (credentialEndpoint(r.URL.Path) && crossSiteCredentialRequest(r)) || s.rebindingSuspect(r) {
			writeErr(w, http.StatusForbidden, locale.Text(responseLanguage(w), "Cross-site request refused"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
