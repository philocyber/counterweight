package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

const (
	// sessionCacheTTL bounds how long a validated session id is trusted without
	// asking the database again. Revocation through this server evicts at once.
	sessionCacheTTL = 30 * time.Second
	// streamTicketTTL is how long a stream ticket can be redeemed.
	streamTicketTTL  = 60 * time.Second
	maxStreamTickets = 256
	// legacyRevokedKey holds the unix time up to which tokens issued without a
	// session id are refused (set when such a token signs out, and by sign-out everywhere).
	legacyRevokedKey  = "auth.legacy_tokens_revoked_before"
	maxUserAgentLabel = 80
)

// streamRecheckInterval is how often an open stream re-validates its credential.
var streamRecheckInterval = 5 * time.Second

// authInfo is what a validated credential proves: the password generation it was
// issued for, its session (empty for tokens issued before sessions existed) and
// its lifetime. Streams keep it to re-validate while they stay open.
type authInfo struct {
	sid     string
	version string
	issued  time.Time
	expires time.Time
}

type streamTicket struct {
	info    authInfo
	expires time.Time
	// path is the full request path the ticket opens and task the value of the
	// task query parameter it was issued for (empty when the stream has none).
	path string
	task string
}

// sessionState is usable as a zero value. Sessions live in PostgreSQL; the cache
// only avoids a query per request, and tickets are short-lived and memory-only.
type sessionState struct {
	mu      sync.Mutex
	live    map[string]time.Time
	tickets map[[32]byte]streamTicket
}

func randomID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// userAgentLabel keeps a short printable description of the client.
func userAgentLabel(r *http.Request) string {
	label := strings.Map(func(c rune) rune {
		if unicode.IsPrint(c) {
			return c
		}
		return -1
	}, strings.ToValidUTF8(r.UserAgent(), ""))
	if runes := []rune(label); len(runes) > maxUserAgentLabel {
		label = string(runes[:maxUserAgentLabel])
	}
	return label
}

// setLive caches a validated session and drops stale cache entries. Callers hold mu.
func (st *sessionState) setLive(sid string, now time.Time) {
	if st.live == nil {
		st.live = map[string]time.Time{}
	}
	for k, until := range st.live {
		if !until.After(now) {
			delete(st.live, k)
		}
	}
	st.live[sid] = now.Add(sessionCacheTTL)
}

// createSession records a new session and prunes expired ones.
func (s *Server) createSession(pg *db.DB, label string, expires time.Time) (string, error) {
	sid, err := randomID(18)
	if err != nil {
		return "", err
	}
	_, _ = pg.Exec(`DELETE FROM auth_sessions WHERE expires_at < now()`)
	if _, err := pg.Exec(`INSERT INTO auth_sessions(id, expires_at, user_agent) VALUES ($1, $2, $3)`, sid, expires, label); err != nil {
		return "", err
	}
	s.sessions.mu.Lock()
	s.sessions.setLive(sid, time.Now())
	s.sessions.mu.Unlock()
	return sid, nil
}

// sessionLive reports whether the session exists and has not expired. A cache
// hit costs no query; a miss validates against the database and refreshes
// last_seen, which therefore advances at most every sessionCacheTTL.
func (s *Server) sessionLive(pg *db.DB, sid string, now time.Time) (bool, error) {
	s.sessions.mu.Lock()
	until, hit := s.sessions.live[sid]
	s.sessions.mu.Unlock()
	if hit && until.After(now) {
		return true, nil
	}
	res, err := pg.Exec(`UPDATE auth_sessions SET last_seen=now() WHERE id=$1 AND expires_at > now()`, sid)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	if n == 0 {
		delete(s.sessions.live, sid)
		return false, nil
	}
	s.sessions.setLive(sid, now)
	return true, nil
}

func (s *Server) revokeSession(pg *db.DB, sid string) error {
	_, err := pg.Exec(`DELETE FROM auth_sessions WHERE id=$1`, sid)
	s.sessions.mu.Lock()
	delete(s.sessions.live, sid)
	s.sessions.mu.Unlock()
	return err
}

// revokeAllSessions deletes every session and evicts the cache.
func (s *Server) revokeAllSessions(pg *db.DB) error {
	_, err := pg.Exec(`DELETE FROM auth_sessions`)
	s.sessions.mu.Lock()
	s.sessions.live = nil
	s.sessions.mu.Unlock()
	return err
}

