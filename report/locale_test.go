package report

import (
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"strings"
	"testing"
	"time"
)

func TestReportLanguagesPreserveUserContent(t *testing.T) {
	f := &db.DBFinding{ID: 1, Name: "user original text café", Summary: "<raw> user summary", Evidence: "GET /café HTTP/1.1", Report: "# user report", Severity: "high", Status: "pending"}
	en := FindingsMarkdown([]*db.DBFinding{f}, time.Unix(0, 0), locale.En)
	if !strings.Contains(en, "# Vulnerability findings report") {
		t.Fatalf("missing heading: %q", en)
	}
	for _, content := range []string{f.Name, f.Summary, f.Evidence, f.Report} {
		if !strings.Contains(en, content) {
			t.Fatalf("stored content changed: %q", content)
		}
	}
	if strings.Contains(en, "%!") {
		t.Fatal("invalid report interpolation")
	}
	if !strings.Contains(string(FindingsCSV([]*db.DBFinding{f}, locale.En)), "Traffic evidence count") {
		t.Fatal("CSV headings not localized")
	}
}

func TestEmptyReportLanguages(t *testing.T) {
	text := Markdown(Input{Language: locale.En, GeneratedAt: time.Unix(0, 0), Goal: "preserved goal"})
	if !strings.Contains(text, "No vulnerabilities were confirmed.") || !strings.Contains(text, "preserved goal") {
		t.Fatalf("wrong language or altered goal: %q", text)
	}
}
