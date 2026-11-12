package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// API tokens for automation (a CI workflow). A token is accepted only on the
// automation endpoints below, with the scope each one needs; it never opens a
// console session, settings, or any other endpoint. Session tokens are
// untouched. The tokens are created, listed and revoked from the console with a
// session.

const (
	// apiTokenPrefix lets secret scanners recognise a leaked token.
	apiTokenPrefix = "cwt_"
	// apiTokenSecretLen is the length of the random part: 32 bytes, base64url.
	apiTokenSecretLen = 43
)

// newAPIToken returns a token, the hash that is stored, and a hint for lists.
// The secret is 256 bits from the system CSPRNG, so a fast hash is enough to
// protect it at rest and a slow one would only add cost to every request.
func newAPIToken() (secret, hash, hint string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", err
	}
	secret = apiTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return secret, hashAPIToken(secret), secret[:len(apiTokenPrefix)+4], nil
}

func hashAPIToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// wellFormedAPIToken rejects anything that cannot be a token before it is hashed.
func wellFormedAPIToken(tok string) bool {
	body, ok := strings.CutPrefix(tok, apiTokenPrefix)
	if !ok || len(body) != apiTokenSecretLen {
		return false
	}
	for _, c := range body {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// automationScope returns the scope an API token needs for a request, or ""
// when the request is not an automation endpoint (so a token is refused there).
// It matches only the exact canonical paths of the endpoints.
func automationScope(r *http.Request) string {
	p := r.URL.Path
	switch r.Method {
	case http.MethodPost:
		if p == "/api/reviews" {
			return db.ScopeReviewsWrite
		}
	case http.MethodGet:
		rest, ok := strings.CutPrefix(p, "/api/reviews/")
		if !ok {
			return ""
		}
		parts := strings.Split(rest, "/")
		if !allDigitsID(parts[0]) {
			return ""
		}
		if len(parts) == 1 || (len(parts) == 2 && (parts[1] == "sarif" || parts[1] == "summary")) {
			return db.ScopeReviewsRead
		}
	}
	return ""
}

func allDigitsID(s string) bool {
	if s == "" || len(s) > 18 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// apiTokenFailures limits failed token attempts per peer like failed logins:
// five in a burst, then one every six seconds. A valid token is never counted.
var apiTokenFailures failedAttempts

type failedAttempts struct {
	mu    sync.Mutex
	peers map[string]authBucket
}

func (f *failedAttempts) blocked(peer string, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.peers[peer]
	if !ok {
		return false
	}
	b.refill(now, 5, 6*time.Second)
	f.peers[peer] = b
	return b.tokens < 1
}

func (f *failedAttempts) fail(peer string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.peers == nil {
		f.peers = make(map[string]authBucket)
	}
	if _, ok := f.peers[peer]; !ok && len(f.peers) >= maxAuthPeers {
		for key, b := range f.peers {
			if now.Sub(b.last) >= 10*time.Minute {
				delete(f.peers, key)
			}
		}
		if len(f.peers) >= maxAuthPeers {
			return // table full of recent peers: the global login limits still apply elsewhere
		}
	}
	b := f.peers[peer]
	b.refill(now, 5, 6*time.Second)
	b.tokens--
	f.peers[peer] = b
}

// serveAPIToken authenticates an API token for requireAuth: valid and unexpired,
// allowed on this endpoint, and carrying the scope it needs.
func (s *Server) serveAPIToken(next http.Handler, w http.ResponseWriter, r *http.Request, tok string) {
	peer, now := authPeer(r), time.Now()
	// The failure limiter only decides how an invalid token is refused; a valid
	// token is served whatever other clients behind the same address did.
	reject := func() {
		if apiTokenFailures.blocked(peer, now) {
			authBusy(w)
			return
		}
		apiTokenFailures.fail(peer, now)
		writeErr(w, http.StatusUnauthorized, locale.Text(responseLanguage(w), "Invalid or expired token"))
	}
	if !wellFormedAPIToken(tok) {
		reject()
		return
	}
	pg := s.pg(w)
	if pg == nil {
		return
	}
	hash := hashAPIToken(tok)
	rec, err := pg.LookupAPIToken(hash)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, locale.Text(responseLanguage(w), "Authentication request unavailable"))
		return
	}
	if rec == nil || !rec.HashMatches(hash) || !rec.Active(now) {
		reject()
		return
	}
	scope := automationScope(r)
	if scope == "" {
		writeErr(w, http.StatusForbidden, locale.Text(responseLanguage(w), "This API token cannot be used on this endpoint"))
		return
	}
	if !rec.HasScope(scope) {
		writeErr(w, http.StatusForbidden, locale.Text(responseLanguage(w), "The API token does not have the %s scope", scope))
		return
	}
	if err := pg.TouchAPIToken(rec.ID); err != nil {
		log.Printf("[api-token] last-used update failed: %v", err)
	}
	next.ServeHTTP(w, r)
}

func (s *Server) registerAPITokenRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/api-tokens", s.listAPITokens)
	mux.HandleFunc("POST /api/api-tokens", s.createAPIToken)
	mux.HandleFunc("DELETE /api/api-tokens/{id}", s.revokeAPIToken)
}

func (s *Server) listAPITokens(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	tokens, err := pg.ListAPITokens()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": tokens})
}

func (s *Server) createAPIToken(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt string   `json:"expires_at"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, locale.Text(responseLanguage(w), "bad json: ")+locale.ErrorMessage(responseLanguage(w), err))
		return
	}
	var expires *time.Time
	if v := strings.TrimSpace(req.ExpiresAt); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, 400, locale.Text(responseLanguage(w), "expires_at must be an RFC 3339 time, for example 2027-01-31T00:00:00Z"))
			return
		}
		expires = &t
	}
	secret, hash, hint, err := newAPIToken()
	if err != nil {
		writeErr(w, 500, locale.Text(responseLanguage(w), "Token generation failed"))
		return
	}
	rec, err := pg.CreateAPIToken(req.Name, req.Scopes, hash, hint, expires)
	if err != nil {
		code := 500
		if errors.Is(err, db.ErrAPITokenName) || errors.Is(err, db.ErrAPITokenScopes) || errors.Is(err, db.ErrAPITokenExpiry) {
			code = 400
		} else if errors.Is(err, db.ErrAPITokenLimit) {
			code = 409
		}
		writeError(w, code, err)
		return
	}
	// The secret is shown here once and is not stored.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"token": secret, "api_token": rec})
}

func (s *Server) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 404, "API token not found")
		return
	}
	if err := pg.RevokeAPIToken(id); err != nil {
		if errors.Is(err, db.ErrAPITokenNotFound) {
			writeError(w, 404, err)
		} else {
			writeError(w, 500, err)
		}
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "revoked": true})
}
