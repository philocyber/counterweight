package db

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAPITokenLifecycle(t *testing.T) {
	d := assessmentDB(t)
	hash := strings.Repeat("ab", 28) + time.Now().Format("150405.000000")
	tok, err := d.CreateAPIToken("  ci for acme  ", []string{ScopeReviewsWrite, ScopeReviewsRead, ScopeReviewsRead}, hash, "cwt_abcd", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM api_tokens WHERE id=$1`, tok.ID) })
	if tok.Name != "ci for acme" || strings.Join(tok.Scopes, ",") != "reviews:read,reviews:write" || tok.LastUsedAt != nil || tok.RevokedAt != nil {
		t.Fatalf("created: %+v", tok)
	}

	// Only the hash is stored: the table has no column that could hold the secret.
	var cols string
	if err := d.QueryRow(`SELECT string_agg(column_name, ',') FROM information_schema.columns WHERE table_name='api_tokens'`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cols, "secret") || !strings.Contains(cols, "token_hash") {
		t.Fatalf("columns: %s", cols)
	}

	got, err := d.LookupAPIToken(hash)
	if err != nil || got == nil || got.ID != tok.ID || !got.HasScope(ScopeReviewsWrite) {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	if none, err := d.LookupAPIToken("0000"); err != nil || none != nil {
		t.Fatalf("unknown hash: %+v %v", none, err)
	}

	// Last use is written once, then not again within the interval.
	if err := d.TouchAPIToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	first, _ := d.LookupAPIToken(hash)
	if first.LastUsedAt == nil {
		t.Fatal("last used not recorded")
	}
	time.Sleep(20 * time.Millisecond)
	if err := d.TouchAPIToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	second, _ := d.LookupAPIToken(hash)
	if !second.LastUsedAt.Equal(*first.LastUsedAt) {
		t.Fatalf("a second use inside the interval rewrote last_used_at: %v -> %v", first.LastUsedAt, second.LastUsedAt)
	}
	if _, err := d.Exec(`UPDATE api_tokens SET last_used_at=now() - interval '1 hour' WHERE id=$1`, tok.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.TouchAPIToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	third, _ := d.LookupAPIToken(hash)
	if time.Since(*third.LastUsedAt) > time.Minute {
		t.Fatalf("a stale last_used_at was not refreshed: %v", third.LastUsedAt)
	}

	// Revocation keeps its first time and is idempotent.
	if err := d.RevokeAPIToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	r1, _ := d.LookupAPIToken(hash)
	if r1.RevokedAt == nil || r1.Active(time.Now()) {
		t.Fatalf("revoked: %+v", r1)
	}
	if err := d.RevokeAPIToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	r2, _ := d.LookupAPIToken(hash)
	if !r2.RevokedAt.Equal(*r1.RevokedAt) {
		t.Fatal("revoking again moved the revocation time")
	}
	if err := d.RevokeAPIToken(-5); !errors.Is(err, ErrAPITokenNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	list, err := d.ListAPITokens()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range list {
		found = found || l.ID == tok.ID
	}
	if !found {
		t.Fatal("revoked token missing from the list")
	}
}

func TestAPITokenValidationAndExpiry(t *testing.T) {
	d := assessmentDB(t)
	h := func(s string) string { return strings.Repeat("cd", 28) + s + time.Now().Format("150405.000000") }
	if _, err := d.CreateAPIToken("", []string{ScopeReviewsRead}, h("1"), "", nil); !errors.Is(err, ErrAPITokenName) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := d.CreateAPIToken(strings.Repeat("n", 101), []string{ScopeReviewsRead}, h("2"), "", nil); !errors.Is(err, ErrAPITokenName) {
		t.Fatalf("long name: %v", err)
	}
	for _, bad := range [][]string{nil, {}, {"admin"}, {"reviews:read", "settings:write"}} {
		if _, err := d.CreateAPIToken("x", bad, h("3"), "", nil); !errors.Is(err, ErrAPITokenScopes) {
			t.Fatalf("scopes %v: %v", bad, err)
		}
	}
	past := time.Now().Add(-time.Hour)
	if _, err := d.CreateAPIToken("x", []string{ScopeReviewsRead}, h("4"), "", &past); !errors.Is(err, ErrAPITokenExpiry) {
		t.Fatalf("past expiry: %v", err)
	}
	soon := time.Now().Add(time.Hour)
	tok, err := d.CreateAPIToken("x", []string{ScopeReviewsRead}, h("5"), "", &soon)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM api_tokens WHERE id=$1`, tok.ID) })
	if !tok.Active(time.Now()) || tok.Active(time.Now().Add(2*time.Hour)) {
		t.Fatalf("expiry: %+v", tok)
	}
}
