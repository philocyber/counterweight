package report

import (
	"fmt"
	"strings"

	"github.com/Philocyber/counterweight/authz"
)

// The authorization matrix is the latest stored run of a task's authorization
// check (package authz): for every listed request, what each role had to get
// and what it got. It is produced without a model. The section shows statuses
// and the runner's own messages, never a response excerpt: an excerpt of a
// failed check is data of the target that the role should not have seen.

const (
	authzTitle = "Authorization matrix"
	// authzMatrixRoles is how many role columns fit the PDF page; with more
	// roles the checks are listed one per row instead.
	authzMatrixRoles = 4
	// authzHeadingRunes is the longest role name a column heading shows whole.
	authzHeadingRunes = 18
)

func authzIntro(r *authz.Report) string {
	return fmt.Sprintf("Deterministic check of which role may send which request, run on %s against %s without a model. It covers only the requests listed in the task's policy; a request that is not listed was not checked. Result: %s (%d checks: %d passed, %d failed, %d inconclusive).",
		r.StartedAt.Format("2006-01-02 15:04 UTC"), nz(r.Target, "an unresolved target"), strings.ToUpper(r.Result),
		r.Counts.Checks, r.Counts.Passed, r.Counts.Failed, r.Counts.Inconclusive)
}

// authzRoles returns the personas of a run in order, and the name to show for each.
func authzRoles(r *authz.Report) (order []string, shown map[string]string) {
	shown = map[string]string{}
	for _, p := range r.Personas {
		order = append(order, p.Name)
		shown[p.Name] = nz(p.Role, p.Name)
	}
	return order, shown
}

func authzStatuses(codes []int) string {
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = fmt.Sprint(c)
	}
	return strings.Join(parts, "/")
}

// authzCell is one cell of the matrix: the expectation, what came back, the result.
func authzCell(ch *authz.CheckResult) string {
	if ch == nil {
		return "-"
	}
	got := "no answer"
	if ch.Status != 0 {
		got = fmt.Sprint(ch.Status)
	}
	result := ch.Result
	if result != authz.Pass {
		result = strings.ToUpper(result)
	}
	return fmt.Sprintf("%s %s, got %s: %s", ch.Expect, authzStatuses(ch.ExpectedStatus), got, result)
}

func authzCheckOf(c *authz.CaseResult, persona string) *authz.CheckResult {
	for i := range c.Checks {
		if c.Checks[i].Persona == persona {
			return &c.Checks[i]
		}
	}
	return nil
}

// authzMatrixFits reports whether the run can be drawn as a table with one
// column per role: few roles, with names short enough for a column heading.
func authzMatrixFits(order []string, shown map[string]string) bool {
	if len(order) > authzMatrixRoles {
		return false
	}
	for _, name := range order {
		if len([]rune(shown[name])) > authzHeadingRunes {
			return false
		}
	}
	return true
}

// authzProblems lists the checks that did not pass, one line each.
func authzProblems(r *authz.Report, shown map[string]string) []string {
	var out []string
	for _, c := range r.Cases {
		for _, ch := range c.Checks {
			if ch.Result != authz.Pass {
				out = append(out, fmt.Sprintf("%s, as %s: %s (%s). %s", c.ID, shown[ch.Persona], strings.ToUpper(ch.Result), ch.Kind, ch.Detail))
			}
		}
	}
	return out
}

func writeAuthzSection(b *strings.Builder, in Input) {
	r := in.Authz
	if r == nil {
		return
	}
	order, shown := authzRoles(r)
	fmt.Fprintf(b, "## %s\n\n%s\n\n", authzTitle, authzIntro(r))
	for _, e := range r.Errors {
		fmt.Fprintf(b, "- Run error: %s\n", mdText(e))
	}
	if len(r.Errors) > 0 {
		b.WriteString("\n")
	}
	if len(r.Cases) > 0 {
		b.WriteString("| Case | Request |")
		for _, name := range order {
			fmt.Fprintf(b, " %s |", mdText(shown[name]))
		}
		b.WriteString("\n| --- | --- |" + strings.Repeat(" --- |", len(order)) + "\n")
		for i := range r.Cases {
			c := &r.Cases[i]
			fmt.Fprintf(b, "| %s | `%s %s` |", mdCell(c.ID), c.Method, mdCell(strings.ReplaceAll(c.Path, "`", "'")))
			for _, name := range order {
				fmt.Fprintf(b, " %s |", mdCell(authzCell(authzCheckOf(c, name))))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if problems := authzProblems(r, shown); len(problems) > 0 {
		b.WriteString("Checks that did not pass:\n\n")
		for _, p := range problems {
			fmt.Fprintf(b, "- %s\n", mdText(p))
		}
		b.WriteString("\n")
	}
}

func authzContents(in Input) []tocEntry {
	if in.Authz == nil {
		return nil
	}
	return []tocEntry{{"authz", authzTitle, 0}}
}

func (w *pdfWriter) authzSection() {
	r := w.in.Authz
	if r == nil {
		return
	}
	order, shown := authzRoles(r)
	w.startSection("authz", authzTitle, authzIntro(r), true)
	for _, e := range r.Errors {
		w.bullet("-", "Run error: "+w.t(e))
	}
	if len(r.Cases) > 0 && authzMatrixFits(order, shown) {
		cols := []column{{"Case", 30, false}, {"Request", 44, true}}
		for _, name := range order {
			cols = append(cols, column{w.t(shown[name]), (contentW - 74) / float64(len(order)), false})
		}
		var rows [][]string
		for i := range r.Cases {
			c := &r.Cases[i]
			row := []string{w.t(c.ID), w.t(c.Method + " " + c.Path)}
			for _, name := range order {
				row = append(row, authzCell(authzCheckOf(c, name)))
			}
			rows = append(rows, row)
		}
		w.table(cols, rows)
	} else if len(r.Cases) > 0 {
		var rows [][]string
		for i := range r.Cases {
			c := &r.Cases[i]
			for _, name := range order {
				rows = append(rows, []string{w.t(c.ID), w.t(c.Method + " " + c.Path), w.t(shown[name]), authzCell(authzCheckOf(c, name))})
			}
		}
		w.table([]column{{"Case", 30, false}, {"Request", 50, true}, {"Role", 36, false}, {"Expected and received", contentW - 116, false}}, rows)
	}
	if problems := authzProblems(r, shown); len(problems) > 0 {
		w.f.Ln(2)
		w.paragraph("Checks that did not pass:")
		for _, p := range problems {
			w.bullet("-", w.t(p))
		}
	}
}
