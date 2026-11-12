package report

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Philocyber/counterweight/db"
)

// ClassInfo is the reader-facing description of a vulnerability class: a plain
// name plus its CWE and OWASP Top 10 (2021) category when the class is known.
type ClassInfo struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	CWE   string `json:"cwe,omitempty"`   // e.g. "CWE-89"
	OWASP string `json:"owasp,omitempty"` // e.g. "A03:2021 Injection"
}

type classDef struct{ label, cwe, owasp string }

const (
	owaspAccess    = "A01:2021 Broken Access Control"
	owaspCrypto    = "A02:2021 Cryptographic Failures"
	owaspInjection = "A03:2021 Injection"
	owaspDesign    = "A04:2021 Insecure Design"
	owaspConfig    = "A05:2021 Security Misconfiguration"
	owaspAuth      = "A07:2021 Identification and Authentication Failures"
	owaspIntegrity = "A08:2021 Software and Data Integrity Failures"
	owaspSSRF      = "A10:2021 Server-Side Request Forgery"
)

// classDefs keys are normalized: lower case, separators collapsed to "_".
var classDefs = map[string]classDef{
	"sqli":                     {"SQL injection", "CWE-89", owaspInjection},
	"sql_injection":            {"SQL injection", "CWE-89", owaspInjection},
	"nosqli":                   {"NoSQL injection", "CWE-943", owaspInjection},
	"ssti":                     {"Server-side template injection", "CWE-1336", owaspInjection},
	"template_injection":       {"Server-side template injection", "CWE-1336", owaspInjection},
	"xss":                      {"Cross-site scripting", "CWE-79", owaspInjection},
	"command_injection":        {"OS command injection", "CWE-78", owaspInjection},
	"rce":                      {"Remote code execution", "CWE-94", owaspInjection},
	"code_injection":           {"Code injection", "CWE-94", owaspInjection},
	"ldap_injection":           {"LDAP injection", "CWE-90", owaspInjection},
	"xxe":                      {"XML external entity injection", "CWE-611", owaspConfig},
	"insecure_deserialization": {"Insecure deserialization", "CWE-502", owaspIntegrity},
	"deserialization":          {"Insecure deserialization", "CWE-502", owaspIntegrity},
	"path_traversal":           {"Path traversal", "CWE-22", owaspAccess},
	"lfi":                      {"Local file inclusion", "CWE-98", owaspAccess},
	"idor":                     {"Insecure direct object reference", "CWE-639", owaspAccess},
	"broken_access_control":    {"Broken access control", "CWE-284", owaspAccess},
	"auth_bypass":              {"Authentication bypass", "CWE-287", owaspAuth},
	"broken_authentication":    {"Broken authentication", "CWE-287", owaspAuth},
	"default_credentials":      {"Default credentials", "CWE-1392", owaspAuth},
	"weak_password":            {"Weak password requirements", "CWE-521", owaspAuth},
	"hardcoded_secret":         {"Hard-coded secret", "CWE-798", owaspAuth},
	"hardcoded_credential":     {"Hard-coded credentials", "CWE-798", owaspAuth},
	"hardcoded_credentials":    {"Hard-coded credentials", "CWE-798", owaspAuth},
	"jwt":                      {"JSON Web Token weakness", "CWE-347", owaspAuth},
	"weak_crypto":              {"Weak cryptography", "CWE-327", owaspCrypto},
	"sensitive_data_exposure":  {"Sensitive data exposure", "CWE-200", owaspCrypto},
	"information_disclosure":   {"Information disclosure", "CWE-200", owaspConfig},
	"info_disclosure":          {"Information disclosure", "CWE-200", owaspConfig},
	"ssrf":                     {"Server-side request forgery", "CWE-918", owaspSSRF},
	"csrf":                     {"Cross-site request forgery", "CWE-352", owaspAccess},
	"open_redirect":            {"Open redirect", "CWE-601", owaspAccess},
	"file_upload":              {"Unrestricted file upload", "CWE-434", owaspDesign},
	"misconfiguration":         {"Security misconfiguration", "CWE-16", owaspConfig},
	"cors":                     {"Permissive CORS policy", "CWE-942", owaspConfig},
	"mass_assignment":          {"Mass assignment", "CWE-915", owaspDesign},
	"race_condition":           {"Race condition", "CWE-362", owaspDesign},
	"csv_injection":            {"CSV injection", "CWE-1236", owaspInjection},
	"formula_injection":        {"CSV injection", "CWE-1236", owaspInjection},
}

