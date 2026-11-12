package db

import (
	"testing"
)

func TestRunVerdictDisposition(t *testing.T) {
	for _, tc := range []struct{ kind, verdict, want string }{
		{RetestKindRetest, "fixed", FindingFixed},
		{RetestKindRetest, "reproduced", ""},
		{RetestKindRetest, "inconclusive", ""},
		{RetestKindRetest, "confirmed", ""},
		{RetestKindVerify, "confirmed", FindingConfirmed},
		{RetestKindVerify, "rejected", FindingFalsePositive},
		{RetestKindVerify, "needs_validation", ""},
		{RetestKindVerify, "fixed", ""},
		{RetestKindVerify, "", ""},
	} {
		if got := RunVerdictDisposition(tc.kind, tc.verdict); got != tc.want {
			t.Errorf("RunVerdictDisposition(%q,%q)=%q want %q", tc.kind, tc.verdict, got, tc.want)
		}
	}
	for _, v := range []string{"confirmed", "needs_validation", "rejected"} {
		if !ValidVerificationVerdict(v) {
			t.Errorf("%s should be a verify verdict", v)
		}
	}
	for _, v := range []string{"", "fixed", "reproduced", "inconclusive", "CONFIRMED"} {
		if ValidVerificationVerdict(v) {
			t.Errorf("%q should not be a verify verdict", v)
		}
	}
}

func TestVerificationTriageAndDeduplication(t *testing.T) {
	for _, tc := range []struct{ verdict, terminal, want string }{
		{"confirmed", "completed", FindingConfirmed},
		{"rejected", "completed", FindingFalsePositive},
		{"needs_validation", "completed", FindingPending},
		{"confirmed", "stopped", FindingPending},
		{"", "completed", FindingPending},
	} {
		t.Run(tc.verdict+"/"+tc.terminal, func(t *testing.T) {
			d, fid := retestDB(t)
			if _, err := d.SetFindingStatus(fid, FindingPending); err != nil {
				t.Fatal(err)
			}
			r, c, created, err := d.CreateFindingVerification(t.Context(), fid)
			if err != nil || !created || r.Kind != RetestKindVerify || c.AgentKey != CodeVerifierAgentKey {
				t.Fatalf("create: r=%+v c=%+v created=%v err=%v", r, c, created, err)
			}
			if _, err := d.StartFindingRetest(t.Context(), r.ID); err != nil {
				t.Fatal(err)
			}
			// A retest verdict can never be written into a verify run, and vice versa.
			if err := d.RecordFindingRetestResult(t.Context(), c.ID, "fixed", "s", "e"); err == nil {
				t.Fatal("retest result accepted on a verify run")
			}
			if err := d.RecordFindingVerificationResult(t.Context(), c.ID, "fixed", "s", "e"); err == nil {
				t.Fatal("retest verdict accepted by verification")
			}
			if tc.verdict != "" {
				if err := d.RecordFindingVerificationResult(t.Context(), c.ID, tc.verdict, "summary", "file.go:12 re-read"); err != nil {
					t.Fatal(err)
				}
			}
			if err := d.FinishFindingRetest(r.ID, tc.terminal, ""); err != nil {
				t.Fatal(err)
			}
			f, err := d.GetFinding(fid)
			if err != nil || f.Status != tc.want || f.Evidence != "original evidence" {
				t.Fatalf("finding=%+v err=%v want=%s", f, err, tc.want)
			}
			// A completed verification is never repeated; a failed/stopped one may be.
			again, _, createdAgain, err := d.CreateFindingVerification(t.Context(), fid)
			if err != nil {
				t.Fatal(err)
			}
			finished, err := d.ListFindingRetests(fid)
			if err != nil || len(finished) == 0 {
				t.Fatal(err)
			}
			completed := finished[len(finished)-1].Status == "completed"
			if completed && (createdAgain || again.ID != r.ID) {
				t.Fatalf("completed verification repeated: again=%+v created=%v", again, createdAgain)
			}
			if !completed && !createdAgain {
				t.Fatalf("interrupted verification not replaceable: %+v", finished)
			}
		})
	}
}

func TestRetestResultRejectedOnVerifyAndStillWorksOnRetest(t *testing.T) {
	d, fid := retestDB(t)
	r, c, _, err := d.CreateFindingRetest(t.Context(), fid, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != RetestKindRetest {
		t.Fatalf("retest kind=%q", r.Kind)
	}
	if _, err := d.StartFindingRetest(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordFindingVerificationResult(t.Context(), c.ID, "confirmed", "s", "e"); err == nil {
		t.Fatal("verification result accepted on a retest run")
	}
	if err := d.RecordFindingRetestResult(t.Context(), c.ID, "reproduced", "s", "e"); err != nil {
		t.Fatal(err)
	}
}
