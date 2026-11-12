package server

import (
	"errors"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// Code-review automation is manual by default: recording a finding verifies nothing
// and a confirmed verification starts no fix, until the task's switches say so.
func TestCodeReviewAutomationIsManualByDefault(t *testing.T) {
	f := newMitigationFixture(t, map[string]string{"app/db.py": "q = 1\n"})
	pg := f.s.m.pg
	finding, err := pg.GetFinding(f.fid)
	if err != nil || finding == nil || finding.TaskID == nil {
		t.Fatalf("finding=%+v err=%v", finding, err)
	}
	taskID := *finding.TaskID
	if a, err := pg.GetTaskAutomation(taskID); err != nil || a.Verify || a.Mitigate {
		t.Fatalf("default automation=%+v err=%v, want both off", a, err)
	}

	// A new finding of a manual task is not verified on its own.
	fresh, err := pg.AddFinding(taskID, 0, "xss", "Reflected XSS", "medium", "q echoed", "app/db.py:1", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.s.queueCodeReviewVerification(taskID, fresh)
	time.Sleep(300 * time.Millisecond)
	if runs, _ := pg.ListFindingRetests(fresh); len(runs) != 0 {
		t.Fatalf("manual task verified a finding by itself: %+v", runs)
	}

	// Unverified: refused unless the operator chose to fix it anyway.
	ctx := locale.WithLang(t.Context(), locale.En)
	if _, _, err := f.s.startFindingMitigation(ctx, fresh, false); !errors.Is(err, db.ErrFindingNotConfirmed) {
		t.Fatalf("unverified fix without consent: err=%v", err)
	}
	run, created, err := f.s.startFindingMitigation(ctx, fresh, true)
	if err != nil || !created || run.Kind != db.RetestKindMitigate {
		t.Fatalf("unverified fix with consent: run=%+v created=%v err=%v", run, created, err)
	}
	f.waitState(t, run.ID, "ready", "blocked", "failed", "cannot_fix", "rejected", "review_failed")

	// A confirmed verification starts a fix only when automatic mitigation is on.
	verify, err := latestVerify(pg, f.fid)
	if err != nil {
		t.Fatal(err)
	}
	f.s.afterFindingVerification(verify, locale.En)
	if ms, _ := pg.ListFindingMitigations(t.Context(), f.fid); len(ms) != 0 {
		t.Fatalf("manual task started a fix by itself: %d", len(ms))
	}
	if err := pg.SetTaskAutomation(taskID, db.TaskAutomation{Mitigate: true}); err != nil {
		t.Fatal(err)
	}
	f.s.afterFindingVerification(verify, locale.En)
	ms, err := pg.ListFindingMitigations(t.Context(), f.fid)
	if err != nil || len(ms) != 1 {
		t.Fatalf("automatic mitigation: %d fixes, err=%v", len(ms), err)
	}
	f.waitState(t, ms[0].Run.ID, "ready", "blocked", "failed", "cannot_fix", "rejected", "review_failed")
}

func latestVerify(pg *db.DB, findingID int64) (int64, error) {
	runs, err := pg.ListFindingRetests(findingID)
	if err != nil {
		return 0, err
	}
	for _, r := range runs {
		if r.Kind == db.RetestKindVerify {
			return r.ID, nil
		}
	}
	return 0, errors.New("no verification")
}
