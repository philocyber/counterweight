package report

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

func sampleReportInput() Input {
	at := time.Date(2026, 11, 6, 10, 30, 0, 0, time.UTC)
	return Input{
		Title:  "Synthetic lab exercise: demo shop on an isolated network",
		Goal:   "Assess authentication and order APIs of the local demo shop (fictional fixture).",
		TaskID: "42", Status: "done", Version: "v0.1.0-test", Models: []string{"Lab profile (demo-model)"},
		CreatedAt: at.Add(-3 * time.Hour), FirstRunAt: at.Add(-170 * time.Minute), CompletedAt: at.Add(-10 * time.Minute), GeneratedAt: at,
		AssetCounts: map[string]int{"domain": 2, "service": 5, "endpoint": 31},
		Coverage:    &db.Coverage{Tested: 19, Denominator: 38},
		Scope:       []db.TaskScope{{Kind: "domain", Domain: "shop.lab.test", Source: "task"}, {Kind: "net", Net: "10.20.0.0/24", Source: "inherited"}},
		FindingRecords: []*db.DBFinding{
			{ID: 7, Name: "SQL injection in order search", VulnClass: "sqli", Severity: "critical", Status: "confirmed", CreatedAt: at,
				Summary:         "The `q` parameter of /api/orders/search reaches a SQL query unescaped. Ünïcödé, 中文 and emoji 🚀 must not crash rendering.\x07",
				Evidence:        "GET /api/orders/search?q=1'%20OR%201=1--&access_token=supersecretvalue HTTP/1.1\nAuthorization: Bearer abcdefghijklmnopqrstuvwxyz012345\nCookie: session=deadbeefcafebabe\n\nHTTP/1.1 200 OK",
				Report:          "## Impact\n\nFull read of the **orders** table.\n\n| Step | Result |\n|---|---|\n| Baseline | 3 rows |\n| Payload | 1,204 rows |\n\n1. Send the request.\n2. Compare counts.\n\n```\nsqlmap -u http://shop.lab.test/api/orders/search?q=1\n```\n\n> Remediation: use parameterized queries.",
				TrafficBindings: []db.FindingTrafficBinding{{ID: 3, Role: "exploit", Note: "Payload request", Snapshot: db.TrafficEvidenceSnapshot{Method: "GET", URL: "http://shop.lab.test/api/orders/search?q=1&token=leakedtokenvalue", Status: 200}}},
				EvidenceVersion: 2, ReportEvidenceVersion: 2},
			{ID: 9, Name: "Verbose stack traces", Severity: "low", Status: "pending", CreatedAt: at.Add(-time.Hour), Summary: "500 responses include framework stack traces."},
			{ID: 8, Name: "Missing rate limit on login", Severity: "medium", Status: "false_positive", CreatedAt: at.Add(-2 * time.Hour), Summary: strings.Repeat("Long summary text. ", 120)},
		},
	}
}

func TestPDFRendersMultiPageDocument(t *testing.T) {
	out, err := PDF(sampleReportInput(), PDFOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %q", out[:8])
	}
	pages := len(regexp.MustCompile(`/Type /Page\b`).FindAll(out, -1))
	if pages < 4 {
		t.Fatalf("expected cover + summary + findings + provenance pages, got %d", pages)
	}
	again, err := PDF(sampleReportInput(), PDFOptions{})
	if err != nil || !bytes.Equal(out, again) {
		t.Fatal("PDF output is not deterministic for identical input")
	}
	if path := os.Getenv("COUNTERWEIGHT_PDF_SAMPLE"); path != "" {
		_ = os.WriteFile(path, out, 0o644)
	}
}

func TestPDFHandlesEmptyAndUnfinishedTasks(t *testing.T) {
	for _, status := range []string{"running", "failed", "timeout", "", "weird"} {
		if _, err := PDF(Input{Status: status, GeneratedAt: time.Unix(0, 0), FindingRecords: []*db.DBFinding{}}, PDFOptions{}); err != nil {
			t.Fatalf("status %q: %v", status, err)
		}
	}
}

func TestRedactSecrets(t *testing.T) {
	in := "Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345\n" +
		"cookie: sid=1234567890\n" +
		"GET /x?user=bob&access_token=tok123secret&page=2\n" +
		`{"password": "hunter2", "name": "bob"}` + "\n" +
		"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.c2lnbmF0dXJlLXZhbHVl\n" +
		"https://admin:pa55w0rd@db.lab.test/\n" +
		"Bearer authentication is enabled."
	got := RedactSecrets(in)
	for _, secret := range []string{"abcdefghijklmnop", "1234567890", "tok123secret", "hunter2", "eyJhbGci", "pa55w0rd"} {
		if strings.Contains(got, secret) {
			t.Errorf("secret %q survived redaction:\n%s", secret, got)
		}
	}
	for _, keep := range []string{"user=bob", "page=2", `"name": "bob"`, "db.lab.test", "Bearer authentication is enabled."} {
		if !strings.Contains(got, keep) {
			t.Errorf("non-secret %q was removed:\n%s", keep, got)
		}
	}
}

func TestDescribeExecutionNeverCallsUnfinishedWorkClean(t *testing.T) {
	for _, status := range []string{"running", "paused", "queued", "created", "failed", "timeout", "bogus"} {
		if DescribeExecution(status, 0).Final() {
			t.Errorf("status %q reported as a completed run", status)
		}
	}
	if !DescribeExecution("done", 0).Final() {
		t.Error("done should be final")
	}
}

func TestFindingsPDFSpansTasksAndEmptySelections(t *testing.T) {
	in := sampleReportInput()
	task := int64(42)
	in.FindingRecords[0].TaskID, in.FindingRecords[0].TaskDescription = &task, "Synthetic lab exercise"
	ex := FindingsExport{Findings: in.FindingRecords, Selection: "Filtered: severity=critical", GeneratedAt: in.GeneratedAt, Version: "v0.1.0-test"}
	out, err := FindingsPDF(ex, PDFOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
	if again, _ := FindingsPDF(ex, PDFOptions{}); !bytes.Equal(out, again) {
		t.Fatal("findings PDF is not deterministic")
	}
	if path := os.Getenv("COUNTERWEIGHT_FINDINGS_PDF_SAMPLE"); path != "" {
		_ = os.WriteFile(path, out, 0o644)
	}
	if _, err := FindingsPDF(FindingsExport{GeneratedAt: in.GeneratedAt}, PDFOptions{}); err != nil {
		t.Fatalf("empty export: %v", err)
	}
	if got := findingTaskLabel(in.FindingRecords[1]); !strings.Contains(got, "No task") {
		t.Errorf("unassigned finding label: %q", got)
	}
}
