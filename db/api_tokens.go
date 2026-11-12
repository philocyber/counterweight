package db

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/locale"
)

// API tokens let automation (a CI workflow) start and read reviews without a
// console session. Only the SHA-256 of a token is stored: tokens are 256 bits
// from a CSPRNG, so there is nothing to brute-force and a slow password hash
// would only cost time on every request. The token itself is shown once.

// API token scopes. A token reaches only the automation endpoints its scopes allow.
const (
	ScopeReviewsRead  = "reviews:read"
	ScopeReviewsWrite = "reviews:write"
)

const (
	MaxAPITokens       = 50
	maxAPITokenNameLen = 100
	// touchInterval is how stale last_used_at must be before a request writes it.
	touchInterval = 5 * time.Minute
)

var (
	ErrAPITokenNotFound = locale.NewError("API token not found")
	ErrAPITokenName     = locale.NewError("The token needs a name of at most 100 characters")
	ErrAPITokenScopes   = locale.NewError("Choose at least one scope: reviews:read or reviews:write")
	ErrAPITokenExpiry   = locale.NewError("The expiry must be in the future")
	ErrAPITokenLimit    = locale.NewError("Too many active API tokens; revoke one first")
)

// APIToken is a token's metadata. The secret and its hash never leave the database layer.
type APIToken struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	Hint       string     `json:"hint"` // the first characters, to tell tokens apart
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`

	hash string // set by LookupAPIToken only
}

// HashMatches compares the stored hash with hash in constant time. It is true
// only for a token returned by LookupAPIToken.
func (t *APIToken) HashMatches(hash string) bool {
	return t.hash != "" && subtle.ConstantTimeCompare([]byte(t.hash), []byte(hash)) == 1
}

// Active reports whether the token may authenticate at now.
func (t *APIToken) Active(now time.Time) bool {
	return t.RevokedAt == nil && (t.ExpiresAt == nil || now.Before(*t.ExpiresAt))
}

// HasScope reports whether the token carries scope.
func (t *APIToken) HasScope(scope string) bool {
	for _, s := range t.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// NormalizeAPITokenScopes validates, deduplicates and orders scopes.
func NormalizeAPITokenScopes(in []string) ([]string, error) {
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != ScopeReviewsRead && s != ScopeReviewsWrite {
			return nil, ErrAPITokenScopes
		}
		seen[s] = true
	}
	var out []string
	for _, s := range []string{ScopeReviewsRead, ScopeReviewsWrite} {
		if seen[s] {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, ErrAPITokenScopes
	}
	return out, nil
}

const apiTokenCols = `id, name, scopes::text, hint, created_at, expires_at, last_used_at, revoked_at`

func scanAPIToken(row interface{ Scan(...any) error }, extra ...any) (*APIToken, error) {
	var t APIToken
	var scopes string
	dest := append([]any{&t.ID, &t.Name, &scopes, &t.Hint, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	// Scopes are plain words; the array literal is "{a,b}".
	if inner := strings.Trim(scopes, "{}"); inner != "" {
		t.Scopes = strings.Split(inner, ",")
	}
	return &t, nil
}

// CreateAPIToken stores a token by the hash of its secret.
func (d *DB) CreateAPIToken(name string, scopes []string, hash, hint string, expires *time.Time) (*APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxAPITokenNameLen {
		return nil, ErrAPITokenName
	}
	scopes, err := NormalizeAPITokenScopes(scopes)
	if err != nil {
		return nil, err
	}
	if expires != nil && !expires.After(time.Now()) {
		return nil, ErrAPITokenExpiry
	}
	var active int
	if err := d.QueryRow(`SELECT count(*) FROM api_tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now())`).Scan(&active); err != nil {
		return nil, err
	}
	if active >= MaxAPITokens {
		return nil, ErrAPITokenLimit
	}
	return scanAPIToken(d.QueryRow(`INSERT INTO api_tokens(name, token_hash, hint, scopes, expires_at)
VALUES ($1,$2,$3,string_to_array($4,','),$5) RETURNING `+apiTokenCols, name, hash, hint, strings.Join(scopes, ","), expires))
}

// ListAPITokens returns every token, newest first, revoked and expired ones included.
func (d *DB) ListAPITokens() ([]*APIToken, error) {
	rows, err := d.Query(`SELECT ` + apiTokenCols + ` FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIToken{}
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken revokes a token; revoking twice keeps the first time.
func (d *DB) RevokeAPIToken(id int64) error {
	res, err := d.Exec(`UPDATE api_tokens SET revoked_at=COALESCE(revoked_at, now()) WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAPITokenNotFound
	}
	return nil
}

// LookupAPIToken finds a token by the hash of its secret, or returns nil.
func (d *DB) LookupAPIToken(hash string) (*APIToken, error) {
	var stored string
	t, err := scanAPIToken(d.QueryRow(`SELECT `+apiTokenCols+`, token_hash FROM api_tokens WHERE token_hash=$1`, hash), &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if t != nil {
		t.hash = stored
	}
	return t, err
}

// TouchAPIToken records a use. It writes only when the last recorded use is
// older than touchInterval, so a polling client does not write on every request.
func (d *DB) TouchAPIToken(id int64) error {
	_, err := d.Exec(`UPDATE api_tokens SET last_used_at=now()
WHERE id=$1 AND (last_used_at IS NULL OR last_used_at < now() - make_interval(secs => $2))`, id, touchInterval.Seconds())
	return err
}
