package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/db"
)

// FindingsExport is a findings-register export that may span several tasks.
type FindingsExport struct {
	Findings    []*db.DBFinding
	Selection   string // How the findings were chosen, e.g. "Selected findings (3)".
	GeneratedAt time.Time
	Version     string
}

var findingsExportNotes = []string{
	"This export is generated from the stored findings register. It is not an independent verification of findings.",
	"Review status is the latest reviewer decision; it does not by itself prove exploitability or remediation.",
	"Only findings matching the selection at export time are included. A finding's absence is not evidence that an issue does not exist.",
}

// findingTaskLabel names a finding's originating task for cross-task exports.
func findingTaskLabel(f *db.DBFinding) string {
	if f.TaskID == nil {
		return "No task (unassigned or deleted)"
	}
	if desc := strings.TrimSpace(f.TaskDescription); desc != "" {
		return fmt.Sprintf("#%d %s", *f.TaskID, desc)
	}
	return fmt.Sprintf("#%d", *f.TaskID)
}

// FindingsPDF renders a findings export with the same branding and finding
// layout as the task report.
func FindingsPDF(ex FindingsExport, opt PDFOptions) ([]byte, error) {
	return renderTwice(func(total int, _ map[string]int) *pdfWriter {
		fs := append([]*db.DBFinding(nil), ex.Findings...)
		sortFindingsForExport(fs)
		w := newPDFWriter(opt, total, fs, "Vulnerability findings report", "Findings export", ex.Version, ex.GeneratedAt)
		w.showTask = true
		w.exportCover(ex)
		w.exportSummary()
		w.findingsSection("Findings ordered by severity, then newest first. Each entry names its originating task and review status.",
			"No findings matched this selection. That is not evidence that the assessed assets are free of issues.")
		w.exportClosing(ex)
		return w
	})
}

func (w *pdfWriter) taskCounts() [][]string {
	counts := map[string]int{}
	for _, fd := range w.findings {
		counts[findingTaskLabel(fd)]++
	}
	labels := sortedKeys(counts)
	sort.SliceStable(labels, func(i, j int) bool { return counts[labels[i]] > counts[labels[j]] })
	rows := make([][]string, 0, len(labels))
	for _, l := range labels {
		rows = append(rows, []string{w.t(l), fmt.Sprint(counts[l])})
	}
	return rows
}

func (w *pdfWriter) exportCover(ex FindingsExport) {
	open := w.openReviews()
	draft := ""
	if open > 0 {
		draft = "DRAFT · REVIEW PENDING"
	}
	counts := w.severityCounts()
	w.cover(coverSpec{
		kicker: "VULNERABILITY FINDINGS REPORT  /  REGISTER EXPORT",
		title:  "Findings export",
		subtitle: fmt.Sprintf("%s. %s from %s; %d awaiting a reviewer decision.", nz(ex.Selection, "Selection not recorded"),
			plural(len(w.findings), "finding", "findings"), plural(len(w.taskCounts()), "task group", "task groups"), open),
		draft: draft,
		metrics: []metric{
			{fmt.Sprint(len(w.findings)), "Findings", cInk},
			{fmt.Sprint(counts["critical"] + counts["high"]), "Critical + high", severityColor("critical")},
			{fmt.Sprint(open), "Awaiting review", cInk},
			{fmt.Sprint(len(w.taskCounts())), "Task groups", cTeal},
		},
		meta: [][2]string{
			{"Document", "Findings register export"},
			{"Generated", when(ex.GeneratedAt)},
			{"Findings", fmt.Sprint(len(w.findings))},
			{"Version", "Counterweight " + nz(ex.Version, "dev")},
		},
	})
}

func (w *pdfWriter) exportSummary() {
	w.startSection("summary", "Summary", "Severity and review state across the exported findings.", true)
	w.severityMetrics()
	w.reviewStatus()
	if len(w.findings) > 0 {
		w.subheading("Findings by task")
		w.table([]column{{"Task", 140, false}, {"Findings", 34, false}}, w.taskCounts())
	}
}

func (w *pdfWriter) exportClosing(ex FindingsExport) {
	w.startSection("closing", "Limitations and provenance", "How this document was produced and what it cannot establish.", true)
	w.subheading("Limitations and remaining review")
	w.limitations(append([]string(nil), findingsExportNotes...))
	w.subheading("Provenance")
	w.fact("Selection", w.t(nz(ex.Selection, "Not recorded")))
	w.fact("Findings", fmt.Sprint(len(w.findings)))
	w.fact("Generated", when(ex.GeneratedAt))
	w.fact("Software", "Counterweight "+nz(ex.Version, "dev"))
	w.fact("Source", "Stored findings register with linked traffic evidence.")
}
