package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/report"
)

// After a fix's pull request is merged, the verifier re-reads the merged code. It
// waits for the operator unless the task verifies automatically; a "gone" verdict
// marks the finding fixed.
func TestMitigationRecheckAfterMerge(t *testing.T) {
	f := newMitigationFixture(t, map[string]string{
		"app/db.py":        "q = 'SELECT * FROM t WHERE id=%s'\nparams = (uid,)\n",
		"tests/test_db.py": "def test_uid_is_bound(): assert True\n",
	})
	f.recheckVerdict = "rejected"
	pg := f.s.m.pg
	ctx := locale.WithLang(t.Context(), locale.En)
	run, _, err := f.s.startFindingMitigation(ctx, f.fid, false)
	if err != nil {
		t.Fatal(err)
	}
	f.waitState(t, run.ID, "ready")

	const mergeSHA = "0123456789abcdef0123456789abcdef01234567"
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /user":
			fmt.Fprint(w, `{"login":"me","id":42}`)
		case "GET /repos/acme/fixture/pulls/7":
			fmt.Fprint(w, `{"state":"closed","merged":true,"merge_commit_sha":"`+mergeSHA+`"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer gh.Close()
	githubAPI = gh.URL
	if _, err := f.s.saveGitHubToken(t.Context(), "ghp_testtoken123"); err != nil {
		t.Fatal(err)
	}
	// The merged code: the review clone's commit with the fix applied.
	addMergedWorktree = func(ctx context.Context, cloneDir, dest string, repo codesrc.Repo, sha, token string) error {
		if sha != mergeSHA || repo.Slug() != "acme/fixture" || token != "ghp_testtoken123" {
			return fmt.Errorf("unexpected fetch %s %s %q", repo.Slug(), sha, token)
		}
		runGitT(t, cloneDir, "worktree", "add", "--detach", "--", dest, "HEAD")
		return os.WriteFile(filepath.Join(dest, "app", "db.py"), []byte("q = 'SELECT * FROM t WHERE id=%s'\nparams = (uid,)\n"), 0o644)
	}
	if err := pg.MarkMitigationPublished(ctx, run.ID, "https://github.com/acme/fixture/pull/7"); err != nil {
		t.Fatal(err)
	}

	// Manual task: the merge is noticed, nothing re-checks on its own.
	m, err := f.s.refreshMitigationPR(ctx, run.ID)
	if err != nil || m.State != "merged" {
		t.Fatalf("refresh: %+v %v", m, err)
	}
	time.Sleep(300 * time.Millisecond)
	if m, _ = pg.GetFindingMitigation(ctx, run.ID); m.Recheck != nil {
		t.Fatalf("manual task re-checked by itself: %+v", m.Recheck)
	}

	// The operator's re-check reads the merged code and marks the finding fixed.
	rc, created, err := f.s.startMitigationRecheck(ctx, run.ID)
	if err != nil || !created || rc.Kind != db.RetestKindRecheck {
		t.Fatalf("recheck: %+v created=%v err=%v", rc, created, err)
	}
	waitRun(t, pg, rc.ID)
	if m, _ = pg.GetFindingMitigation(ctx, run.ID); m.Recheck == nil || m.Recheck.Status != "completed" || m.Recheck.Verdict != "rejected" {
		t.Fatalf("recheck after run: %+v", m.Recheck)
	}
	if fd, _ := pg.GetFinding(f.fid); fd.Status != db.FindingFixed {
		t.Fatalf("finding status=%s, want fixed", fd.Status)
	}
	if commit, _ := pg.RecheckCommit(ctx, rc.ID); commit != mergeSHA {
		t.Fatalf("recheck commit=%q", commit)
	}
	// A later turn in the re-check conversation is told that it is over.
	res, err := f.s.findingVerificationTools()[0].Call(intercept.WithConvID(t.Context(), *m.Recheck.ConversationID), json.RawMessage(`{}`), nil)
	if err != nil || res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, `"run_ended":"This run has ended (status: completed)`) {
		t.Fatalf("ended re-check context does not say so: %+v %v", res, err)
	}
	// The report sees the whole path: verified, merged, validated after merge.
	fd, _ := pg.GetFinding(f.fid)
	lc, err := f.s.findingLifecycles(ctx, []*db.DBFinding{fd})
	if l := lc[f.fid]; err != nil || l.Stage() != report.StageValidated || l.PRURL != "https://github.com/acme/fixture/pull/7" || l.Verification != "confirmed" {
		t.Fatalf("lifecycle=%+v err=%v", lc[f.fid], err)
	}

	// With automatic verification on, a merge starts the re-check by itself.
	if err := pg.SetTaskAutomation(*fd.TaskID, db.TaskAutomation{Verify: true}); err != nil {
		t.Fatal(err)
	}
	f.s.afterPullRequestMerged(run.ID, locale.En)
	if m, _ = pg.GetFindingMitigation(ctx, run.ID); m.Recheck == nil || m.Recheck.ID == rc.ID {
		t.Fatalf("automatic recheck did not start: %+v", m.Recheck)
	}
	waitRun(t, pg, m.Recheck.ID)

	// An unmerged fix cannot be re-checked.
	if _, _, _, err := pg.CreateMitigationRecheck(ctx, rc.ID); err == nil {
		t.Fatal("re-checked something that is not a merged fix")
	}
}

func waitRun(t *testing.T, pg *db.DB, id int64) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if r, _ := pg.GetFindingRetest(t.Context(), id); r != nil && r.Status != "pending" && r.Status != "running" {
			return
		}
	}
	t.Fatalf("run %d never finished", id)
}