// revokeLegacyTokens refuses every token issued without a session id up to now.
func (s *Server) revokeLegacyTokens(pg *db.DB) error {
	return pg.SetSetting(legacyRevokedKey, strconv.FormatInt(time.Now().Unix(), 10))
}

// authorize checks a parsed credential against current state. hash is the
// stored password hash. It returns 0 when the credential is accepted, 401 when
// it is not, and 503 when the database cannot answer.
func (s *Server) authorize(pg *db.DB, info authInfo, hash string) int {
	now := time.Now()
	if !now.Before(info.expires) || hash == "" || !hmac.Equal([]byte(info.version), []byte(sessionVersion(s.jwtKey, hash))) {
		return http.StatusUnauthorized
	}
	if info.sid != "" {
		live, err := s.sessionLive(pg, info.sid, now)
		if err != nil {
			return http.StatusServiceUnavailable
		}
		if !live {
			return http.StatusUnauthorized
		}
		return 0
	}
	// Token issued before sessions existed: valid until it expires, the password
	// changes, or a sign-out moved the cutoff past its issue time.
	raw, ok, err := pg.GetSetting(legacyRevokedKey)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	if ok {
		if cutoff, err := strconv.ParseInt(raw, 10, 64); err == nil && info.issued.Unix() <= cutoff {
			return http.StatusUnauthorized
		}
	}
	return 0
}

// recheck re-validates an authenticated credential, for streams that outlive
// the request that opened them.
func (s *Server) recheck(info authInfo) int {
	if s.m == nil || s.m.pg == nil {
		return http.StatusServiceUnavailable
	}
	hash, _, err := s.m.pg.GetSetting(authPassKey)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	return s.authorize(s.m.pg, info, hash)
}

// authenticate validates a bearer token against its signature, the password
// generation and the session store. It writes the error response itself and
// returns the stored password hash on success.
func (s *Server) authenticate(w http.ResponseWriter, token string) (authInfo, string, bool) {
	unauthorized := func() (authInfo, string, bool) {
		writeErr(w, http.StatusUnauthorized, locale.Text(responseLanguage(w), "Invalid or expired token"))
		return authInfo{}, "", false
	}
	if !verifyJWT(token, s.jwtKey) {
		return unauthorized()
	}
	pg := s.pg(w)
	if pg == nil {
		return authInfo{}, "", false
	}
	hash, _, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, locale.Text(responseLanguage(w), "Authentication request unavailable"))
		return authInfo{}, "", false
	}
	claims, ok := parseCredentialClaims(token, s.jwtKey)
	if !ok {
		return unauthorized()
	}
	info := authInfo{sid: claims.SessionID, version: claims.Version, expires: claims.ExpiresAt.Time}
	if claims.IssuedAt != nil {
		info.issued = claims.IssuedAt.Time
	}
	switch s.authorize(pg, info, hash) {
	case 0:
		return info, hash, true
	case http.StatusServiceUnavailable:
		writeErr(w, http.StatusServiceUnavailable, locale.Text(responseLanguage(w), "Authentication request unavailable"))
		return authInfo{}, "", false
	}
	return unauthorized()
}

// issueSession creates a session and the token bound to it.
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, pg *db.DB, hash string) (string, bool) {
	expires := time.Now().Add(jwtTTL)
	sid, err := s.createSession(pg, userAgentLabel(r), expires)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, locale.Text(responseLanguage(w), "Authentication request unavailable"))
		return "", false
	}
	tok, err := signSessionJWT(s.jwtKey, hash, sid, expires)
	if err != nil {
		writeErr(w, 500, locale.Text(responseLanguage(w), "Token generation failed"))
		return "", false
	}
	return tok, true
}

