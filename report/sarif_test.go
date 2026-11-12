package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

type jm = map[string]any

func sarifDecode(t *testing.T, in SARIFInput) jm {
	t.Helper()
	raw, err := SARIF(in)
	if err != nil {
		t.Fatal(err)
	}
	var log jm
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	validateSARIF(t, log)
	return log
}

func sarifResults(log jm) []any { return log["runs"].([]any)[0].(jm)["results"].([]any) }

// validateSARIF checks the parts of the 2.1.0 schema GitHub relies on:
// required properties, types and enum values.
func validateSARIF(t *testing.T, log jm) {
	t.Helper()
	if log["version"] != "2.1.0" {
		t.Errorf("version = %v", log["version"])
	}
	if s, _ := log["$schema"].(string); !strings.HasPrefix(s, "https://") || !strings.Contains(s, "sarif-2.1.0") {
		t.Errorf("$schema = %v", log["$schema"])
	}
	runs, ok := log["runs"].([]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs = %v", log["runs"])
	}
	run := runs[0].(jm)
	driver := run["tool"].(jm)["driver"].(jm)
	for _, k := range []string{"name", "informationUri", "version"} {
		if s, _ := driver[k].(string); s == "" {
			t.Errorf("driver.%s missing", k)
		}
	}
	if !strings.Contains(driver["informationUri"].(string), "github.com/Philocyber/counterweight") {
		t.Errorf("informationUri = %v", driver["informationUri"])
	}
	rules, _ := driver["rules"].([]any)
	ids := map[string]bool{}
	for _, r := range rules {
		rule := r.(jm)
		id, _ := rule["id"].(string)
		if id == "" || ids[id] {
			t.Errorf("rule id %q empty or repeated", id)
		}
		ids[id] = true
		props := rule["properties"].(jm)
		if _, ok := props["tags"].([]any); !ok {
			t.Errorf("rule %s tags missing", id)
		}
		if s, _ := props["security-severity"].(string); s == "" {
			t.Errorf("rule %s security-severity missing", id)
		}
		if rule["shortDescription"].(jm)["text"] == "" || rule["help"].(jm)["text"] == "" {
			t.Errorf("rule %s descriptions missing", id)
		}
	}
	results, ok := run["results"].([]any)
	if !ok {
		t.Fatal("results must be an array")
	}
	for i, r := range results {
		res := r.(jm)
		if !ids[res["ruleId"].(string)] {
			t.Errorf("result %d: unknown rule %v", i, res["ruleId"])
		}
		rule := rules[int(res["ruleIndex"].(float64))].(jm)
		if rule["id"] != res["ruleId"] {
			t.Errorf("result %d: ruleIndex points at %v", i, rule["id"])
		}
		switch res["level"] {
		case "none", "note", "warning", "error":
		default:
			t.Errorf("result %d: bad level %v", i, res["level"])
		}
		if res["message"].(jm)["text"] == "" {
			t.Errorf("result %d: empty message", i)
		}
		if fp, _ := res["partialFingerprints"].(jm); len(fp) == 0 {
			t.Errorf("result %d: no fingerprint", i)
		}
		var locs []any
		locs = append(locs, anySlice(res["locations"])...)
		locs = append(locs, anySlice(res["relatedLocations"])...)
		for _, l := range locs {
			phys := l.(jm)["physicalLocation"].(jm)
			uri := phys["artifactLocation"].(jm)["uri"].(string)
			if uri == "" || strings.HasPrefix(uri, "/") || strings.Contains(uri, `\`) || strings.Contains(uri, "..") {
				t.Errorf("result %d: bad uri %q", i, uri)
			}
			region := phys["region"].(jm)
			start := region["startLine"].(float64)
			if start < 1 {
				t.Errorf("result %d: startLine %v", i, start)
			}
			if end, ok := region["endLine"].(float64); ok && end < start {
				t.Errorf("result %d: endLine before startLine", i)
			}
		}
		for _, s := range anySlice(res["suppressions"]) {
			sup := s.(jm)
			if k := sup["kind"]; k != "inSource" && k != "external" {
				t.Errorf("result %d: bad suppression kind %v", i, k)
			}
			if st, ok := sup["status"]; ok && st != "accepted" && st != "underReview" && st != "rejected" {
				t.Errorf("result %d: bad suppression status %v", i, st)
			}
		}
	}
}

func anySlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func codeReviewFor(cr *CodeReview) func(*db.DBFinding) *CodeReview {
	return func(*db.DBFinding) *CodeReview { return cr }
}

var testReview = &CodeReview{Repository: "acme/shop", URL: "https://github.com/acme/shop", Ref: "main", Commit: "0123456789abcdef0123456789abcdef01234567"}

func sqliFinding() *db.DBFinding {
	return &db.DBFinding{
		ID: 7, VulnClass: "sqli", Name: "SQL injection in order search", Severity: "high", Status: db.FindingConfirmed,
		Summary:  "The search term is concatenated into a query.\n\nSecond paragraph is not in the message.",
		Evidence: "source: app/orders/search.go:42-48 sink: app/db/query.go:10",
	}
}

func TestSARIFCodeReviewFindingWithTwoLocations(t *testing.T) {
	log := sarifDecode(t, SARIFInput{
		Findings: []*db.DBFinding{sqliFinding()}, GeneratedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
		Version: "1.2.3", CodeReview: codeReviewFor(testReview),
		Lifecycle: map[int64]FindingLifecycle{7: {Verification: "confirmed"}},
	})
	run := log["runs"].([]any)[0].(jm)
	driver := run["tool"].(jm)["driver"].(jm)
	if driver["name"] != "Counterweight" || driver["version"] != "1.2.3" {
		t.Errorf("driver = %v", driver)
	}
	rule := driver["rules"].([]any)[0].(jm)
	props := rule["properties"].(jm)
	if rule["id"] != "sql-injection" || props["security-severity"] != "8.0" {
		t.Errorf("rule = %v", rule)
	}
	tags := props["tags"].([]any)
	if len(tags) != 2 || tags[0] != "security" || tags[1] != "external/cwe/cwe-89" {
		t.Errorf("tags = %v", tags)
	}
	res := sarifResults(log)[0].(jm)
	if res["level"] != "error" {
		t.Errorf("level = %v", res["level"])
	}
	if got := res["message"].(jm)["text"]; got != "SQL injection in order search: The search term is concatenated into a query." {
		t.Errorf("message = %q", got)
	}
	locs := res["locations"].([]any)
	if len(locs) != 1 {
		t.Fatalf("locations = %v", locs)
	}
	phys := locs[0].(jm)["physicalLocation"].(jm)
	if phys["artifactLocation"].(jm)["uri"] != "app/orders/search.go" ||
		phys["region"].(jm)["startLine"] != 42.0 || phys["region"].(jm)["endLine"] != 48.0 {
		t.Errorf("primary = %v", phys)
	}
	rel := res["relatedLocations"].([]any)
	if len(rel) != 1 {
		t.Fatalf("related = %v", rel)
	}
	rp := rel[0].(jm)["physicalLocation"].(jm)
	if rp["artifactLocation"].(jm)["uri"] != "app/db/query.go" || rp["region"].(jm)["startLine"] != 10.0 {
		t.Errorf("related = %v", rp)
	}
	if _, has := rp["region"].(jm)["endLine"]; has {
		t.Errorf("single line must not carry endLine: %v", rp)
	}
	rprops := res["properties"].(jm)
	if rprops["counterweightFindingId"] != 7.0 || rprops["status"] != "confirmed" || rprops["verification"] != "confirmed" {
		t.Errorf("properties = %v", rprops)
	}
	if _, has := res["suppressions"]; has {
		t.Errorf("open finding must not be suppressed")
	}
	vcp := run["versionControlProvenance"].([]any)[0].(jm)
	if vcp["repositoryUri"] != "https://github.com/acme/shop" || vcp["revisionId"] != testReview.Commit || vcp["branch"] != "main" {
		t.Errorf("provenance = %v", vcp)
	}
	base := run["originalUriBaseIds"].(jm)["%SRCROOT%"].(jm)["uri"].(string)
	if base != "https://github.com/acme/shop/blob/"+testReview.Commit+"/" {
		t.Errorf("base = %q", base)
	}
}

func TestSARIFBlackBoxFindingHasNoLocation(t *testing.T) {
	f := &db.DBFinding{ID: 3, VulnClass: "xss", Name: "Reflected XSS", Severity: "medium", Summary: "q is reflected.",
		Evidence: "see main.go:12 in the vendor docs"}
	log := sarifDecode(t, SARIFInput{
		Findings: []*db.DBFinding{f}, Affected: map[int64][]string{3: {"https://app.example.test/search"}},
	})
	res := sarifResults(log)[0].(jm)
	if _, has := res["locations"]; has {
		t.Errorf("black-box finding must have no locations: %v", res["locations"])
	}
	if res["level"] != "warning" {
		t.Errorf("level = %v", res["level"])
	}
	if !strings.Contains(res["message"].(jm)["text"].(string), "Affected: https://app.example.test/search.") {
		t.Errorf("message = %v", res["message"])
	}
	aff := res["properties"].(jm)["affected"].([]any)
	if len(aff) != 1 || aff[0] != "https://app.example.test/search" {
		t.Errorf("affected = %v", aff)
	}
	run := log["runs"].([]any)[0].(jm)
	if _, has := run["versionControlProvenance"]; has {
		t.Errorf("no provenance without a code review")
	}
}

func TestSARIFClosedFindingsAreSuppressedNotDropped(t *testing.T) {
	var fs []*db.DBFinding
	for i, st := range []string{db.FindingFixed, db.FindingFalsePositive, db.FindingDuplicate, db.FindingPending} {
		f := sqliFinding()
		f.ID, f.Status = int64(i+1), st
		fs = append(fs, f)
	}
	results := sarifResults(sarifDecode(t, SARIFInput{Findings: fs, CodeReview: codeReviewFor(testReview)}))
	if len(results) != 4 {
		t.Fatalf("results = %d, want 4", len(results))
	}
	want := map[float64]string{1: "Fixed", 2: "False positive", 3: "Duplicate"}
	for _, r := range results {
		res := r.(jm)
		id := res["properties"].(jm)["counterweightFindingId"].(float64)
		sup := anySlice(res["suppressions"])
		if label, closed := want[id]; closed {
			if len(sup) != 1 || sup[0].(jm)["kind"] != "external" || sup[0].(jm)["justification"] != label {
				t.Errorf("finding %v suppressions = %v", id, sup)
			}
		} else if len(sup) != 0 {
			t.Errorf("finding %v should be open: %v", id, sup)
		}
	}
}

func TestSARIFRedactsSecretsAndLocalPaths(t *testing.T) {
	const secret = "sk_live_abcdefghijklmnop1234"
	f := &db.DBFinding{
		ID: 9, VulnClass: "hardcoded_secret", Name: "Key in C:\\Users\\user\\work", Severity: "critical",
		Summary:  "The token=" + secret + " is committed. Found under /home/user/codereview/5/src/app/cfg.go:3",
		Evidence: "see ./app/cfg.go:3 uses Authorization: Bearer " + secret,
	}
	raw, err := SARIF(SARIFInput{
		Findings: []*db.DBFinding{f}, CodeReview: codeReviewFor(&CodeReview{Repository: "acme/shop", URL: "https://user:hunter2pw@github.com/acme/shop"}),
		Affected: map[int64][]string{9: {"https://x.example.test/?api_key=" + secret}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	for _, bad := range []string{secret, "user", "hunter2pw", "/home/", "C:\\\\Users"} {
		if strings.Contains(out, bad) {
			t.Errorf("output leaks %q", bad)
		}
	}
	log := sarifDecode(t, SARIFInput{Findings: []*db.DBFinding{f}, CodeReview: codeReviewFor(testReview)})
	res := sarifResults(log)[0].(jm)
	if uri := res["locations"].([]any)[0].(jm)["physicalLocation"].(jm)["artifactLocation"].(jm)["uri"]; uri != "app/cfg.go" {
		t.Errorf("uri = %v, want repository-relative app/cfg.go", uri)
	}
}

func TestSARIFFingerprintIgnoresLineNumbers(t *testing.T) {
	fp := func(f *db.DBFinding) string {
		res := sarifResults(sarifDecode(t, SARIFInput{Findings: []*db.DBFinding{f}, CodeReview: codeReviewFor(testReview)}))[0].(jm)
		return res["partialFingerprints"].(jm)[sarifFingerprintKey].(string)
	}
	a := sqliFinding()
	b := sqliFinding()
	b.Evidence = "source: app/orders/search.go:90-96 sink: app/db/query.go:55"
	b.Name = "  SQL Injection in order  SEARCH "
	if fp(a) != fp(b) {
		t.Errorf("fingerprint changed with line numbers or title casing")
	}
	c := sqliFinding()
	c.Evidence = "source: app/orders/list.go:42-48"
	if fp(a) == fp(c) {
		t.Errorf("fingerprint must change with the file")
	}
}
