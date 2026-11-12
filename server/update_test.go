package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/selfupdate"
)

// releaseCache protects the unauthenticated GitHub quota of 60 requests/hour/IP.
// The update indicator checks on each full page load; a broken cache lets a few
// browser tabs exhaust the quota and prevent a later intentional update check.

func newTestCache(fetch func(context.Context, *http.Client) (*selfupdate.Release, error)) *releaseCache {
	return &releaseCache{fetch: fetch}
}

func TestReleaseCacheServesFromCache(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	for range 5 {
		rel, err := c.get(t.Context(), nil, false)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if rel.TagName != "v0.3.8" {
			t.Fatalf("TagName = %q", rel.TagName)
		}
	}
	if calls != 1 {
		t.Errorf("Five queries must fetch upstream once, got %d fetches", calls)
	}
}

func TestReleaseCacheForceBypasses(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	// An explicit update check must fetch fresh results instead of hiding a newly released version until cache expiry.
	if _, err := c.get(t.Context(), nil, true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("force must bypass cache; want two upstream fetches, got %d", calls)
	}
}

func TestReleaseCacheExpiresAfterTTL(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	// Move the stored timestamp just past expiry to simulate the TTL elapsing.
	c.at = time.Now().Add(-releaseTTL - time.Second)
	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("Expired TTL must trigger a new fetch; want two, got %d", calls)
	}
}

func TestReleaseCacheUsesShorterTTLForErrors(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return nil, errors.New("github unreachable")
	})

	if _, err := c.get(t.Context(), nil, false); err == nil {
		t.Fatal("Expected an error")
	}
	// Cache failures briefly so every page load does not wait through another timeout while GitHub is unreachable.
	if _, err := c.get(t.Context(), nil, false); err == nil {
		t.Fatal("Expected an error")
	}
	if calls != 1 {
		t.Errorf("Errors must be cached briefly; want one upstream fetch, got %d", calls)
	}

	// Error TTL must be shorter than success TTL so recovery is detected promptly.
	if releaseErrTTL >= releaseTTL {
		t.Fatalf("Error TTL (%v) must be shorter than success TTL (%v)", releaseErrTTL, releaseTTL)
	}
	c.at = time.Now().Add(-releaseErrTTL - time.Second)
	if _, err := c.get(t.Context(), nil, false); err == nil {
		t.Fatal("Expected an error")
	}
	if calls != 2 {
		t.Errorf("Expired error TTL must retry; want two fetches, got %d", calls)
	}
}

func TestReleaseCacheDoesNotPoisonOnCallerCancel(t *testing.T) {
	good := &selfupdate.Release{TagName: "v0.3.8"}
	c := newTestCache(func(ctx context.Context, _ *http.Client) (*selfupdate.Release, error) {
		return good, nil
	})
	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}

	// Closing a browser tab cancels its request, which does not imply a GitHub failure.
	// Caching cancellation would incorrectly fail every visitor for the next thirty minutes.
	c.fetch = func(ctx context.Context, _ *http.Client) (*selfupdate.Release, error) {
		return nil, ctx.Err()
	}
	c.at = time.Now().Add(-releaseTTL - time.Second) // Expire the cache to force an upstream fetch.

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.get(ctx, nil, false); err == nil {
		t.Fatal("Caller cancellation must return its error")
	}

	// Cancellation must leave the cache unchanged: no cancellation error and the last
	// successful result still intact.
	if c.err != nil {
		t.Fatalf("Cancellation error must not enter cache, got %v", c.err)
	}
	if c.rel == nil || c.rel.TagName != "v0.3.8" {
		t.Fatalf("Cache must retain the last successful result, got %+v", c.rel)
	}

	// Cancellation produced no fresh data, so the next visitor should fetch again
	// and receive a normal result unaffected by the cancelled request.
	c.fetch = func(context.Context, *http.Client) (*selfupdate.Release, error) {
		return good, nil
	}
	rel, err := c.get(t.Context(), nil, false)
	if err != nil {
		t.Fatalf("Normal request after cancellation must succeed: %v", err)
	}
	if rel == nil || rel.TagName != "v0.3.8" {
		t.Fatalf("Expected a successful result, got %+v", rel)
	}
}
