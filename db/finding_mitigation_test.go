package db

import (
	"errors"
	"testing"
)

// runToCompletion starts a freshly created run, stages verdict through record and seals it.
func runToCompletion(t *testing.T, d *DB, r *FindingRetest, convID int64, record func(int64, string, string, string) error, verdict string) {
	t.Helper()
	if _, err := d.StartFindingRetest(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if err := record(convID, verdict, "summary "+verdict, "evidence "+verdict); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishFindingRetest(r.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
}

func confirmByVerification(t *testing.T, d *DB, fid int64) {
	t.Helper()
	r, c, _, err := d.CreateFindingVerification(t.Context(), fid)
	if err != nil {
		t.Fatal(err)
	}
	runToCompletion(t, d, r, c.ID, func(cid int64, v, s, e string) error {
		return d.RecordFindingVerificationResult(t.Context(), cid, v, s, e)
	}, "confirmed")
}

func TestMitigationLifecycleToPublishedPR(t *testing.T) {
	d, fid := retestDB(t)
	if _, _, _, err := d.CreateFindingMitigation(t.Context(), fid, false); !errors.Is(err, ErrFindingNotConfirmed) {
		t.Fatalf("unverified finding: err=%v", err)
	}
	confirmByVerification(t, d, fid)

	run, conv, created, err := d.CreateFindingMitigation(t.Context(), fid, false)
	if err != nil || !created || run.Kind != RetestKindMitigate || conv.AgentKey != CodeFixerAgentKey {
		t.Fatalf("create fix: run=%+v conv=%+v created=%v err=%v", run, conv, created, err)
	}
	if m, _ := d.GetFindingMitigation(t.Context(), run.ID); m.State != "drafting" {
		t.Fatalf("state=%s want drafting", m.State)
	}
	if err := d.SetMitigationWorkspace(t.Context(), run.ID, "counterweight/fix-1", "abc123"); err != nil {
		t.Fatal(err)
	}
	// A fix verdict cannot be written by another run kind's tool.
	if _, err := d.StartFindingRetest(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordFindingVerificationResult(t.Context(), conv.ID, "confirmed", "s", "e"); err == nil {
		t.Fatal("verification verdict accepted on a fix run")
	}
	if err := d.RecordFindingPatch(t.Context(), conv.ID, "approve", "s", "e"); err == nil {
		t.Fatal("review verdict accepted as a fix verdict")
	}
	if err := d.RecordFindingPatch(t.Context(), conv.ID, "patched", "Parameterized the query", "app.py:12"); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishFindingRetest(run.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if f, _ := d.GetFinding(fid); f.Status != FindingConfirmed {
		t.Fatalf("a fix must not change triage: status=%s", f.Status)
	}
	if _, _, _, err := d.CreatePatchReview(t.Context(), run.ID); err == nil {
		t.Fatal("review allowed before the server stored a diff")
	}
	if err := d.SetMitigationDiff(t.Context(), run.ID, "diff --git a/app.py b/app.py\n", ""); err != nil {
		t.Fatal(err)
	}
	if m, _ := d.GetFindingMitigation(t.Context(), run.ID); m.State != "in_review" || m.Branch != "counterweight/fix-1" {
		t.Fatalf("mitigation=%+v want in_review", m)
	}

	review, rconv, created, err := d.CreatePatchReview(t.Context(), run.ID)
	if err != nil || !created || review.Kind != RetestKindPatchReview || rconv.AgentKey != PatchReviewerAgentKey {
		t.Fatalf("create review: %+v %+v %v %v", review, rconv, created, err)
	}
	m, run2, err := d.MitigationForConversation(t.Context(), rconv.ID)
	if err != nil || m == nil || m.Run.ID != run.ID || run2.ID != review.ID {
		t.Fatalf("review conversation not bound to its fix: m=%+v run=%+v err=%v", m, run2, err)
	}
	runToCompletion(t, d, review, rconv.ID, func(cid int64, v, s, e string) error {
		return d.RecordPatchReview(t.Context(), cid, v, s, e)
	}, "approve")
	if m, _ := d.GetFindingMitigation(t.Context(), run.ID); m.State != "ready" || m.Review == nil || m.Review.Verdict != "approve" {
		t.Fatalf("mitigation=%+v want ready", m)
	}

	if err := d.MarkMitigationPublished(t.Context(), run.ID, "https://github.com/o/r/pull/1"); err != nil {
		t.Fatal(err)
	}
	if err := d.MarkMitigationPublished(t.Context(), run.ID, "https://github.com/o/r/pull/2"); !errors.Is(err, ErrMitigationPublished) {
		t.Fatalf("second publish: %v", err)
	}
	if _, _, _, err := d.CreateFindingMitigation(t.Context(), fid, false); !errors.Is(err, ErrMitigationPublished) {
		t.Fatalf("new fix while a PR is open: %v", err)
	}
	if err := d.SetMitigationPRState(t.Context(), run.ID, "merged"); err != nil {
		t.Fatal(err)
	}
	list, err := d.ListFindingMitigations(t.Context(), fid)
	if err != nil || len(list) != 1 || list[0].State != "merged" || list[0].PRURL != "https://github.com/o/r/pull/1" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
}

func TestMitigationBlockedAndCannotFix(t *testing.T) {
	d, fid := retestDB(t)
	confirmByVerification(t, d, fid)
	for _, tc := range []struct{ verdict, blocked, want string }{
		{"patched", "changes .github/workflows/ci.yml", "blocked"},
		{"cannot_fix", "", "cannot_fix"},
	} {
		run, conv, _, err := d.CreateFindingMitigation(t.Context(), fid, false)
		if err != nil {
			t.Fatal(err)
		}
		runToCompletion(t, d, run, conv.ID, func(cid int64, v, s, e string) error {
			return d.RecordFindingPatch(t.Context(), cid, v, s, e)
		}, tc.verdict)
		if err := d.SetMitigationDiff(t.Context(), run.ID, "diff", tc.blocked); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := d.CreatePatchReview(t.Context(), run.ID); err == nil {
			t.Fatalf("%s fix was sent to review", tc.want)
		}
		if m, _ := d.GetFindingMitigation(t.Context(), run.ID); m.State != tc.want {
			t.Fatalf("state=%s want %s", m.State, tc.want)
		}
	}
}

func TestMitigationStateOrder(t *testing.T) {
	done := func(kind, verdict string) *FindingRetest {
		return &FindingRetest{Kind: kind, Status: "completed", Verdict: verdict}
	}
	for _, tc := range []struct {
		m    FindingMitigation
		want string
	}{
		{FindingMitigation{Run: &FindingRetest{Status: "running"}}, "drafting"},
		{FindingMitigation{Run: &FindingRetest{Status: "stopped"}}, "failed"},
		{FindingMitigation{Run: done(RetestKindMitigate, "patched"), Review: &FindingRetest{Status: "failed"}}, "review_failed"},
		{FindingMitigation{Run: done(RetestKindMitigate, "patched"), Review: done(RetestKindPatchReview, "changes_needed")}, "changes_needed"},
		{FindingMitigation{Run: done(RetestKindMitigate, "patched"), Review: done(RetestKindPatchReview, "reject")}, "rejected"},
		{FindingMitigation{Run: done(RetestKindMitigate, "patched"), Review: done(RetestKindPatchReview, "approve"), PRState: "closed"}, "pr_closed"},
	} {
		if got := MitigationState(&tc.m); got != tc.want {
			t.Errorf("state=%s want %s for %+v", got, tc.want, tc.m)
		}
	}
}