var classSep = regexp.MustCompile(`[\s\-./]+`)

// classInfo describes a stored vulnerability class. Unknown classes keep a
// readable label and no taxonomy reference rather than a guessed one.
func classInfo(class string) ClassInfo {
	key := strings.Trim(classSep.ReplaceAllString(strings.ToLower(strings.TrimSpace(class)), "_"), "_")
	if key == "" {
		return ClassInfo{}
	}
	if d, ok := classDefs[key]; ok {
		return ClassInfo{Key: class, Label: d.label, CWE: d.cwe, OWASP: d.owasp}
	}
	words := strings.Split(key, "_")
	for i, w := range words {
		if i == 0 && w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return ClassInfo{Key: class, Label: strings.Join(words, " ")}
}

// findingStatusLabels names review statuses for readers.
var findingStatusLabels = map[string]string{
	db.FindingPending:       "Pending review",
	db.FindingInProgress:    "In review",
	db.FindingConfirmed:     "Confirmed",
	db.FindingFixed:         "Fixed",
	db.FindingResolved:      "Resolved",
	db.FindingRiskAccepted:  "Risk accepted",
	db.FindingFalsePositive: "False positive",
	db.FindingDuplicate:     "Duplicate",
	db.FindingIgnored:       "Ignored",
}

func findingStatusLabel(s string) string {
	s = nz(s, db.FindingPending)
	if l, ok := findingStatusLabels[s]; ok {
		return l
	}
	return humanize(s)
}

var taskStatusLabels = map[string]string{
	"done": "Completed", "failed": "Failed", "timeout": "Stopped at time limit", "running": "Running",
	"paused": "Paused", "queued": "Queued", "created": "Not started",
}

func taskStatusLabel(s string) string {
	if l, ok := taskStatusLabels[s]; ok {
		return l
	}
	return humanize(nz(s, "unknown"))
}

func reviewModeLabel(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return "Not recorded"
	}
	return humanize(mode) + " review"
}

func humanize(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "_", " "), "-", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// plural returns "1 finding" or "3 findings".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// mergeRanges merges overlapping or adjacent line ranges ("101", "101-102",
// "103-112" become "101-112") and returns them in order.
func mergeRanges(lines []string) []string {
	type span struct{ a, b int }
	var spans []span
	var other []string
	for _, l := range lines {
		parts := strings.SplitN(l, "-", 2)
		a, err := strconv.Atoi(parts[0])
		if err != nil {
			other = append(other, l)
			continue
		}
		b := a
		if len(parts) == 2 {
			if v, err := strconv.Atoi(parts[1]); err == nil && v >= a {
				b = v
			}
		}
		spans = append(spans, span{a, b})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].a < spans[j].a })
	var merged []span
	for _, s := range spans {
		if n := len(merged); n > 0 && s.a <= merged[n-1].b+1 {
			if s.b > merged[n-1].b {
				merged[n-1].b = s.b
			}
			continue
		}
		merged = append(merged, s)
	}
	out := make([]string, 0, len(merged)+len(other))
	for _, s := range merged {
		if s.a == s.b {
			out = append(out, strconv.Itoa(s.a))
		} else {
			out = append(out, fmt.Sprintf("%d-%d", s.a, s.b))
		}
	}
	return append(out, other...)
}

