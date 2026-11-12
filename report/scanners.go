package report

import (
	"fmt"
	"strings"
)

// Advisory is a dependency advisory a supplementary scanner reported for a
// lockfile. It is not a finding and has not been checked for reachability.
type Advisory struct {
	Package string `json:"package"`
	Version string `json:"version"`
	ID      string `json:"id"`
	Fixed   string `json:"fixed_version"`
	File    string `json:"file"`
	Link    string `json:"link"`
	Review  string `json:"review"`
}

// SecretLead is a possible committed secret a supplementary scanner reported.
// It carries a location and a rule name only, never the value.
type SecretLead struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Rule   string `json:"rule"`
	Review string `json:"review"`
}

const (
	advisoriesTitle = "Dependency advisories"
	advisoriesIntro = "Reported by a dependency scanner from the repository's lockfiles. These are advisories for packages the code depends on, not vulnerabilities in the reviewed code. They are not verified for reachability and are not registered as findings."
	secretsTitle    = "Possible committed secrets"
	secretsIntro    = "Reported by a secret scanner over the working tree at the reviewed commit; git history is not scanned. Each entry is an unverified lead: the matched value is never stored or shown, and an entry may be a placeholder or test value."
)

func hasScannerSections(in Input) bool { return len(in.Advisories) > 0 || len(in.SecretLeads) > 0 }

func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

// writeScannerSections renders the scanner sections after the findings.
func writeScannerSections(b *strings.Builder, in Input) {
	if len(in.Advisories) > 0 {
		fmt.Fprintf(b, "## %s\n\n%s\n\n", advisoriesTitle, advisoriesIntro)
		b.WriteString("| Package | Installed | Advisory | Fixed in | File | Review |\n| --- | --- | --- | --- | --- | --- |\n")
		for _, a := range in.Advisories {
			id := mdCell(a.ID)
			if a.Link != "" {
				id = "[" + id + "](" + a.Link + ")"
			}
			fmt.Fprintf(b, "| %s | %s | %s | %s | `%s` | %s |\n", mdCell(a.Package), mdCell(nz(a.Version, "-")), id,
				mdCell(nz(a.Fixed, "-")), mdCell(a.File), mdCell(a.Review))
		}
		b.WriteString("\n")
	}
	if len(in.SecretLeads) > 0 {
		fmt.Fprintf(b, "## %s\n\n%s\n\n", secretsTitle, secretsIntro)
		b.WriteString("| File | Line | Rule | Review |\n| --- | --- | --- | --- |\n")
		for _, s := range in.SecretLeads {
			line := "-"
			if s.Line > 0 {
				line = fmt.Sprint(s.Line)
			}
			fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", mdCell(s.File), line, mdCell(s.Rule), mdCell(s.Review))
		}
		b.WriteString("\n")
	}
}

// scannerContents lists the scanner sections in the PDF's contents.
func scannerContents(in Input) []tocEntry {
	var out []tocEntry
	if len(in.Advisories) > 0 {
		out = append(out, tocEntry{"advisories", advisoriesTitle, 0})
	}
	if len(in.SecretLeads) > 0 {
		out = append(out, tocEntry{"secret-leads", secretsTitle, 0})
	}
	return out
}

func (w *pdfWriter) scannerSections() {
	if len(w.in.Advisories) > 0 {
		w.startSection("advisories", advisoriesTitle, advisoriesIntro, true)
		var rows, links [][]string
		for _, a := range w.in.Advisories {
			rows = append(rows, []string{w.t(a.Package), w.t(nz(a.Version, "-")), w.t(a.ID), w.t(nz(a.Fixed, "-")), w.t(a.File), a.Review})
			links = append(links, []string{"", "", a.Link, "", "", ""})
		}
		w.linkedTable([]column{{"Package", 28, false}, {"Installed", 22, false}, {"Advisory", 36, false}, {"Fixed in", 22, false},
			{"File", 38, true}, {"Review", 28, false}}, rows, links)
	}
	if len(w.in.SecretLeads) > 0 {
		w.startSection("secret-leads", secretsTitle, secretsIntro, len(w.in.Advisories) == 0)
		var rows [][]string
		for _, s := range w.in.SecretLeads {
			line := "-"
			if s.Line > 0 {
				line = fmt.Sprint(s.Line)
			}
			rows = append(rows, []string{w.t(s.File), line, w.t(s.Rule), s.Review})
		}
		w.table([]column{{"File", 78, true}, {"Line", 16, false}, {"Rule", 50, false}, {"Review", 30, false}}, rows)
	}
}
