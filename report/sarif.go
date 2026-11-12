package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/db"
)

// SARIF 2.1.0 export. The log carries one run for Counterweight with a rule
// per vulnerability class and a result per finding. Findings are never
// dropped: closed ones carry a suppression. Black-box findings have no source
// location and are ignored by GitHub code scanning (it only shows results that
// point at a file in the repository), but other tools still read them.

const (
	// SARIFMediaType is the registered media type of SARIF logs.
	SARIFMediaType = "application/sarif+json"

	sarifSchema   = "https://json.schemastore.org/sarif-2.1.0.json"
	sarifToolName = "Counterweight"
	sarifToolURI  = "https://github.com/Philocyber/counterweight"
	sarifBaseID   = "%SRCROOT%"
	// sarifFingerprintKey names the line-independent fingerprint of a result.
	sarifFingerprintKey = "counterweightFinding/v1"
	sarifMaxRelated     = 20
	sarifMaxAffected    = 10
	sarifMessageRunes   = 1000
)

// SARIFInput is what a SARIF log is built from.
type SARIFInput struct {
	Execution   string
	Findings    []*db.DBFinding
	GeneratedAt time.Time
	Version     string
	// CodeReview names the reviewed repository of a finding, or nil for a
	// black-box finding. Source locations are read only for code reviews.
	CodeReview func(*db.DBFinding) *CodeReview
	// Lifecycle maps finding IDs to verification and fix state; optional.
	Lifecycle map[int64]FindingLifecycle
	// Affected lists the assets or URLs a finding concerns, by finding ID.
	Affected map[int64][]string
	// Keep, when set, leaves out the findings it rejects, for example those
	// outside the change a pull request review covers.
	Keep func(*db.DBFinding) bool
	// InChange, when set, records on every result whether the finding lies
	// inside the change (property inChange).
	InChange func(*db.DBFinding) bool
	// RepoRelative gives locations as plain paths from the repository root,
	// without the %SRCROOT% base, which is what GitHub code scanning places.
	RepoRelative bool
}

// SARIFFromInput builds the log for a task report: every finding is judged by
// the task's own code-review metadata. affected names the assets per finding.
func SARIFFromInput(in Input, affected map[int64][]string) ([]byte, error) {
	return SARIF(SARIFInput{
		Execution: in.ExecutionIsolation().Description(),
		Findings:  reportFindings(in), GeneratedAt: in.GeneratedAt, Version: in.Version,
		CodeReview: func(*db.DBFinding) *CodeReview { return in.CodeReview },
		Lifecycle:  in.Lifecycle, Affected: affected,
	})
}

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Properties               map[string]any           `json:"properties,omitempty"`
	Tool                     sarifTool                `json:"tool"`
	Invocations              []sarifInvocation        `json:"invocations,omitempty"`
	VersionControlProvenance []sarifVersionControl    `json:"versionControlProvenance,omitempty"`
	OriginalURIBaseIDs       map[string]sarifLocation `json:"originalUriBaseIds,omitempty"`
	Results                  []sarifResult            `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version,omitempty"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifText struct {
	Text     string `json:"text"`
	Markdown string `json:"markdown,omitempty"`
}

type sarifRule struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	ShortDescription sarifText      `json:"shortDescription"`
	FullDescription  sarifText      `json:"fullDescription"`
	Help             sarifText      `json:"help"`
	HelpURI          string         `json:"helpUri,omitempty"`
	Properties       map[string]any `json:"properties"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool   `json:"executionSuccessful"`
	EndTimeUTC          string `json:"endTimeUtc,omitempty"`
}

type sarifVersionControl struct {
	RepositoryURI string `json:"repositoryUri"`
	RevisionID    string `json:"revisionId,omitempty"`
	Branch        string `json:"branch,omitempty"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           int               `json:"ruleIndex"`
	Level               string            `json:"level"`
	Message             sarifText         `json:"message"`
	Locations           []sarifLocation   `json:"locations,omitempty"`
	RelatedLocations    []sarifLocation   `json:"relatedLocations,omitempty"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Suppressions        []sarifSuppress   `json:"suppressions,omitempty"`
	Properties          map[string]any    `json:"properties"`
}

