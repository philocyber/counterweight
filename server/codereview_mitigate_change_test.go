package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/locale"
)

// Publishing a fix for a reviewed change pushes nothing unless the reviewed head is
// already in the upstream repository, opens the pull request against the change's base
// branch, and does not leave the pushed branch behind when the pull request is refused.
func TestMitigationPublishForChangeReview(t *testing.T) {
	f := newMitigationFixture(t, map[string]string{
		"app/db.py":        "q = 'SELECT * FROM t WHERE id=%s'\nparams = (uid,)\n",
		"tests/test_db.py": "def test_uid_is_bound(): assert True\n",
	})
	run, _, err := f.s.startFindingMitigation(locale.WithLang(t.Context(), locale.En), f.fid, false)
	if err != nil {
		t.Fatal(err)
	}
	if m := f.waitState(t, run.ID, "ready", "blocked", "failed", "rejected", "review_failed"); m.State != "ready" {
		t.Fatalf("state=%s", m.State)
	}
	finding, _ := f.s.m.pg.GetFinding(f.fid)
	setChange := func(c codesrc.ChangeSet) {
		raw, _ := json.Marshal(c)
		if _, err := f.s.m.pg.Exec(`INSERT INTO task_code_targets(task_id, repository, url, ref, commit_sha, mode, local_path, change)
VALUES ($1,'acme/fixture','https://github.com/acme/fixture','','','light',$2,$3::jsonb)
ON CONFLICT (task_id) DO UPDATE SET local_path=EXCLUDED.local_path, change=EXCLUDED.change`, *finding.TaskID, f.clone, string(raw)); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	var calls []string
	var prBody map[string]any
	headRepo, prStatus := "acme/fixture", 200
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /user":
			w.Header().Set("github-authentication-token-expiration", "2099-01-01 00:00:00 UTC")
			fmt.Fprint(w, `{"login":"me","id":42}`)
		case "GET /repos/acme/fixture":
			fmt.Fprint(w, `{"default_branch":"main","permissions":{"push":true}}`)
		case "GET /repos/acme/fixture/pulls/7":
			fmt.Fprintf(w, `{"head":{"repo":{"full_name":%q}}}`, headRepo)
		case "GET /repos/acme/fixture/branches/release/1.x":
			fmt.Fprint(w, `{}`)
		case "GET /repos/acme/fixture/pulls":
			fmt.Fprint(w, `[]`)
		case "POST /repos/acme/fixture/pulls":
			_ = json.NewDecoder(r.Body).Decode(&prBody)
			if prStatus != 200 {
				w.WriteHeader(prStatus)
				fmt.Fprint(w, `{"message":"refused"}`)
				return
			}
			fmt.Fprint(w, `{"html_url":"https://github.com/acme/fixture/pull/9"}`)
		default:
			if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/repos/acme/fixture/git/refs/heads/") {
				w.WriteHeader(204)
				return
			}
			w.WriteHeader(404)
		}
	}))
	defer gh.Close()
	githubAPI = gh.URL
	pushes := 0
	pushFix = func(context.Context, string, codesrc.Repo, string, string) error { pushes++; return nil }
	if _, err := f.s.saveGitHubToken(t.Context(), "ghp_testtoken123"); err != nil {
		t.Fatal(err)
	}
	called := func(prefix string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range calls {
			if strings.HasPrefix(c, prefix) {
				return true
			}
		}
		return false
	}

	// A pull request from another repository: refused before anything is pushed.
	setChange(codesrc.ChangeSet{BaseRef: "release/1.x", HeadRef: "pull/7", PullNumber: 7, HeadCommit: strings.Repeat("a", 40)})
	headRepo = "stranger/fixture"
	if _, err := f.s.publishMitigation(t.Context(), run.ID); err == nil || pushes != 0 || called("POST") || called("DELETE") {
		t.Fatalf("fork pull request: err=%v pushes=%d calls=%v", err, pushes, calls)
	}
	// A range whose head is not a branch tip of the upstream: refused too.
	setChange(codesrc.ChangeSet{BaseRef: "release/1.x", HeadRef: strings.Repeat("b", 40), HeadCommit: strings.Repeat("b", 40)})
	if _, err := f.s.publishMitigation(t.Context(), run.ID); err == nil || pushes != 0 || called("POST") {
		t.Fatalf("loose head: err=%v pushes=%d calls=%v", err, pushes, calls)
	}
	// A base that is not a branch is refused before the push as well.
	headRepo = "acme/fixture"
	setChange(codesrc.ChangeSet{BaseRef: "gone", HeadRef: "pull/7", PullNumber: 7, HeadCommit: strings.Repeat("a", 40)})
	if _, err := f.s.publishMitigation(t.Context(), run.ID); err == nil || pushes != 0 {
		t.Fatalf("bad base: err=%v pushes=%d", err, pushes)
	}

	// Same-repository pull request: the pull request targets the change's base, and when
	// GitHub refuses it the pushed branch is deleted again.
	setChange(codesrc.ChangeSet{BaseRef: "release/1.x", HeadRef: "pull/7", PullNumber: 7, HeadCommit: strings.Repeat("a", 40)})
	prStatus = 422
	if _, err := f.s.publishMitigation(t.Context(), run.ID); err == nil {
		t.Fatal("published although the pull request was refused")
	}
	if pushes != 1 || prBody["base"] != "release/1.x" || !called("DELETE /repos/acme/fixture/git/refs/heads/") {
		t.Fatalf("pushes=%d base=%v calls=%v", pushes, prBody["base"], calls)
	}
	if m, _ := f.s.m.pg.GetFindingMitigation(t.Context(), run.ID); m == nil || m.PRURL != "" {
		t.Fatalf("a refused pull request was recorded: %+v", m)
	}
}

