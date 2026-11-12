package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

const hostileSeverity = "high\n| x | y |\n<img src=x> @admin \U0001F680"

func TestNormSeverity(t *testing.T) {
	for in, want := range map[string]string{"": "info", "  High ": "high", "CRITICAL": "critical", "low": "low", "info": "info", hostileSeverity: "info", "urgent": "info"} {
		if got := normSeverity(in); got != want {
			t.Errorf("normSeverity(%q) = %q, want %q", in, got, want)
		}
	}
	if !(severityRank("critical") < severityRank("low") && severityRank("low") < severityRank("urgent") && severityRank("urgent") == severityRank("")) {
		t.Error("unknown severities must rank below every known one")
	}
}

func TestHostileSeverityDoesNotReachRenderers(t *testing.T) {
	bad := prFinding(1, hostileSeverity, "Odd finding", "summary", "app/db.py:3")
	good := prFinding(2, "low", "Plain finding", "summary", "app/db.py:4")
	md := ReviewSummaryMarkdown(ReviewSummaryInput{Repository: "acme/example", Status: "finished", Findings: []*db.DBFinding{bad, good}, InChange: prInChange})
	for _, leak := range []string{"<img", "@admin", "\U0001F680", "| x | y |"} {
		if strings.Contains(md, leak) {
			t.Errorf("summary carries %q:\n%s", leak, md)
		}
	}
	if !strings.Contains(md, "| info |") {
		t.Errorf("unknown severity should read as info:\n%s", md)
	}

	in := sampleReportInput()
	in.FindingRecords = append(in.FindingRecords, &db.DBFinding{ID: 11, Name: "Odd", Severity: hostileSeverity, Status: "pending", CreatedAt: time.Unix(1700000000, 0)})
	if out, err := PDF(in, PDFOptions{}); err != nil || !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("PDF with hostile severity: %v", err)
	}
	if md := Markdown(in); strings.Contains(md, "<img") || strings.Contains(md, "@admin") {
		t.Error("report Markdown carries the raw severity")
	}

	odd := &db.DBFinding{ID: 5, Name: "Odd", Severity: hostileSeverity, VulnClass: "sqli", CreatedAt: time.Unix(1700000000, 0)}
	tk := FindingTicket(TicketInput{Finding: odd})
	for _, l := range tk.Labels {
		if strings.ContainsAny(l, " \n|@<") {
			t.Errorf("ticket label %q carries raw severity", l)
		}
	}
	if strings.Contains(tk.Summary, "<img") {
		t.Errorf("ticket summary carries raw severity: %q", tk.Summary)
	}
}

func TestUnknownSeverityNeverSortsAsMostSevere(t *testing.T) {
	fs := []*db.DBFinding{{ID: 1, Severity: "urgent!"}, {ID: 2, Severity: "low"}, {ID: 3, Severity: " HIGH "}}
	sortFindingsForExport(fs)
	if fs[0].ID != 3 || fs[2].ID != 1 {
		t.Errorf("order = %d,%d,%d", fs[0].ID, fs[1].ID, fs[2].ID)
	}
}