type sarifSuppress struct {
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	Justification string `json:"justification"`
}

type sarifLocation struct {
	ID               int            `json:"id,omitempty"`
	PhysicalLocation *sarifPhysical `json:"physicalLocation,omitempty"`
	Message          *sarifText     `json:"message,omitempty"`
	// URI is set only on originalUriBaseIds entries.
	URI string `json:"uri,omitempty"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId,omitempty"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine,omitempty"`
}

// sarifSecurity maps a severity to the CVSS-like score GitHub bands into
// critical (over 9), high (7 to 8.9), medium (4 to 6.9) and low (under 4).
var sarifSecurity = map[string]string{"critical": "9.5", "high": "8.0", "medium": "5.5", "low": "3.0", "info": "0.0"}

func sarifLevel(severity string) string {
	switch severity {
	case "critical", "high":
		return "error"
	case "medium":
		return "warning"
	}
	return "note"
}

func sarifScore(severity string) string {
	if s, ok := sarifSecurity[severity]; ok {
		return s
	}
	return "0.0"
}

// sarifSuppressed lists the statuses that take a finding out of the open set.
var sarifSuppressed = map[string]bool{
	db.FindingFixed: true, db.FindingResolved: true, db.FindingFalsePositive: true,
	db.FindingDuplicate: true, db.FindingIgnored: true, db.FindingRiskAccepted: true,
}

var (
	slugRe     = regexp.MustCompile(`[^a-z0-9]+`)
	windowsAbs = regexp.MustCompile(`^[A-Za-z]:`)
)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// sarifClean removes credentials and machine paths from export text.
func sarifClean(s string) string { return RedactLocalPaths(RedactSecrets(s)) }

