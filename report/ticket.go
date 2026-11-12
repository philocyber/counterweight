package report

import (
	"fmt"
	"strings"

	"github.com/Philocyber/counterweight/db"
)

// Ticket is a finding written for the developers who own the code: what is
// wrong, where, how it happens, how to fix it and how the fix is verified. All
// text is redacted (credentials, local paths): issue trackers are widely read.
type Ticket struct {
	Summary  string
	Labels   []string
	Facts    []TicketFact // shown in a panel at the top
	Sections []TicketSection
}

// TicketFact is one labelled line of the summary panel. Link is optional.
type TicketFact struct {
	Label, Text, Link string
}

// TicketSection is a titled block of Markdown.
type TicketSection struct {
	Title, Markdown string
}

// TicketInput is what a finding ticket is built from.
type TicketInput struct {
	Finding    *db.DBFinding
	CodeReview *CodeReview
	Lifecycle  *FindingLifecycle
	TaskID     string
	TaskTitle  string
	// FindingURL links back to the finding in Counterweight; "" when no public
	// address is configured.
	FindingURL string
	// Due is the remediation due date (YYYY-MM-DD) and SLADays its policy; empty
	// or zero when no SLA applies.
	Due     string
	SLADays int
}

// Ticket length limits: Jira caps summaries at 255 characters and whole fields
// near 32 KB, so long agent-written text is clipped with a pointer to the source.
const (
	ticketSummaryMax  = 240
	ticketEvidenceMax = 6000
	ticketReportMax   = 14000
)

var owaspLinks = map[string]string{
	"A01": "https://owasp.org/Top10/A01_2021-Broken_Access_Control/",
	"A02": "https://owasp.org/Top10/A02_2021-Cryptographic_Failures/",
	"A03": "https://owasp.org/Top10/A03_2021-Injection/",
	"A04": "https://owasp.org/Top10/A04_2021-Insecure_Design/",
	"A05": "https://owasp.org/Top10/A05_2021-Security_Misconfiguration/",
	"A07": "https://owasp.org/Top10/A07_2021-Identification_and_Authentication_Failures/",
	"A08": "https://owasp.org/Top10/A08_2021-Software_and_Data_Integrity_Failures/",
	"A10": "https://owasp.org/Top10/A10_2021-Server-Side_Request_Forgery_%28SSRF%29/",
}

// redactTicket hides credentials and machine paths in ticket text.
func redactTicket(s string) string { return RedactLocalPaths(RedactSecrets(s)) }

func clipRunes(s string, n int) (string, bool) {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + "…", true
	}
	return s, false
}

// cweLink is the MITRE page of a CWE id such as "CWE-89".
func cweLink(cwe string) string {
	return "https://cwe.mitre.org/data/definitions/" + strings.TrimPrefix(cwe, "CWE-") + ".html"
}

// severityName reads "Critical", "High"...
func severityName(s string) string { return humanize(normSeverity(s)) }

