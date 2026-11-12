// Package report renders a deliverable Markdown report from the exploration
// graph: confirmed findings (with severity/evidence) (docs §15 P5).
package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/authz"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/execution"
	"github.com/Philocyber/counterweight/locale"
)

// Input bundles what the report needs.
type Input struct {
	Isolation      execution.Info
	Language       locale.Lang
	Title          string
	Goal           string
	GeneratedAt    time.Time
	AssetCounts    map[string]int
	Findings       []*db.Node
	FindingRecords []*db.DBFinding // Non-nil selects the authoritative findings register.
	Facts          []*db.Node
	Intents        []*db.Node
	Scope          []db.TaskScope
	Coverage       *db.Coverage
	Truncated      bool

	// Provenance. Zero values render as "not recorded".
	AssessmentType string
	TaskID         string
	Status         string // Resolved task status: done, failed, timeout, running, paused, queued, created.
	CreatedAt      time.Time
	FirstRunAt     time.Time
	CompletedAt    time.Time
	Version        string
	Models         []string

	// IncludeModels names the models that served the task in Markdown and PDF
	// exports. Off by default: model choice is operator configuration that a
	// shared report does not need. The console view always shows it.
	IncludeModels bool

	// CodeReview is set for white-box source reviews; nil means a network task.
	CodeReview *CodeReview
	// Lifecycle maps finding IDs to what happened after they were reported
	// (verification, fix, pull request, post-merge re-check). Nil: not tracked.
	Lifecycle map[int64]FindingLifecycle
	// Advisories and SecretLeads are supplementary scanner leads, shown after the
	// findings in separate sections. They are never findings.
	Advisories  []Advisory
	SecretLeads []SecretLead
	// Authz is the latest stored run of the task's authorization check, shown
	// as a matrix of requests and roles. Nil: the task has none.
	Authz *authz.Report
}

type findingView struct {
	VulnClass string
	Name      string
	Severity  string
	Summary   string
	PoC       string
}

func parseFinding(n *db.Node) findingView {
	var p struct {
		VulnClass string `json:"vulnclass"`
		Name      string `json:"name"`
		Severity  string `json:"severity"`
		Summary   string `json:"summary"`
		Evidence  struct {
			PoC string `json:"poc"`
		} `json:"evidence"`
	}
	_ = json.Unmarshal(n.Payload, &p)
	return findingView{p.VulnClass, p.Name, normSeverity(p.Severity), p.Summary, p.Evidence.PoC}
}

var sevRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}

// normSeverity maps any stored severity to one of the fixed display values (critical, high,
// medium, low, info). Anything else, including text an agent supplied, becomes "info", so no
// renderer emits a stored severity verbatim.
func normSeverity(s string) string { return nz(db.NormalizeSeverity(s), "info") }

// severityRank orders severities from the most to the least serious; unknown values rank as info.
func severityRank(s string) int { return sevRank[normSeverity(s)] }

