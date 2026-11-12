package report

import (
	"bytes"
	"strings"
	"testing"
)

func TestFindingLifecycleStage(t *testing.T) {
	for _, tc := range []struct {
		l    FindingLifecycle
		want string
	}{
		{FindingLifecycle{}, StageUnverified},
		{FindingLifecycle{Verification: "needs_validation"}, StageNeedsValidation},
		{FindingLifecycle{Verification: "rejected"}, StageFalsePositive},
		{FindingLifecycle{Verification: "confirmed"}, StageConfirmed},
		{FindingLifecycle{Verification: "confirmed", Fix: "changes_needed"}, StageConfirmed},
		{FindingLifecycle{Verification: "confirmed", Fix: "ready"}, StageFixProposed},
		{FindingLifecycle{Verification: "confirmed", Fix: "pr_open", PRState: "open"}, StagePROpen},
		{FindingLifecycle{Verification: "confirmed", Fix: "merged", PRState: "merged"}, StageMerged},
		{FindingLifecycle{Verification: "confirmed", Fix: "merged", PRState: "merged", Recheck: "confirmed"}, StageStillPresent},
		{FindingLifecycle{Fix: "merged", PRState: "merged", Recheck: "rejected"}, StageValidated},
		{FindingLifecycle{Verification: "confirmed", Fix: "pr_closed", PRState: "closed"}, StageConfirmed},
	} {
		if got := tc.l.Stage(); got != tc.want {
			t.Errorf("%+v: stage=%s want %s", tc.l, got, tc.want)
		}
	}
}

// A launched code review shows each finding's path from report to validated fix
// in every format; a network task shows none of it.
func TestReportShowsFindingLifecycle(t *testing.T) {
	in := codeReviewInput()
	in.CodeReview.Inferred = false
	in.Lifecycle = map[int64]FindingLifecycle{
		7: {Verification: "confirmed", Fix: "merged", PRURL: "https://github.com/philocyber/argus-tm/pull/1", PRState: "merged", Recheck: "rejected"},
		8: {Verification: "rejected"},
	}
	md := Markdown(in)
	for _, want := range []string{"## Finding lifecycle", "Fixed and validated after merge: **1**", "False positive (rejected by verification): **1**",
		"Not independently verified: **1**", "[PR merged](https://github.com/philocyber/argus-tm/pull/1)", "Fixed (re-check passed)",
		"- Lifecycle: Fixed and validated after merge", "neither runs the application"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
	v := BuildView(in)
	if len(v.Lifecycle) != 3 || v.Lifecycle[0] != (Count{StageValidated, 1}) {
		t.Fatalf("view lifecycle=%+v", v.Lifecycle)
	}
	for _, f := range v.Findings {
		if f.Lifecycle == nil || f.Stage == "" {
			t.Fatalf("finding %d has no lifecycle", f.ID)
		}
	}
	out, err := PDF(in, PDFOptions{})
	if err != nil || !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("PDF with lifecycle: %v", err)
	}

	plain := sampleReportInput()
	if strings.Contains(Markdown(plain), "Finding lifecycle") {
		t.Fatal("network report shows a lifecycle")
	}
	if v := BuildView(plain); len(v.Lifecycle) != 0 || v.Findings[0].Lifecycle != nil {
		t.Fatalf("network view lifecycle=%+v", v.Lifecycle)
	}
}
