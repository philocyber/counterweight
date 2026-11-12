package report

import (
	"regexp"
	"sort"
	"strings"

	"github.com/Philocyber/counterweight/db"
)

// CodeReview describes the target of a white-box source review. When set on
// Input, the task report uses code-review structure instead of network scope.
type CodeReview struct {
	Repository string `json:"repository"` // owner/name
	URL        string `json:"url"`
	Ref        string `json:"ref,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Mode       string `json:"mode,omitempty"`
	// Inferred marks metadata derived from the task objective rather than
	// recorded when the review was launched.
	Inferred bool `json:"-"`
}

func (c *CodeReview) shortCommit() string {
	if c.Commit == "" {
		return "Not recorded"
	}
	if len(c.Commit) > 12 {
		return c.Commit[:12]
	}
	return c.Commit
}

func (c *CodeReview) targetSource() string {
	if c.Inferred {
		return "Inferred from the task objective; not recorded when the review was launched."
	}
	return "Recorded when the review was launched."
}

var codeReviewNotes = []string{
	"This export is generated from stored task records. It is not an independent verification of findings.",
	"Results apply to the commit shown. Later changes to the repository are not covered.",
	"Findings are supported by source-to-sink code traces and, where recorded, local proof-of-concept tests. No deployed instance was tested unless a finding says so.",
	"File-level review coverage is not tracked: a file not cited by a finding is not established as reviewed or free of issues.",
	"Asset scope and network coverage records do not apply to a source review and are omitted from this report.",
}

// codeLocationRe matches file:line or file:start-end references to source files.
// A known extension is required so host:port values are not read as code.
var codeLocationRe = regexp.MustCompile(`(?:^|[\s(\[` + "`" + `"'])((?:[A-Za-z0-9_.@-]+/)*[A-Za-z0-9_.@-]+\.(?:go|ts|tsx|js|jsx|mjs|cjs|py|rb|java|kt|kts|cs|php|rs|c|h|cc|cpp|hpp|swift|scala|sql|ya?ml|json|toml|sh|tf|vue|svelte|html|ex|exs|erl|dart|lua|pl)):(\d+)(?:-(\d+))?`)

type codeLocation struct {
	File  string   `json:"file"`
	Lines []string `json:"lines"` // "5-7", "28-31"
}

// codeLocations lists the files and line ranges a finding cites, in first-seen order.
func codeLocations(f *db.DBFinding) []codeLocation {
	var out []codeLocation
	index := map[string]int{}
	seen := map[string]bool{}
	for _, text := range []string{f.Evidence, f.Report, f.Summary} {
		for _, m := range codeLocationRe.FindAllStringSubmatch(text, -1) {
			file := strings.TrimPrefix(m[1], "./")
			lines := m[2]
			if m[3] != "" && m[3] != m[2] {
				lines += "-" + m[3]
			}
			if seen[file+":"+lines] {
				continue
			}
			seen[file+":"+lines] = true
			i, ok := index[file]
			if !ok {
				i = len(out)
				index[file] = i
				out = append(out, codeLocation{File: file})
			}
			out[i].Lines = append(out[i].Lines, lines)
		}
	}
	for i := range out {
		out[i].Lines = mergeRanges(out[i].Lines)
	}
	return out
}

// citedFiles aggregates code locations across findings: file -> lines and finding count.
func citedFiles(fs []*db.DBFinding) []CitedFile {
	type agg struct {
		lines    []string
		findings int
	}
	byFile := map[string]*agg{}
	for _, f := range fs {
		for _, loc := range codeLocations(f) {
			a := byFile[loc.File]
			if a == nil {
				a = &agg{}
				byFile[loc.File] = a
			}
			a.findings++
			a.lines = append(a.lines, loc.Lines...)
		}
	}
	// Several findings often cite the same or overlapping lines.
	for _, a := range byFile {
		a.lines = mergeRanges(a.lines)
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	out := make([]CitedFile, 0, len(files))
	for _, f := range files {
		out = append(out, CitedFile{File: f, Lines: byFile[f].lines, Findings: byFile[f].findings})
	}
	return out
}