// Markdown renders the report. Local paths are redacted: the file is meant to be shared.
func Markdown(in Input) string {
	lang := locale.Resolve(in.Language)
	var b strings.Builder
	if in.CodeReview != nil {
		fmt.Fprintf(&b, "# Security code review report: %s\n\n", in.CodeReview.Repository)
	} else {
		fmt.Fprintf(&b, locale.Text(lang, "# Penetration test report — %s\n\n"), nz(in.Title, locale.Text(lang, "Untitled task")))
	}
	fmt.Fprintf(&b, "- **Document**: %s\n", in.DocumentID())
	fmt.Fprintf(&b, locale.Text(lang, "- **Generated at**: %s\n"), in.GeneratedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- **Execution isolation**: %s\n", in.ExecutionIsolation().Description())
	exec := DescribeExecution(in.Status, findingCount(in))
	fmt.Fprintf(&b, "- **Execution**: %s. %s\n\n", exec.Headline, exec.Detail)

	b.WriteString("## Executive summary\n\n")
	fmt.Fprintf(&b, "**Objective.** %s\n\n", nz(in.Objective(), locale.Text(lang, "(not specified)")))
	if in.FindingRecords != nil {
		fmt.Fprintf(&b, "- Registered findings: **%d** (review status is shown per finding)\n", len(in.FindingRecords))
		fmt.Fprintf(&b, "- Status: %s\n", posture(in))
	} else {
		fmt.Fprintf(&b, locale.Text(lang, "- Confirmed findings: **%d**\n"), len(in.Findings))
	}
	writeSeverityTable(&b, in)
	if actions := keyActions(in, 3); len(actions) > 0 {
		b.WriteString("### Priorities\n\n")
		for i, a := range actions {
			fmt.Fprintf(&b, "%d. **[%s] %s** (finding %d): %s\n", i+1, strings.ToUpper(normSeverity(a.Severity)), a.Title, a.FindingID, a.Action)
		}
		b.WriteString("\n")
	}
	if in.CodeReview != nil {
		writeReviewTarget(&b, in.CodeReview, reportFindings(in))
		if in.Lifecycle != nil {
			writeLifecycle(&b, in)
		}
	} else {
		writeNetworkScope(&b, in, lang)
	}

	writeFindingsAndRecords(&b, in, lang)
	writeProvenance(&b, in)
	return RedactLocalPaths(b.String())
}

func writeSeverityTable(b *strings.Builder, in Input) {
	w := &pdfWriter{findings: reportFindings(in)}
	c := w.severityCounts()
	b.WriteString("\n| Critical | High | Medium | Low | Info / other |\n| --- | --- | --- | --- | --- |\n")
	fmt.Fprintf(b, "| %d | %d | %d | %d | %d |\n\n", c["critical"], c["high"], c["medium"], c["low"], c["info"])
}

// mdLines renders cited lines, linked to the reviewed commit when possible.
func mdLines(cr *CodeReview, file string, lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if link := cr.SourceLink(file, l); link != "" {
			out[i] = "[" + l + "](" + link + ")"
		} else {
			out[i] = l
		}
	}
	return strings.Join(out, ", ")
}

// writeReviewTarget replaces network scope and asset coverage for white-box reviews.
func writeReviewTarget(b *strings.Builder, cr *CodeReview, fs []*db.DBFinding) {
	b.WriteString("## Review target\n\n")
	fmt.Fprintf(b, "- Repository: %s\n", cr.URL)
	fmt.Fprintf(b, "- Ref: %s\n", nz(cr.Ref, "Default branch (not pinned)"))
	fmt.Fprintf(b, "- Commit: %s\n", nz(cr.Commit, "Not recorded"))
	fmt.Fprintf(b, "- Review mode: %s\n", reviewModeLabel(cr.Mode))
	b.WriteString("- Method: source code review only. No traffic is sent to live hosts or networks as part of the review.\n")
	fmt.Fprintf(b, "- Target details: %s\n\n", cr.targetSource())
	b.WriteString("### Code cited by findings\n\n")
	files := citedFiles(fs)
	if len(files) == 0 {
		b.WriteString("No finding cites a file:line location.\n\n")
		return
	}
	b.WriteString("| File | Lines | Findings |\n| --- | --- | --- |\n")
	for _, cf := range files {
		fmt.Fprintf(b, "| `%s` | %s | %d |\n", cf.File, mdLines(cr, cf.File, cf.Lines), cf.Findings)
	}
	b.WriteString("\nFile-level review coverage is not tracked yet: a file not listed here is not established as reviewed or free of issues.\n\n")
}

