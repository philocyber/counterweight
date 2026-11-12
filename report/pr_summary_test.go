package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

func prFinding(id int64, sev, name, summary, evidence string) *db.DBFinding {
	return &db.DBFinding{ID: id, Severity: sev, Name: name, Summary: summary, Evidence: evidence, Status: db.FindingPending, CreatedAt: time.Unix(1700000000+id, 0)}
}

func prInChange(f *db.DBFinding) bool { return strings.Contains(f.Evidence, "app/db.py") }

func TestReviewSummaryMarkdown(t *testing.T) {
	fs := []*db.DBFinding{
		prFinding(1, "medium", "Weak hash", "md5 used", "app/db.py:20"),
		prFinding(2, "high", "SQL injection | in query", "uid reaches SQL. password=hunter2secret in /home/user/app", "app/db.py:10-12"),
		prFinding(3, "low", "Old issue @someone", "outside", "lib/old.py:5"),
		prFinding(4, "critical", "Verified issue", "confirmed", "app/db.py:30"),
		prFinding(5, "high", "Dismissed", "x", "app/db.py:1"),
	}
	fs[4].Status = db.FindingFalsePositive
	md := ReviewSummaryMarkdown(ReviewSummaryInput{
		Repository: "acme/example", PullNumber: 7, HeadCommit: strings.Repeat("a", 40), Status: "finished",
		TaskURL: "https://cw.example.com/function/tasks/detail/?id=9",
		FindingURL: func(id int64) string {
			return "https://cw.example.com/function/findings/detail?id=" + string(rune('0'+id))
		},
		Findings: fs, InChange: prInChange,
		Lifecycle:    map[int64]FindingLifecycle{1: {Verification: "confirmed"}},
		ChangedFiles: 3,
	})
	for _, want := range []string{"acme/example #7", "Status: **finished**", "`aaaaaaaaaaaa`", "3 (1 critical, 1 high, 1 medium)",
		"**Outside the changed lines:** 1", "`app/db.py:10`", "verified", "findings/detail?id=2", "SQL injection \\| in query"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	for _, bad := range []string{"hunter2secret", "/home/user", "@someone", "Dismissed"} {
		if strings.Contains(md, bad) {
			t.Errorf("summary leaks or keeps %q", bad)
		}
	}
	// Verified finding first even though it is only medium; then by severity.
	iv, ic, ih := strings.Index(md, "Weak hash"), strings.Index(md, "Verified issue"), strings.Index(md, "SQL injection")
	if !(iv < ic && ic < ih) {
		t.Errorf("order wrong: weak=%d verified=%d sql=%d", iv, ic, ih)
	}
}

func TestReviewSummaryNoFindingsAndUnfinished(t *testing.T) {
	md := ReviewSummaryMarkdown(ReviewSummaryInput{Repository: "acme/example", Status: "waiting", WaitingReason: "budget", InChange: prInChange})
	for _, want := range []string{"waiting (budget)", "has not finished", "not a statement that the change is free of problems"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
}

func TestSARIFKeepInChangeAndRelative(t *testing.T) {
	fs := []*db.DBFinding{prFinding(1, "high", "In", "s", "app/db.py:10"), prFinding(2, "low", "Out", "s", "lib/old.py:5")}
	cr := &CodeReview{Repository: "acme/example", URL: "https://github.com/acme/example", Commit: strings.Repeat("a", 40)}
	base := SARIFInput{Findings: fs, Version: "test", CodeReview: func(*db.DBFinding) *CodeReview { return cr }, InChange: prInChange}

	plain := sarifDecode(t, base)
	if len(sarifResults(plain)) != 2 {
		t.Fatal("default must keep every finding")
	}
	if !strings.Contains(sarifString(t, plain), "%SRCROOT%") {
		t.Fatal("default export lost its base id")
	}

	base.Keep, base.RepoRelative = prInChange, true
	log := sarifDecode(t, base)
	res := sarifResults(log)
	if len(res) != 1 {
		t.Fatalf("results = %d", len(res))
	}
	if res[0].(jm)["properties"].(jm)["inChange"] != true {
		t.Fatal("inChange not recorded")
	}
	s := sarifString(t, log)
	if strings.Contains(s, "uriBaseId") || strings.Contains(s, "originalUriBaseIds") || !strings.Contains(s, `"uri": "app/db.py"`) {
		t.Fatalf("locations are not repository-relative: %s", s)
	}
}

func sarifString(t *testing.T, log jm) string {
	t.Helper()
	raw, err := jsonMarshalIndent(log)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func jsonMarshalIndent(v any) (string, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	return string(raw), err
}