// FindingTicket builds the issue for one finding.
func FindingTicket(in TicketInput) Ticket {
	f := in.Finding
	ci := classInfo(f.VulnClass)
	title, _ := clipRunes(redactTicket(findingTitle(f)), ticketSummaryMax-12)
	t := Ticket{Summary: fmt.Sprintf("[%s] %s", severityName(f.Severity), title)}

	t.Labels = []string{"counterweight", "security", "severity-" + strings.ToLower(normSeverity(f.Severity))}
	if ci.CWE != "" {
		t.Labels = append(t.Labels, strings.ToLower(ci.CWE))
	}

	t.Facts = append(t.Facts, TicketFact{Label: "Severity", Text: severityName(f.Severity)})
	if ci.Label != "" {
		fact := TicketFact{Label: "Classification", Text: ci.Label}
		if ci.CWE != "" {
			fact.Text += " (" + ci.CWE + ")"
			fact.Link = cweLink(ci.CWE)
		}
		t.Facts = append(t.Facts, fact)
	}
	if in.Due != "" {
		due := in.Due
		if in.SLADays > 0 {
			due += fmt.Sprintf(" (%d-day remediation target for %s findings)", in.SLADays, strings.ToLower(severityName(f.Severity)))
		}
		t.Facts = append(t.Facts, TicketFact{Label: "Fix by", Text: due})
	}
	status := findingStatusLabel(f.Status)
	if in.Lifecycle != nil {
		status += ". " + stageLabel(in.Lifecycle.Stage())
	}
	t.Facts = append(t.Facts, TicketFact{Label: "Status", Text: status})
	if cr := in.CodeReview; cr != nil {
		fact := TicketFact{Label: "Repository", Text: cr.Repository}
		if cr.Commit != "" {
			fact.Text += " at " + cr.shortCommit()
			if strings.HasPrefix(cr.URL, "https://github.com/") {
				fact.Link = strings.TrimSuffix(cr.URL, "/") + "/commit/" + cr.Commit
			}
		}
		t.Facts = append(t.Facts, fact)
	}
	t.Facts = append(t.Facts, TicketFact{Label: "Finding", Text: fmt.Sprintf("#%d in Counterweight", f.ID), Link: in.FindingURL})

	add := func(title, md string) {
		if strings.TrimSpace(md) != "" {
			t.Sections = append(t.Sections, TicketSection{title, redactTicket(md)})
		}
	}
	add("What is wrong", f.Summary)

	if locs := codeLocations(f); len(locs) > 0 {
		var b strings.Builder
		for _, l := range locs {
			fmt.Fprintf(&b, "- `%s` lines %s\n", l.File, mdLines(in.CodeReview, l.File, l.Lines))
		}
		add("Affected code", b.String())
	}

	tr := parseEvidence(f.Evidence)
	remediation := ""
	if tr.Parsed {
		var b strings.Builder
		for i, st := range tr.Steps {
			loc := ""
			if st.Location != "" {
				loc = " `" + st.Location + "`"
				if file, lines, ok := strings.Cut(st.Location, ":"); ok {
					if link := in.CodeReview.SourceLink(file, lines); link != "" {
						loc = " [" + st.Location + "](" + link + ")"
					}
				}
			}
			fmt.Fprintf(&b, "%d. **%s**%s: %s\n", i+1, humanize(st.Kind), loc, st.Detail)
		}
		b.WriteString("\n")
		for _, fl := range tr.Fields {
			switch fl.Label {
			case "Fingerprint", "Ref":
			case "Remediation":
				remediation = fl.Text
			default:
				fmt.Fprintf(&b, "- **%s:** %s\n", fl.Label, fl.Text)
			}
		}
		add("How it happens", b.String())
	} else if e := strings.TrimSpace(f.Evidence); e != "" {
		e, cut := clipRunes(e, ticketEvidenceMax)
		md := "```\n" + strings.ReplaceAll(e, "```", "'''") + "\n```"
		if cut {
			md += "\nThe evidence is shortened here; the full text is in Counterweight."
		}
		add("Recorded evidence", md)
	}
	add("How to fix", remediation)

	if rep := strings.TrimSpace(f.Report); rep != "" {
		rep, cut := clipRunes(rep, ticketReportMax)
		if cut {
			rep += "\n\nThe report is shortened here; the full text is in Counterweight."
		}
		add("Detailed report and reproduction", rep)
	}

	verify := "Deploy the fix, then run **Retest** on this finding in Counterweight. Counterweight comments on this ticket with the result."
	if in.CodeReview != nil {
		verify = "Merge the fix, then run **Re-check merged code** on this finding in Counterweight (Mitigation tab). " +
			"Counterweight comments on this ticket and moves it to Done when an independent re-check finds the reported path closed."
	}
	add("How to verify the fix", verify)

	var refs strings.Builder
	if ci.CWE != "" {
		fmt.Fprintf(&refs, "- [%s: %s](%s)\n", ci.CWE, ci.Label, cweLink(ci.CWE))
	}
	if ci.OWASP != "" {
		if link := owaspLinks[strings.SplitN(ci.OWASP, ":", 2)[0]]; link != "" {
			fmt.Fprintf(&refs, "- [OWASP Top 10 %s](%s)\n", ci.OWASP, link)
		}
	}
	if in.FindingURL != "" {
		fmt.Fprintf(&refs, "- [Finding #%d in Counterweight](%s)\n", f.ID, in.FindingURL)
	}
	add("References", refs.String())
	return t
}

// TaskEpic builds the epic that groups the findings of one task.
func TaskEpic(in Input, taskURL string) Ticket {
	title, _ := clipRunes(redactTicket(nz(in.Title, "Task "+in.TaskID)), ticketSummaryMax-12)
	t := Ticket{Summary: "[Security] " + title, Labels: []string{"counterweight", "security", "security-review"}}
	if cr := in.CodeReview; cr != nil {
		fact := TicketFact{Label: "Repository", Text: cr.Repository, Link: cr.URL}
		if cr.Commit != "" {
			fact.Text += " at " + cr.shortCommit()
		}
		t.Facts = append(t.Facts, fact, TicketFact{Label: "Review mode", Text: reviewModeLabel(cr.Mode)})
	}
	w := &pdfWriter{findings: reportFindings(in)}
	c := w.severityCounts()
	t.Facts = append(t.Facts,
		TicketFact{Label: "Findings", Text: fmt.Sprintf("%d critical, %d high, %d medium, %d low, %d info", c["critical"], c["high"], c["medium"], c["low"], c["info"])},
		TicketFact{Label: "Status", Text: posture(in)},
		TicketFact{Label: "Assessment", Text: "Task #" + nz(in.TaskID, "-") + " in Counterweight", Link: taskURL},
	)
	t.Sections = append(t.Sections, TicketSection{"Objective", redactTicket(in.Objective())})
	if actions := keyActions(in, 3); len(actions) > 0 {
		var b strings.Builder
		for i, a := range actions {
			fmt.Fprintf(&b, "%d. **[%s] %s**: %s\n", i+1, severityName(a.Severity), redactTicket(a.Title), a.Action)
		}
		t.Sections = append(t.Sections, TicketSection{"Priorities", b.String()})
	}
	t.Sections = append(t.Sections, TicketSection{"How this epic is used",
		"Each finding of this assessment is a child issue with its affected code, how it happens, how to fix it and how the fix is verified. " +
			"Counterweight comments on the child issues as fixes are proposed, merged and re-checked."})
	return t
}