func day(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// lifecycleDates lists the recorded dates of a finding's lifecycle steps.
func lifecycleDates(l FindingLifecycle) string {
	var parts []string
	for _, p := range []struct {
		label string
		at    *time.Time
	}{{"verified", l.VerifiedAt}, {"PR opened", l.PROpenedAt}, {"re-checked", l.RecheckedAt}} {
		if d := day(p.at); d != "" {
			parts = append(parts, p.label+" "+d)
		}
	}
	return strings.Join(parts, ", ")
}

// writeLifecycle shows, per finding, how far it went after it was reported.
func writeLifecycle(b *strings.Builder, in Input) {
	b.WriteString("## Finding lifecycle\n\n")
	b.WriteString(lifecycleIntro + "\n\n")
	for _, c := range lifecycleCounts(in) {
		fmt.Fprintf(b, "- %s: **%d**\n", stageLabel(c.Key), c.Count)
	}
	b.WriteString("\n| ID | Finding | Severity | Verification | Fix | After merge | Dates |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, f := range reportFindings(in) {
		l := in.Lifecycle[f.ID]
		fix := fixLabel(l)
		if l.PRURL != "" {
			fix = "[" + fix + "](" + l.PRURL + ")"
		}
		fmt.Fprintf(b, "| %d | %s | %s | %s | %s | %s | %s |\n", f.ID, strings.ReplaceAll(findingTitle(f), "|", "\\|"),
			strings.ToUpper(normSeverity(f.Severity)), verificationLabel(l.Verification), fix, recheckLabel(l), nz(lifecycleDates(l), "-"))
	}
	b.WriteString("\n" + lifecycleNote + "\n\n")
}

// writeTrace renders a parsed source-to-sink trace and its labelled fields.
func writeTrace(b *strings.Builder, cr *CodeReview, tr EvidenceTrace) {
	b.WriteString("**Code trace:**\n\n")
	for i, st := range tr.Steps {
		loc := ""
		if st.Location != "" {
			loc = " `" + st.Location + "`"
			if file, lines, ok := strings.Cut(st.Location, ":"); ok {
				if link := cr.SourceLink(file, lines); link != "" {
					loc = " [`" + st.Location + "`](" + link + ")"
				}
			}
		}
		fmt.Fprintf(b, "%d. **%s**%s: %s\n", i+1, humanize(st.Kind), loc, st.Detail)
	}
	b.WriteString("\n")
	for _, f := range tr.Fields {
		fmt.Fprintf(b, "- **%s:** %s\n", f.Label, f.Text)
	}
	b.WriteString("\n")
}

func writeProvenance(b *strings.Builder, in Input) {
	if strings.TrimSpace(in.Goal) != "" {
		b.WriteString("## Appendix: task instructions\n\n")
		b.WriteString("The instructions the task was started with, as stored.\n\n```text\n")
		b.WriteString(strings.TrimSpace(in.Goal))
		b.WriteString("\n```\n\n")
	}
	b.WriteString("## Provenance\n\n")
	fmt.Fprintf(b, "- Document ID: %s\n", in.DocumentID())
	fmt.Fprintf(b, "- Task: %s (%s)\n", nz(in.TaskID, "-"), taskStatusLabel(in.Status))
	fmt.Fprintf(b, "- Content fingerprint (SHA-256 of the reported findings): `%s`\n", in.ContentFingerprint())
	if in.IncludeModels {
		fmt.Fprintf(b, "- Models used: %s\n", nz(strings.Join(in.Models, "; "), "Not recorded"))
	}
	fmt.Fprintf(b, "- Software: Counterweight %s\n", nz(in.Version, "dev"))
}

func writeNetworkScope(b *strings.Builder, in Input, lang locale.Lang) {
	b.WriteString(locale.Text(lang, "- Assets: "))
	var types []string
	for t := range in.AssetCounts {
		types = append(types, t)
	}
	sort.Strings(types)
	for i, t := range types {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%s %d", t, in.AssetCounts[t])
	}
	b.WriteString("\n\n")
	b.WriteString("## Scope and coverage\n\n")
	if len(in.Scope) == 0 {
		b.WriteString("No scope rows were recorded for this task. Review the authorized scope separately.\n\n")
	}
	for _, row := range in.Scope {
		label := row.Domain
		if label == "" {
			label = row.Net
		}
		if label == "" {
			label = row.CompanyName
		}
		if label == "" {
			label = row.Value
		}
		if label == "" {
			label = row.Kind
		}
		fmt.Fprintf(b, "- %s: %s (source: %s)\n", row.Kind, label, row.Source)
	}
	if in.Coverage != nil {
		fmt.Fprintf(b, "\nAssets with recorded results: **%d / %d** within this task and its direct read-only source context. This is approximate asset coverage, not a completed security test suite.\n\n", in.Coverage.Tested, in.Coverage.Denominator)
	}

}

func writeFindingsAndRecords(b *strings.Builder, in Input, lang locale.Lang) {
	b.WriteString(locale.Text(lang, "## Findings\n\n"))
	if in.FindingRecords != nil {
		if len(in.FindingRecords) == 0 {
			b.WriteString("No findings are registered for this task. This does not establish that the task completed or that the assets are safe.\n\n")
		}
		if len(in.FindingRecords) > 0 {
			b.WriteString("| # | Severity | Finding | Class | Status |\n| --- | --- | --- | --- | --- |\n")
			for i, f := range reportFindings(in) {
				fmt.Fprintf(b, "| %d | %s | %s | %s | %s |\n", i+1, strings.ToUpper(normSeverity(f.Severity)),
					strings.ReplaceAll(findingTitle(f), "|", "\\|"), classRef(classInfo(f.VulnClass)), findingStatusLabel(f.Status))
			}
			b.WriteString("\n")
		}
		for i, finding := range reportFindings(in) {
			ci := classInfo(finding.VulnClass)
			fmt.Fprintf(b, "### %d. [%s] %s\n\n", i+1, severityLabel(lang, finding.Severity), findingTitle(finding, lang))
			fmt.Fprintf(b, "- Finding ID: %d\n- Review status: %s\n", finding.ID, findingStatusLabel(finding.Status))
			if ci.Label != "" {
				fmt.Fprintf(b, "- Class: %s\n", classRef(ci))
			}
			var lp *FindingLifecycle
			if in.Lifecycle != nil {
				l := in.Lifecycle[finding.ID]
				lp = &l
				fmt.Fprintf(b, "- Lifecycle: %s\n", stageLabel(l.Stage()))
			}
			if !closed(finding, lp) {
				fmt.Fprintf(b, "- Next step: %s\n", nextAction(finding, lp))
			}
			if in.CodeReview != nil {
				for _, loc := range codeLocations(finding) {
					fmt.Fprintf(b, "- Affected code: `%s` lines %s\n", loc.File, mdLines(in.CodeReview, loc.File, loc.Lines))
				}
			}
			fmt.Fprintf(b, "\n%s\n\n", finding.Summary)
			if tr := parseEvidence(finding.Evidence); tr.Parsed {
				writeTrace(b, in.CodeReview, tr)
			} else if finding.Evidence != "" {
				fmt.Fprintf(b, "**Recorded evidence:**\n\n%s\n\n", finding.Evidence)
			}
			if finding.Report != "" {
				fmt.Fprintf(b, "**Detailed report:**\n\n%s\n\n", finding.Report)
			}
		}
	} else if len(in.Findings) == 0 {
		b.WriteString(locale.Text(lang, "_No vulnerabilities were confirmed._\n\n"))
	} else {
		fs := make([]findingView, 0, len(in.Findings))
		for _, n := range in.Findings {
			fs = append(fs, parseFinding(n))
		}
		sort.SliceStable(fs, func(i, j int) bool { return severityRank(fs[i].Severity) < severityRank(fs[j].Severity) })
		for i, f := range fs {
			fmt.Fprintf(b, "### %d. [%s] %s\n\n", i+1, severityLabel(lang, normSeverity(f.Severity)), nz(f.Name, nz(f.VulnClass, locale.Text(lang, "Unclassified"))))
			fmt.Fprintf(b, "%s\n\n", nz(f.Summary, ""))
			if f.PoC != "" {
				fmt.Fprintf(b, locale.Text(lang, "**PoC / evidence:**\n\n```\n%s\n```\n\n"), f.PoC)
			}
		}
	}

	writeScannerSections(b, in)
	writeAuthzSection(b, in)
	b.WriteString("## Recorded exploration conclusions\n\n")
	b.WriteString("These are recorded observations, including negative or inconclusive results where the agent supplied them. A completed attempt or a linked fact is not proof that an asset is safe.\n\n")
	written := 0
	for _, n := range in.Facts {
		if n.State == db.StateOrigin {
			continue
		}
		writeRecordedNode(b, n)
		written++
	}
	if written == 0 {
		b.WriteString("No exploration conclusions were recorded in this report.\n\n")
	}
	b.WriteString("## Exploration execution record\n\n")
	b.WriteString("States describe execution, not a vulnerability verdict. Blocked, exhausted, stopped and deleted attempts require separate review.\n\n")
	for _, n := range in.Intents {
		writeRecordedNode(b, n)
	}
	if len(in.Intents) == 0 {
		b.WriteString("No exploration attempts were recorded in this report.\n\n")
	}
	b.WriteString("## Limitations and remaining review\n\n")
	notes := limitationNotes
	if in.CodeReview != nil {
		notes = codeReviewNotes
	}
	for _, note := range notes {
		fmt.Fprintf(b, "- %s\n", note)
	}
	if in.Truncated {
		b.WriteString("- **Record limit reached:** each section includes at most 5,000 newest records. Older records remain in the application; this export is incomplete.\n")
	}
}

// Preserve authored observations and their execution state without classifying
// free-form model prose as a clean bill of health.
func writeRecordedNode(b *strings.Builder, n *db.Node) {
	summary := recordSummary(n)
	fmt.Fprintf(b, "### Record %d — %s\n\n", n.ID, n.State)
	fmt.Fprintf(b, "- Recorded at: %s\n- Origin: %s\n\n%s\n\n", n.CreatedAt.Format(time.RFC3339), nz(n.Origin, "unspecified"), summary)
	if n.BlockedReason != "" {
		fmt.Fprintf(b, "Execution issue: %s\n\n", n.BlockedReason)
	}
	if n.DeleteReason != "" {
		fmt.Fprintf(b, "Removal reason: %s\n\n", n.DeleteReason)
	}
}

// recordSummary picks the authored text of an exploration record, falling back
// to the raw payload so nothing recorded is dropped.
func recordSummary(n *db.Node) string {
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(n.Payload, &payload)
	summary := ""
	for _, key := range []string{"summary", "text", "name", "description", "conclusion"} {
		if json.Unmarshal(payload[key], &summary) == nil && strings.TrimSpace(summary) != "" {
			break
		}
	}
	if strings.TrimSpace(summary) == "" {
		summary = string(n.Payload)
	}
	return summary
}

// limitationNotes is shared by the Markdown and PDF task reports.
var limitationNotes = []string{
	"This export is generated from stored task records. It is not an independent verification of findings or a complete raw activity archive.",
	"Unrecorded checks cannot be inferred as passed. Review the asset backlog, session traces, traffic and approvals for gaps.",
	"Scope and textual constraints alone do not establish an enforced low-impact execution boundary.",
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func findingCount(in Input) int {
	if in.FindingRecords != nil {
		return len(in.FindingRecords)
	}
	return len(in.Findings)
}

func nz(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// severityLabel and statusLabel are display-only; export machine fields remain unchanged.
// lang is accepted for API stability but has no effect: English is the only
// supported language.
func severityLabel(lang locale.Lang, value string) string {
	return strings.ToUpper(normSeverity(value))
}
func statusLabel(lang locale.Lang, value string) string {
	return value
}

// classRef reads "SQL injection (CWE-89, A03:2021 Injection)".
func classRef(c ClassInfo) string {
	if c.Label == "" {
		return "-"
	}
	var refs []string
	for _, r := range []string{c.CWE, c.OWASP} {
		if r != "" {
			refs = append(refs, r)
		}
	}
	if len(refs) == 0 {
		return c.Label
	}
	return c.Label + " (" + strings.Join(refs, ", ") + ")"
}