// lineAnchor turns "10" or "10-14" into a GitHub line fragment.
func lineAnchor(r string) string {
	parts := strings.SplitN(r, "-", 2)
	if len(parts) == 2 {
		return "#L" + parts[0] + "-L" + parts[1]
	}
	return "#L" + parts[0]
}

// blobBase is "<repository>/blob/<commit>/" for GitHub repositories, else "".
func (c *CodeReview) blobBase() string {
	if c == nil || c.Commit == "" || !strings.HasPrefix(c.URL, "https://github.com/") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSuffix(c.URL, "/"), ".git") + "/blob/" + c.Commit + "/"
}

// SourceLink is a permalink to cited lines at the reviewed commit, or "" when
// the repository is not on GitHub or the commit is unknown.
func (c *CodeReview) SourceLink(file, lines string) string {
	base := c.blobBase()
	if base == "" {
		return ""
	}
	link := base + strings.TrimPrefix(file, "/")
	if lines != "" {
		link += lineAnchor(lines)
	}
	return link
}

// Each pattern starts at the beginning of the text or after a delimiter, so
// path-like segments inside URLs ("https://host/home/x") are left alone. The
// delimiter is captured as group 1 and kept.
var (
	// checkoutRe matches the local clone a code review ran against
	// (<data dir>/codereview/<id>/src) in any separator style.
	checkoutRe = regexp.MustCompile(`(?i)(^|[\s"'(\[=,;])(?:[a-z]:|/)[^\s"'<>|]*?(?:\\{1,2}|/)codereview(?:\\{1,2}|/)[^\s\\/"'<>|]+(?:\\{1,2}|/)src\b`)
	// homeSpacedRe matches a Windows home directory whose user name contains
	// spaces; it needs the separator after the name to know where it ends.
	homeSpacedRe = regexp.MustCompile(`(?i)(^|[\s"'(\[=,;])[a-z]:(\\{1,2}|/)Users(?:\\{1,2}|/)[^\\/"'<>|:\r\n]+(?:\\{1,2}|/)`)
	// homeRe matches a home directory, which names the operator.
	homeRe = regexp.MustCompile(`(?i)(^|[\s"'(\[=,;])(?:[a-z]:(?:\\{1,2}|/)Users(?:\\{1,2}|/)[^\s\\/"'<>|]+|/(?:home|Users)/[^\s/"'<>]+)`)
)

// Placeholders for machine-specific paths in exports.
const (
	CheckoutPlaceholder = "<checkout>"
	HomePlaceholder     = "<home>"
)

// RedactLocalPaths hides where the task ran: the review checkout becomes
// <checkout> (so "<checkout>/app/app.py" stays readable) and home directories
// become <home>. Other paths are left alone because they can be evidence.
func RedactLocalPaths(s string) string {
	s = checkoutRe.ReplaceAllString(s, "${1}"+CheckoutPlaceholder)
	s = homeSpacedRe.ReplaceAllString(s, "${1}"+HomePlaceholder+"${2}")
	return homeRe.ReplaceAllString(s, "${1}"+HomePlaceholder)
}

// Objective is the reader-facing statement of what the task set out to do.
// A code review states its target instead of the instructions given to the agent.
func (in Input) Objective() string {
	if cr := in.CodeReview; cr != nil {
		ref := nz(cr.Ref, "the default branch")
		at := ""
		if cr.Commit != "" {
			at = ", commit " + cr.shortCommit()
		}
		return fmt.Sprintf("White-box security review of the source code of %s (%s%s), %s. "+
			"The goal is to find and confirm exploitable vulnerabilities, each backed by a traced code path, without sending traffic to any deployed system.",
			cr.Repository, ref, at, strings.ToLower(reviewModeLabel(cr.Mode)))
	}
	return strings.TrimSpace(RedactLocalPaths(in.Goal))
}

// KeyAction is one prioritized next step for an open finding.
type KeyAction struct {
	FindingID int64  `json:"finding_id"`
	Title     string `json:"title"`
	Severity  string `json:"severity"`
	Action    string `json:"action"`
}