// sarifURI turns a cited file into a repository-relative POSIX path, or "" when
// it points outside the repository or at the machine that ran the review.
func sarifURI(file string) string {
	file = strings.ReplaceAll(RedactLocalPaths(file), `\`, "/")
	if i := strings.Index(file, CheckoutPlaceholder+"/"); i >= 0 {
		file = file[i+len(CheckoutPlaceholder)+1:]
	}
	file = strings.TrimPrefix(path.Clean(strings.TrimPrefix(file, "./")), "/")
	if file == "." || file == ".." || strings.HasPrefix(file, "../") || strings.ContainsAny(file, "<>") || windowsAbs.MatchString(file) {
		return ""
	}
	return file
}

// sarifRegion reads "12" or "12-18".
func parseRegion(lines string) *sarifRegion {
	a, b, _ := strings.Cut(lines, "-")
	start, err := strconv.Atoi(a)
	if err != nil || start < 1 {
		return nil
	}
	r := &sarifRegion{StartLine: start}
	if end, err := strconv.Atoi(b); err == nil && end > start {
		r.EndLine = end
	}
	return r
}

// sarifLocations flattens the cited files and lines: the first is the primary
// location, the rest are related locations.
func sarifLocations(f *db.DBFinding) (primary *sarifLocation, related []sarifLocation) {
	var all []sarifLocation
	for _, loc := range codeLocations(f) {
		uri := sarifURI(loc.File)
		if uri == "" {
			continue
		}
		for _, lines := range loc.Lines {
			region := parseRegion(lines)
			if region == nil {
				continue
			}
			all = append(all, sarifLocation{PhysicalLocation: &sarifPhysical{
				ArtifactLocation: sarifArtifact{URI: uri, URIBaseID: sarifBaseID}, Region: region,
			}})
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	primary = &all[0]
	for i, l := range all[1:] {
		if i == sarifMaxRelated {
			break
		}
		l.ID = i + 1
		l.Message = &sarifText{Text: "Also cited by this finding"}
		related = append(related, l)
	}
	return primary, related
}

// relativeLocations drops the base id of each location so its uri is a plain
// path from the repository root.
func relativeLocations(primary *sarifLocation, related []sarifLocation) (*sarifLocation, []sarifLocation) {
	strip := func(l sarifLocation) sarifLocation {
		if l.PhysicalLocation != nil {
			p := *l.PhysicalLocation
			p.ArtifactLocation.URIBaseID = ""
			l.PhysicalLocation = &p
		}
		return l
	}
	if primary != nil {
		l := strip(*primary)
		primary = &l
	}
	for i := range related {
		related[i] = strip(related[i])
	}
	return primary, related
}

// sarifFingerprint identifies a finding without its line numbers: class, file
// (or affected asset for black-box findings) and a normalised title.
func sarifFingerprint(ruleID, where, title string) string {
	norm := strings.Join(strings.Fields(slugRe.ReplaceAllString(strings.ToLower(title), " ")), " ")
	sum := sha256.Sum256([]byte(ruleID + "\x00" + where + "\x00" + norm))
	return hex.EncodeToString(sum[:])
}

// pascal turns "SQL injection" into "SQLInjection".
func pascal(s string) string {
	var b strings.Builder
	for _, w := range strings.Fields(slugRe.ReplaceAllString(s, " ")) {
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

func firstParagraph(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n\n"); i >= 0 {
		s = s[:i]
	}
	s, _ = clipRunes(strings.Join(strings.Fields(s), " "), sarifMessageRunes)
	return s
}

func sarifRuleFor(info ClassInfo, id string) sarifRule {
	label := nz(info.Label, "Unclassified")
	full := label + "."
	help := "Review the finding message and its Counterweight report for the affected code and the recommended fix."
	md := help
	tags := []string{"security"}
	rule := sarifRule{ID: id, Name: pascal(label)}
	if info.CWE != "" {
		full += " " + info.CWE + "."
		tags = append(tags, "external/cwe/"+strings.ToLower(info.CWE))
		rule.HelpURI = cweLink(info.CWE)
		help += " Reference: " + rule.HelpURI
		md += " Reference: [" + info.CWE + "](" + rule.HelpURI + ")"
	}
	if info.OWASP != "" {
		full += " OWASP Top 10: " + info.OWASP + "."
	}
	rule.ShortDescription = sarifText{Text: label}
	rule.FullDescription = sarifText{Text: full}
	rule.Help = sarifText{Text: help, Markdown: md}
	rule.Properties = map[string]any{"tags": tags}
	return rule
}

// SARIF renders a SARIF 2.1.0 log. All text is redacted like the other exports.
func SARIF(in SARIFInput) ([]byte, error) {
	items := append([]*db.DBFinding(nil), in.Findings...)
	sortFindingsForExport(items)

	rules := []sarifRule{}
	ruleIndex := map[string]int{}
	worst := map[string]string{} // rule id -> highest severity of its findings
	results := []sarifResult{}
	var repos []*CodeReview
	seenRepo := map[CodeReview]bool{}

	for _, f := range items {
		if in.Keep != nil && !in.Keep(f) {
			continue
		}
		info := classInfo(f.VulnClass)
		ruleID := slug(nz(info.Label, "unclassified"))
		sev := normSeverity(f.Severity)
		idx, ok := ruleIndex[ruleID]
		if !ok {
			idx = len(rules)
			ruleIndex[ruleID] = idx
			rules = append(rules, sarifRuleFor(info, ruleID))
			worst[ruleID] = sev
		} else if severityRank(sev) < severityRank(worst[ruleID]) {
			worst[ruleID] = sev
		}

		title := sarifClean(findingTitle(f))
		message := title
		if s := sarifClean(firstParagraph(f.Summary)); s != "" {
			message += ": " + s
		}
		status := nz(f.Status, db.FindingPending)
		res := sarifResult{
			RuleID: ruleID, RuleIndex: idx, Level: sarifLevel(sev),
			Properties: map[string]any{
				"counterweightFindingId": f.ID, "status": status, "severity": sev,
				"security-severity": sarifScore(sev),
			},
		}
		if in.InChange != nil {
			res.Properties["inChange"] = in.InChange(f)
		}
		if l, ok := in.Lifecycle[f.ID]; ok {
			res.Properties["verification"] = nz(l.Verification, "unverified")
			res.Properties["stage"] = l.Stage()
		}

		where := ""
		var cr *CodeReview
		if in.CodeReview != nil {
			cr = in.CodeReview(f)
		}
		if cr != nil {
			primary, related := sarifLocations(f)
			if in.RepoRelative {
				primary, related = relativeLocations(primary, related)
			}
			if primary != nil {
				res.Locations = []sarifLocation{*primary}
				res.RelatedLocations = related
				where = primary.PhysicalLocation.ArtifactLocation.URI
			}
			if (cr.URL != "" || cr.Commit != "") && !seenRepo[*cr] {
				seenRepo[*cr] = true
				repos = append(repos, cr)
			}
			res.Properties["repository"] = sarifClean(cr.Repository)
		}
		if where == "" {
			affected := sarifAffected(f, in.Affected[f.ID])
			if len(affected) > 0 {
				where = affected[0]
				res.Properties["affected"] = affected
				message += " Affected: " + strings.Join(affected, ", ") + "."
			}
		}
		res.Message = sarifText{Text: message}
		res.PartialFingerprints = map[string]string{sarifFingerprintKey: sarifFingerprint(ruleID, where, title)}
		if sarifSuppressed[status] {
			res.Suppressions = []sarifSuppress{{Kind: "external", Status: "accepted", Justification: findingStatusLabel(status)}}
		}
		results = append(results, res)
	}
	for i := range rules {
		rules[i].Properties["security-severity"] = sarifScore(worst[rules[i].ID])
	}

	run := sarifRun{
		Properties: map[string]any{"execution": in.Execution},
		Tool: sarifTool{Driver: sarifDriver{
			Name: sarifToolName, Version: nz(in.Version, "dev"), InformationURI: sarifToolURI, Rules: rules,
		}},
		Results: results,
	}
	if !in.GeneratedAt.IsZero() {
		run.Invocations = []sarifInvocation{{ExecutionSuccessful: true, EndTimeUTC: in.GeneratedAt.UTC().Format(time.RFC3339)}}
	}
	for _, cr := range repos {
		if cr.URL == "" {
			continue
		}
		run.VersionControlProvenance = append(run.VersionControlProvenance, sarifVersionControl{
			RepositoryURI: sarifClean(cr.URL), RevisionID: cr.Commit, Branch: cr.Ref,
		})
	}
	if len(repos) == 1 && repos[0].URL != "" && !in.RepoRelative {
		base := nz(repos[0].blobBase(), strings.TrimSuffix(strings.TrimSuffix(repos[0].URL, "/"), ".git")+"/")
		run.OriginalURIBaseIDs = map[string]sarifLocation{sarifBaseID: {URI: sarifClean(base)}}
	}
	return json.MarshalIndent(sarifLog{Schema: sarifSchema, Version: "2.1.0", Runs: []sarifRun{run}}, "", "  ")
}

// sarifAffected lists, redacted and without repeats, what a black-box finding
// concerns: its recorded assets, then the URLs of its linked traffic.
func sarifAffected(f *db.DBFinding, assets []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = sarifClean(strings.TrimSpace(s))
		if s != "" && !seen[s] && len(out) < sarifMaxAffected {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, a := range assets {
		add(a)
	}
	for _, b := range f.TrafficBindings {
		add(b.Snapshot.URL)
	}
	sort.Strings(out)
	return out
}
