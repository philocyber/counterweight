package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
)

func TestCodeLocationsReadsFileLineReferencesOnly(t *testing.T) {
	f := &db.DBFinding{
		Evidence: "Source-to-sink: app/api/v1/projects/route.ts:5-7 GET uses localRoute; lib/local-route.ts:28-31 and lib/local-route.ts:54-71.\n" +
			"Called from http://shop.lab.test:8080/x and localhost:3000; see `lib/security/service-auth.ts:92`.",
		Report: "Repeated lib/local-route.ts:28-31 and (lib/workspace/local-project.ts:116-145).",
	}
	got := codeLocations(f)
	want := map[string]string{
		"app/api/v1/projects/route.ts":   "5-7",
		"lib/local-route.ts":             "28-31, 54-71",
		"lib/security/service-auth.ts":   "92",
		"lib/workspace/local-project.ts": "116-145",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files: %+v", len(got), got)
	}
	for _, loc := range got {
		if w, ok := want[loc.File]; !ok || strings.Join(loc.Lines, ", ") != w {
			t.Errorf("%s: got %v, want %q", loc.File, loc.Lines, w)
		}
	}
}

func codeReviewInput() Input {
	in := sampleReportInput()
	in.Status = "paused"
	in.CodeReview = &CodeReview{Repository: "philocyber/argus-tm", URL: "https://github.com/philocyber/argus-tm", Inferred: true}
	in.FindingRecords[0].Evidence = "app/api/v1/projects/route.ts:5-7 -> lib/local-route.ts:28-31"
	return in
}

func TestCodeReviewReportReplacesNetworkScope(t *testing.T) {
	md := Markdown(codeReviewInput())
	for _, want := range []string{"# Security code review report", "## Review target", "https://github.com/philocyber/argus-tm",
		"Commit: Not recorded", "Inferred from the task objective", "`lib/local-route.ts`", "Results apply to the commit shown"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
	for _, unwanted := range []string{"## Scope and coverage", "Assets with recorded results", "shop.lab.test (source", "Penetration test report"} {
		if strings.Contains(md, unwanted) {
			t.Errorf("markdown still contains network-scope text %q", unwanted)
		}
	}
	out, err := PDF(codeReviewInput(), PDFOptions{})
	if err != nil || !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("code review PDF: %v", err)
	}
}

func TestBuildViewCarriesReportFacts(t *testing.T) {
	v := BuildView(codeReviewInput())
	if v.Kind != "code_review" || v.CodeReview == nil || !v.CodeReview.Inferred || v.Scope == nil || len(v.Scope) != 0 {
		t.Fatalf("code review view: kind=%q target=%+v scope=%v", v.Kind, v.CodeReview, v.Scope)
	}
	if len(v.Findings) != 3 || v.Findings[0].Severity != "critical" || len(v.Findings[0].CodeLocations) != 2 {
		t.Fatalf("findings not ordered or located: %+v", v.Findings)
	}
	if v.Severity.Critical != 1 || v.OpenReviews != 1 || !v.Draft || v.Execution.Kind != "in_progress" {
		t.Fatalf("summary facts: %+v open=%d draft=%v exec=%+v", v.Severity, v.OpenReviews, v.Draft, v.Execution)
	}
	if len(v.CitedFiles) != 2 || v.Limitations[1] != codeReviewNotes[1] {
		t.Fatalf("cited files/limitations: %+v %v", v.CitedFiles, v.Limitations)
	}
	net := BuildView(sampleReportInput())
	if net.Kind != "pentest" || net.CodeReview != nil || len(net.Scope) != 2 || net.Coverage == nil || len(net.Assets) != 3 {
		t.Fatalf("network view: %+v", net)
	}
}

func TestCitedFilesListsEachLineOnceInOrder(t *testing.T) {
	fs := []*db.DBFinding{
		{ID: 1, Evidence: "app/app.py:26 then app/app.py:40-42"},
		{ID: 2, Evidence: "app/app.py:26 and app/app.py:9"},
	}
	got := citedFiles(fs)
	if len(got) != 1 || got[0].File != "app/app.py" || got[0].Findings != 2 {
		t.Fatalf("citedFiles = %+v", got)
	}
	if want := []string{"9", "26", "40-42"}; strings.Join(got[0].Lines, ",") != strings.Join(want, ",") {
		t.Fatalf("lines = %v, want %v", got[0].Lines, want)
	}
}