// closed reports whether a finding needs no further remediation work.
func closed(f *db.DBFinding, l *FindingLifecycle) bool {
	switch f.Status {
	case db.FindingFixed, db.FindingResolved, db.FindingFalsePositive, db.FindingDuplicate, db.FindingIgnored, db.FindingRiskAccepted:
		return true
	}
	if l != nil {
		switch l.Stage() {
		case StageValidated, StageFalsePositive:
			return true
		}
	}
	return false
}

// nextAction says what moves a finding forward from where it stands.
func nextAction(f *db.DBFinding, l *FindingLifecycle) string {
	if l != nil {
		switch l.Stage() {
		case StageStillPresent:
			return "Rework the fix: the issue is still present after the merge."
		case StageMerged:
			return "Re-check the merged code to confirm the fix."
		case StagePROpen:
			return "Review and merge the open pull request."
		case StageFixProposed:
			return "Review the prepared fix and open a pull request."
		case StageConfirmed:
			return "Fix the confirmed issue and verify it after the merge."
		case StageNeedsValidation:
			return "Validate the finding manually; verification was inconclusive."
		}
	}
	switch nz(f.Status, db.FindingPending) {
	case db.FindingPending, db.FindingInProgress:
		return "Review the evidence and confirm or reject the finding."
	}
	return "Remediate and retest."
}

// keyActions lists the open findings in severity order with their next step.
func keyActions(in Input, limit int) []KeyAction {
	out := []KeyAction{}
	for _, f := range reportFindings(in) {
		var l *FindingLifecycle
		if in.Lifecycle != nil {
			v := in.Lifecycle[f.ID]
			l = &v
		}
		if closed(f, l) {
			continue
		}
		out = append(out, KeyAction{f.ID, findingTitle(f), normSeverity(f.Severity), nextAction(f, l)})
		if len(out) == limit {
			break
		}
	}
	return out
}

// posture summarizes how many findings are open, fixed or dismissed.
func posture(in Input) string {
	fs := reportFindings(in)
	if len(fs) == 0 {
		return "No findings are registered."
	}
	var open, fixed, dismissed, validated int
	for _, f := range fs {
		var l *FindingLifecycle
		if in.Lifecycle != nil {
			v := in.Lifecycle[f.ID]
			l = &v
		}
		switch {
		case l != nil && l.Stage() == StageValidated:
			validated++
		case f.Status == db.FindingFixed || f.Status == db.FindingResolved:
			fixed++
		case closed(f, l):
			dismissed++
		default:
			open++
		}
	}
	parts := []string{fmt.Sprintf("%d open", open)}
	if validated > 0 {
		parts = append(parts, fmt.Sprintf("%d fixed and validated after merge", validated))
	}
	if fixed > 0 {
		parts = append(parts, fmt.Sprintf("%d fixed", fixed))
	}
	if dismissed > 0 {
		parts = append(parts, fmt.Sprintf("%d dismissed (false positive, duplicate, ignored or risk accepted)", dismissed))
	}
	return plural(len(fs), "finding", "findings") + ": " + strings.Join(parts, ", ") + "."
}

// DocumentID names one export of one task, e.g. "CW-12-20261109-083000".
func (in Input) DocumentID() string {
	return fmt.Sprintf("CW-%s-%s", nz(in.TaskID, "0"), in.GeneratedAt.Format("20060102-150405"))
}

