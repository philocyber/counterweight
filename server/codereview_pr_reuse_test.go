package server

import (
	"os"
	"path/filepath"
	"testing"
)

// A review is reused only for the same change: the same head under another base or
// another pull request is a different change and gets its own review.
func TestStartReviewReuseRequiresSameChange(t *testing.T) {
	f := newPRFixture(t)
	sess := f.session(t)
	prGit(t, f.origin, "checkout", "-q", "-b", "stable", "main")
	if err := os.WriteFile(filepath.Join(f.origin, "lib", "old.py"), []byte("x\ny\nz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prGit(t, f.origin, "commit", "-q", "-am", "stable moves on")
	prGit(t, f.origin, "checkout", "-q", "main")

	start := func(body map[string]any, wantCode int, wantID string) string {
		t.Helper()
		w := f.do("POST", "/api/reviews", sess, body)
		got := decodeBody(t, w)
		id, _ := got["id"].(string)
		if w.Code != wantCode || (wantID != "" && id != wantID) || (wantID == "" && wantCode == 201 && id == "") {
			t.Fatalf("%v: %d %v", body, w.Code, got)
		}
		if w.Code == 201 {
			f.cleanupTask(t, id)
		}
		return id
	}

	pull := start(map[string]any{"repository": "acme/example", "pull_number": 7, "base": "main", "head": f.head, "depth": "light"}, 201, "")
	// Same change again: reused, even though the repeat names no base.
	start(map[string]any{"repository": "acme/example", "pull_number": 7, "head": f.head}, 200, pull)
	start(map[string]any{"repository": "acme/example", "pull_number": 7, "base": "main", "head": f.head}, 200, pull)
	// The same head as another pull request, or against another base, is not the same change.
	if other := start(map[string]any{"repository": "acme/example", "pull_number": 8, "base": "main", "head": f.head}, 201, ""); other == pull {
		t.Fatal("a different pull request reused the review")
	}
	if other := start(map[string]any{"repository": "acme/example", "pull_number": 7, "base": "stable", "head": f.head}, 201, ""); other == pull {
		t.Fatal("a different base reused the review")
	}
	// With both reviews stored, a request that names no base gets the one for the pull
	// request's own base, which GitHub reports as main.
	start(map[string]any{"repository": "acme/example", "pull_number": 7, "head": f.head}, 200, pull)
	// A plain base..head request is not a pull request review either.
	if other := start(map[string]any{"repository": "acme/example", "base": "main", "head": f.head}, 201, ""); other == pull {
		t.Fatal("a non-pull request reused a pull request review")
	}
}