// A pull request that GitHub created although the call failed is the result of the
// publication, and the branch it carries is not deleted.
func TestMitigationPublishKeepsCreatedPullRequest(t *testing.T) {
	f := newMitigationFixture(t, map[string]string{
		"app/db.py":        "q = 'SELECT * FROM t WHERE id=%s'\nparams = (uid,)\n",
		"tests/test_db.py": "def test_uid_is_bound(): assert True\n",
	})
	run, _, err := f.s.startFindingMitigation(locale.WithLang(t.Context(), locale.En), f.fid, false)
	if err != nil {
		t.Fatal(err)
	}
	if m := f.waitState(t, run.ID, "ready", "blocked", "failed", "rejected", "review_failed"); m.State != "ready" {
		t.Fatalf("state=%s", m.State)
	}
	var mu sync.Mutex
	head, deleted := "", false
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /user":
			w.Header().Set("github-authentication-token-expiration", "2099-01-01 00:00:00 UTC")
			fmt.Fprint(w, `{"login":"me","id":42}`)
		case "GET /repos/acme/fixture":
			fmt.Fprint(w, `{"default_branch":"main","permissions":{"push":true}}`)
		case "POST /repos/acme/fixture/pulls":
			w.WriteHeader(502)
		case "GET /repos/acme/fixture/pulls":
			head = r.URL.Query().Get("head")
			fmt.Fprint(w, `[{"html_url":"https://github.com/acme/fixture/pull/9"}]`)
		default:
			deleted = deleted || r.Method == http.MethodDelete
			w.WriteHeader(404)
		}
	}))
	defer gh.Close()
	githubAPI = gh.URL
	pushFix = func(context.Context, string, codesrc.Repo, string, string) error { return nil }
	if _, err := f.s.saveGitHubToken(t.Context(), "ghp_testtoken123"); err != nil {
		t.Fatal(err)
	}
	url, err := f.s.publishMitigation(t.Context(), run.ID)
	mu.Lock()
	defer mu.Unlock()
	if err != nil || url != "https://github.com/acme/fixture/pull/9" || deleted || head != "acme:"+codesrc.FixBranchName(f.fid, run.ID) {
		t.Fatalf("url=%q err=%v deleted=%v head=%q", url, err, deleted, head)
	}
	if m, _ := f.s.m.pg.GetFindingMitigation(t.Context(), run.ID); m == nil || m.PRURL != url {
		t.Fatalf("the created pull request was not recorded: %+v", m)
	}
}