// ContentFingerprint is a SHA-256 over the reported findings (identity, rating,
// status, recorded text, evidence versions and linked traffic). Two exports with the same fingerprint report the
// same findings, whatever their layout or generation time.
func (in Input) ContentFingerprint() string {
	h := sha256.New()
	fs := append([]*db.DBFinding(nil), reportFindings(in)...)
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].ID < fs[j].ID })
	for _, f := range fs {
		fmt.Fprintf(h, "%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d",
			f.ID, f.Severity, f.Status, f.Name, f.VulnClass, f.Summary, f.Evidence, f.Report, f.EvidenceVersion, f.ReportEvidenceVersion)
		for _, b := range f.TrafficBindings {
			fmt.Fprintf(h, "\x1f%d\x00%s\x00%s", b.ID, b.Role, b.Note)
		}
		h.Write([]byte{0x1e})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TraceStep is one hop of a source-to-sink code trace.
type TraceStep struct {
	Kind     string `json:"kind"` // entrypoint, propagation, sink, source, check...
	Location string `json:"location,omitempty"`
	Detail   string `json:"detail"`
}

// EvidenceField is a labelled line of recorded evidence, such as "Root cause".
type EvidenceField struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

// EvidenceTrace is recorded evidence split into its code trace and labelled
// fields. Parsed is false when the text does not follow that structure; the raw
// evidence is always kept alongside.
type EvidenceTrace struct {
	Parsed bool            `json:"parsed"`
	Steps  []TraceStep     `json:"steps"`
	Fields []EvidenceField `json:"fields"`
}

var (
	traceStepRe  = regexp.MustCompile(`(?i)^\s*\d+[.)]\s+(entry\s*point|entrypoint|source|propagation|transform|validation|check|guard|sink)\b[\s:–—-]*(.*)$`)
	traceLocRe   = regexp.MustCompile(`^((?:[A-Za-z0-9_.@-]+/)*[A-Za-z0-9_.@-]+\.[A-Za-z0-9]+:\d+(?:-\d+)?)[\s:–—-]*(.*)$`)
	traceFieldRe = regexp.MustCompile(`^(Fingerprint|Ref|Root cause|Conditions|Remediation|Concrete input|Impact|Intended behavior|Related variant|Sanitizer|Note)s?:\s*(.*)$`)
	traceHeadRe  = regexp.MustCompile(`(?i)^trace\b.*:\s*$`)
)

// parseEvidence recognizes the trace format the code-review methodology asks
// for. Lines that fit neither a step nor a field continue the previous item.
func parseEvidence(s string) EvidenceTrace {
	out := EvidenceTrace{Steps: []TraceStep{}, Fields: []EvidenceField{}}
	last := ""
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "" || traceHeadRe.MatchString(line):
			continue
		case traceStepRe.MatchString(line):
			m := traceStepRe.FindStringSubmatch(line)
			step := TraceStep{Kind: strings.ToLower(strings.ReplaceAll(m[1], " ", "")), Detail: m[2]}
			if lm := traceLocRe.FindStringSubmatch(m[2]); lm != nil {
				step.Location, step.Detail = lm[1], lm[2]
			}
			out.Steps = append(out.Steps, step)
			last = "step"
		case traceFieldRe.MatchString(line):
			m := traceFieldRe.FindStringSubmatch(line)
			out.Fields = append(out.Fields, EvidenceField{m[1], m[2]})
			last = "field"
		case last == "step":
			st := &out.Steps[len(out.Steps)-1]
			st.Detail = strings.TrimSpace(st.Detail + " " + line)
		case last == "field":
			fl := &out.Fields[len(out.Fields)-1]
			fl.Text = strings.TrimSpace(fl.Text + " " + line)
		default:
			out.Fields = append(out.Fields, EvidenceField{"Note", line})
			last = "field"
		}
	}
	out.Parsed = len(out.Steps) >= 2
	return out
}

// FindingClosed reports whether a finding needs no further remediation work:
// fixed, dismissed, or validated after a merged fix.
func FindingClosed(f *db.DBFinding, l *FindingLifecycle) bool { return closed(f, l) }

// StageLabel names a lifecycle stage for readers.
func StageLabel(stage string) string { return stageLabel(stage) }

// FindingStatusLabel names a review status for readers.
func FindingStatusLabel(status string) string { return findingStatusLabel(status) }