// POST /api/auth/logout revokes the caller's own session.
func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	info, _, ok := s.authenticate(w, extractToken(r))
	if !ok {
		return
	}
	var err error
	if info.sid != "" {
		err = s.revokeSession(s.m.pg, info.sid)
	} else {
		// A token without a session id cannot be revoked alone.
		err = s.revokeLegacyTokens(s.m.pg)
	}
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, locale.Text(responseLanguage(w), "Authentication request unavailable"))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/auth/logout-all revokes every session, including the caller's.
func (s *Server) authLogoutAll(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.authenticate(w, extractToken(r)); !ok {
		return
	}
	err := s.revokeLegacyTokens(s.m.pg)
	if err == nil {
		err = s.revokeAllSessions(s.m.pg)
	}
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, locale.Text(responseLanguage(w), "Authentication request unavailable"))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/auth/stream-ticket returns a single-use credential for one event
// stream, so the long-lived token never appears in a URL.
func (s *Server) authStreamTicket(w http.ResponseWriter, r *http.Request) {
	info, _, ok := s.authenticate(w, extractToken(r))
	if !ok {
		return
	}
	// The ticket is bound to one stream: the body names its request path, with
	// its query string when it has one.
	var req struct {
		Path string `json:"path"`
	}
	if !decodeAuthRequest(w, r, &req) {
		return
	}
	streamPath, task, valid := parseStreamTicketPath(req.Path)
	if !valid {
		writeErr(w, http.StatusBadRequest, locale.Text(responseLanguage(w), "A valid stream path is required"))
		return
	}
	ticket, err := randomID(32)
	if err != nil {
		writeErr(w, 500, locale.Text(responseLanguage(w), "Token generation failed"))
		return
	}
	now := time.Now()
	st := &s.sessions
	st.mu.Lock()
	for k, t := range st.tickets {
		if !t.expires.After(now) {
			delete(st.tickets, k)
		}
	}
	if len(st.tickets) >= maxStreamTickets {
		st.mu.Unlock()
		writeErr(w, http.StatusTooManyRequests, locale.Text(responseLanguage(w), "Too many pending stream tickets"))
		return
	}
	if st.tickets == nil {
		st.tickets = map[[32]byte]streamTicket{}
	}
	st.tickets[sha256.Sum256([]byte(ticket))] = streamTicket{info: info, expires: now.Add(streamTicketTTL), path: streamPath, task: task}
	st.mu.Unlock()
	writeJSON(w, 200, map[string]any{"ticket": ticket, "expires_in": int(streamTicketTTL / time.Second)})
}

// parseStreamTicketPath turns the stream path a client names (the full request
// path, query string included) into the path and task value a ticket is bound
// to. It reports false for anything that is not an event stream.
func parseStreamTicketPath(raw string) (path, task string, ok bool) {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Fragment != "" {
		return "", "", false
	}
	path = u.Path
	if !isStreamPath(path) {
		return "", "", false
	}
	return path, u.Query().Get("task"), true
}

// redeemTicket consumes a ticket: it works once, whether or not it is still
// fresh, and only for the stream and task it was issued for.
func (s *Server) redeemTicket(ticket string, r *http.Request) (authInfo, bool) {
	key := sha256.Sum256([]byte(ticket))
	s.sessions.mu.Lock()
	t, ok := s.sessions.tickets[key]
	delete(s.sessions.tickets, key)
	s.sessions.mu.Unlock()
	if !ok || !time.Now().Before(t.expires) {
		return authInfo{}, false
	}
	if r.URL.Path != t.path || r.URL.Query().Get("task") != t.task {
		return authInfo{}, false
	}
	return t.info, true
}

// isStreamRoute lists the event-stream endpoints, the only routes a ticket opens.
func isStreamRoute(r *http.Request) bool {
	return r.Method == http.MethodGet && isStreamPath(r.URL.Path)
}

// isStreamPath reports whether p is the full path of an event-stream endpoint.
func isStreamPath(p string) bool {
	switch p {
	case "/api/logs/stream", "/api/update/stream", "/api/exploration/activity/stream", "/api/recontools/stream":
		return true
	default:
		return strings.HasPrefix(p, "/api/side-questions/") && strings.HasSuffix(p, "/events") && strings.Count(p, "/") == 4
	}
}

// guardStream cancels the request context when the credential stops being
// valid (session revoked, password changed, token expired), which ends the
// stream handler. Transient database errors do not close a stream.
func (s *Server) guardStream(r *http.Request, info authInfo) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	go func() {
		tick := time.NewTicker(streamRecheckInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if s.recheck(info) == http.StatusUnauthorized {
					cancel()
					return
				}
			}
		}
	}()
	return r.WithContext(ctx), cancel
}
