package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Philocyber/counterweight/db"
)

// Markdown summary of a change-scoped review, sized for a workflow job summary
// or a pull request comment. Text that comes from findings is redacted like the
// other exports, and made inert as Markdown: no table breaks, no HTML, no
// mentions that would notify someone.

const (
	summaryMaxInChange  = 50
	summaryMaxOutside   = 20
	summaryDescription  = 160
	summaryFooter       = "Findings are leads for a human to confirm. The review reads the code without running it, and it does not establish that anything it did not report is free of problems."
	summaryNoFindingsIn = "No findings were recorded inside the changed lines. This is not a statement that the change is free of problems."
)

// SeverityOrder lists severities from the most to the least serious.
var SeverityOrder = []string{"critical", "high", "medium", "low", "info"}

// ReviewSummaryInput is what the Markdown summary is built from.
type ReviewSummaryInput struct {
	Repository string
	PullNumber int
	HeadCommit string
	Status     string // queued, running, waiting, finished, failed
	// WaitingReason names why a waiting review waits (budget, approval, ...).
	WaitingReason string
	// TaskURL and FindingURL make links; FindingURL formats a finding id, "" for no link.
	TaskURL    string
	FindingURL func(id int64) string

	Findings  []*db.DBFinding
	InChange  func(*db.DBFinding) bool
	Lifecycle map[int64]FindingLifecycle

	ChangedFiles   int
	FilesTruncated bool
	DiffTruncated  bool
}

// OpenReviewFinding reports whether a finding still counts: one a person or a
// verification closed (false positive, fixed, duplicate, ...) does not.
func OpenReviewFinding(f *db.DBFinding) bool {
	return !sarifSuppressed[nz(f.Status, db.FindingPending)]
}

// ReviewSummaryMarkdown renders the summary.
func ReviewSummaryMarkdown(in ReviewSummaryInput) string {
	var inside, outside []*db.DBFinding
	for _, f := range in.Findings {
		if !OpenReviewFinding(f) {
			continue
		}
		if in.InChange != nil && in.InChange(f) {
			inside = append(inside, f)
		} else {
			outside = append(outside, f)
		}
	}
	order := func(list []*db.DBFinding) {
		sort.SliceStable(list, func(i, j int) bool {
			vi, vj := in.Lifecycle[list[i].ID].Verification == "confirmed", in.Lifecycle[list[j].ID].Verification == "confirmed"
			if vi != vj {
				return vi
			}
			if ri, rj := severityRank(list[i].Severity), severityRank(list[j].Severity); ri != rj {
				return ri < rj
			}
			return list[i].ID < list[j].ID
		})
	}
	order(inside)
	order(outside)

	var b strings.Builder
	title := mdText(in.Repository)
	if in.PullNumber > 0 {
		title += fmt.Sprintf(" #%d", in.PullNumber)
	}
	fmt.Fprintf(&b, "## Counterweight security review: %s\n\n", title)
	state := in.Status
	if in.Status == "waiting" && in.WaitingReason != "" {
		state += " (" + mdText(in.WaitingReason) + ")"
	}
	fmt.Fprintf(&b, "Status: **%s**", state)
	if len(in.HeadCommit) >= 12 {
		fmt.Fprintf(&b, " · head `%s`", in.HeadCommit[:12])
	}
	if in.TaskURL != "" {
		fmt.Fprintf(&b, " · [open in Counterweight](%s)", in.TaskURL)
	}
	b.WriteString("\n\n")
	if in.Status != "finished" {
		b.WriteString("The review has not finished, so this list can still change.\n\n")
	}

	fmt.Fprintf(&b, "**Inside the change:** %s\n\n", severityCounts(inside))
	if len(outside) > 0 {
		fmt.Fprintf(&b, "**Outside the changed lines:** %d (%s). They concern code this change does not modify; they are listed after the table.\n\n", len(outside), severityCounts(outside))
	} else {
		b.WriteString("**Outside the changed lines:** 0.\n\n")
	}
	if in.ChangedFiles > 0 {
		fmt.Fprintf(&b, "Changed files: %d.", in.ChangedFiles)
		if in.FilesTruncated {
			b.WriteString(" The change was too large to list completely; findings in unlisted files count as inside the change.")
		}
		if in.DiffTruncated {
			b.WriteString(" The reviewers saw only the start of the diff text, and read the other files directly.")
		}
		b.WriteString("\n\n")
	}

	if len(inside) == 0 {
		b.WriteString(summaryNoFindingsIn + "\n\n")
	} else {
		writeFindingTable(&b, in, inside, summaryMaxInChange)
	}
	if len(outside) > 0 {
		fmt.Fprintf(&b, "<details><summary>%d outside the changed lines</summary>\n\n", len(outside))
		writeFindingTable(&b, in, outside, summaryMaxOutside)
		b.WriteString("</details>\n\n")
	}
	b.WriteString("_" + summaryFooter + "_\n")
	return b.String()
}

func severityCounts(list []*db.DBFinding) string {
	if len(list) == 0 {
		return "0"
	}
	counts := map[string]int{}
	for _, f := range list {
		counts[normSeverity(f.Severity)]++
	}
	var parts []string
	for _, s := range SeverityOrder {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	return fmt.Sprintf("%d (%s)", len(list), strings.Join(parts, ", "))
}

func writeFindingTable(b *strings.Builder, in ReviewSummaryInput, list []*db.DBFinding, max int) {
	b.WriteString("| Severity | Location | Finding | Verification |\n|---|---|---|---|\n")
	for i, f := range list {
		if i == max {
			break
		}
		loc := "-"
		for _, l := range FindingLocations(f) {
			if uri := sarifURI(l.File); uri != "" {
				loc = fmt.Sprintf("`%s:%d`", strings.ReplaceAll(uri, "`", "'"), l.Start)
				break
			}
		}
		text := mdText(findingTitle(f))
		if s := mdText(firstParagraph(f.Summary)); s != "" {
			text += ": " + clipSummary(s)
		}
		if in.FindingURL != nil {
			if u := in.FindingURL(f.ID); u != "" {
				text += fmt.Sprintf(" ([#%d](%s))", f.ID, u)
			}
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", normSeverity(f.Severity), loc, text, reviewVerificationLabel(in.Lifecycle[f.ID].Verification))
	}
	if len(list) > max {
		fmt.Fprintf(b, "\n%d more in Counterweight.\n", len(list)-max)
	}
	b.WriteString("\n")
}

func reviewVerificationLabel(v string) string {
	switch v {
	case "confirmed":
		return "verified"
	case "needs_validation":
		return "needs validation"
	}
	return "not verified"
}

func clipSummary(s string) string {
	if r := []rune(s); len(r) > summaryDescription {
		return string(r[:summaryDescription]) + "..."
	}
	return s
}

var mdEscaper = strings.NewReplacer("|", "\\|", "<", "&lt;", ">", "&gt;", "[", "\\[", "]", "\\]", "`", "'", "@", "@​", "\r", " ", "\n", " ")

// mdText redacts secrets and local paths and makes the text one inert Markdown line.
func mdText(s string) string {
	return strings.Join(strings.Fields(mdEscaper.Replace(sarifClean(s))), " ")
}
