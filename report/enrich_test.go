package report

import (
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

func TestClassInfoMapsKnownClassesAndHumanizesOthers(t *testing.T) {
	for in, want := range map[string]ClassInfo{
		"sqli":                     {Key: "sqli", Label: "SQL injection", CWE: "CWE-89", OWASP: owaspInjection},
		"insecure_deserialization": {Key: "insecure_deserialization", Label: "Insecure deserialization", CWE: "CWE-502", OWASP: owaspIntegrity},
		"hardcoded-credential":     {Key: "hardcoded-credential", Label: "Hard-coded credentials", CWE: "CWE-798", OWASP: owaspAuth},
		"Auth Bypass":              {Key: "Auth Bypass", Label: "Authentication bypass", CWE: "CWE-287", OWASP: owaspAuth},
		"weird_new_class":          {Key: "weird_new_class", Label: "Weird new class"},
	} {
		if got := classInfo(in); got != want {
			t.Errorf("classInfo(%q) = %+v, want %+v", in, got, want)
		}
	}
	if got := classInfo(""); got != (ClassInfo{}) {
		t.Errorf("empty class = %+v", got)
	}
}

func TestMergeRangesJoinsOverlapsAndNeighbours(t *testing.T) {
	got := mergeRanges([]string{"114", "101-102", "101", "103-112", "270-281", "26", "28"})
	want := "26,28,101-112,114,270-281"
	if strings.Join(got, ",") != want {
		t.Fatalf("mergeRanges = %v, want %s", got, want)
	}
}

func TestRedactLocalPathsKeepsEvidencePaths(t *testing.T) {
	for in, want := range map[string]string{
		`cloned to C:\counterweight\data\codereview\repo-abc123\src. Review`: `cloned to <checkout>. Review`,
		`read C:\\Users\\user\\Desktop\\x.txt`:                               `read <home>\\Desktop\\x.txt`,
		`at /app/data/codereview/repo-1/src/app/app.py:12`:                   `at <checkout>/app/app.py:12`,
		`home /home/user/project and /Users/me/x`:                            `home <home>/project and <home>/x`,
		`upload saved to /tmp/uploads and C:\Windows\Temp`:                   `upload saved to /tmp/uploads and C:\Windows\Temp`,
		`see https://example.com/a:b`:                                        `see https://example.com/a:b`,
		`open C:\Users\Alice Smith\Desktop\notes.txt now`:                    `open <home>\Desktop\notes.txt now`,
		`https://example.com/data/codereview/repo-1/src/app.py`:              `https://example.com/data/codereview/repo-1/src/app.py`,
		`docs at https://host.example/Users/me/page`:                         `docs at https://host.example/Users/me/page`,
		`path="C:\Users\user\x"`:                                             `path="<home>\x"`,
	} {
		if got := RedactLocalPaths(in); got != want {
			t.Errorf("RedactLocalPaths(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSourceLinkPointsAtCommit(t *testing.T) {
	cr := &CodeReview{URL: "https://github.com/acme/app", Commit: "abc123"}
	if got := cr.SourceLink("app/app.py", "10-14"); got != "https://github.com/acme/app/blob/abc123/app/app.py#L10-L14" {
		t.Fatal(got)
	}
	if got := (&CodeReview{URL: "https://gitlab.com/x/y", Commit: "abc"}).SourceLink("a.go", "1"); got != "" {
		t.Fatal("non-GitHub repositories have no permalink", got)
	}
}

func TestParseEvidenceSplitsTraceAndFields(t *testing.T) {
	ev := strings.Join([]string{
		"Fingerprint: app/404:untrusted-url",
		"Ref: b6a4f97a",
		"Trace (confirmed by source trace, not executed):",
		"1. entrypoint app/app.py:101-102: @app.errorhandler(404) accepts requests.",
		"2. propagation – app/app.py:103-112 the handler builds a template",
		"   from request.url.",
		"3. sink app/app.py:114: render_template_string(template).",
		"Root cause: the handler treats URL data as template source.",
		"Remediation: use a fixed template.",
	}, "\n")
	got := parseEvidence(ev)
	if !got.Parsed || len(got.Steps) != 3 {
		t.Fatalf("parsed = %+v", got)
	}
	if s := got.Steps[1]; s.Kind != "propagation" || s.Location != "app/app.py:103-112" || s.Detail != "the handler builds a template from request.url." {
		t.Fatalf("step 2 = %+v", s)
	}
	if got.Steps[0].Kind != "entrypoint" || got.Steps[2].Location != "app/app.py:114" {
		t.Fatalf("steps = %+v", got.Steps)
	}
	labels := []string{}
	for _, f := range got.Fields {
		labels = append(labels, f.Label)
	}
	if strings.Join(labels, ",") != "Fingerprint,Ref,Root cause,Remediation" {
		t.Fatalf("fields = %v", labels)
	}
	if parseEvidence("free text with no structure").Parsed {
		t.Fatal("unstructured evidence must not be reported as parsed")
	}
}

func TestKeyActionsAndPostureFollowLifecycle(t *testing.T) {
	fs := []*db.DBFinding{
		{ID: 1, Name: "SQLi", Severity: "high", Status: db.FindingConfirmed},
		{ID: 2, Name: "SSTI", Severity: "critical", Status: db.FindingConfirmed},
		{ID: 3, Name: "Weak JWT", Severity: "high", Status: db.FindingConfirmed},
		{ID: 4, Name: "Noise", Severity: "low", Status: db.FindingFalsePositive},
	}
	in := Input{FindingRecords: fs, Lifecycle: map[int64]FindingLifecycle{
		1: {Verification: "confirmed", Fix: "ready", PRState: "merged", Recheck: "rejected"},
		2: {Verification: "confirmed"},
		3: {Verification: "confirmed", Fix: "ready", PRState: "open"},
	}}
	got := keyActions(in, 3)
	if len(got) != 2 || got[0].FindingID != 2 || got[1].FindingID != 3 {
		t.Fatalf("key actions = %+v", got)
	}
	if got[1].Action != "Review and merge the open pull request." {
		t.Fatalf("action = %q", got[1].Action)
	}
	if p := posture(in); p != "4 findings: 2 open, 1 fixed and validated after merge, 1 dismissed (false positive, duplicate, ignored or risk accepted)." {
		t.Fatalf("posture = %q", p)
	}
}

func TestDocumentIDAndFingerprintAreStable(t *testing.T) {
	at := time.Date(2026, 11, 9, 8, 30, 0, 0, time.UTC)
	a := Input{TaskID: "7", GeneratedAt: at, FindingRecords: []*db.DBFinding{{ID: 2, Name: "b"}, {ID: 1, Name: "a"}}}
	b := Input{TaskID: "7", GeneratedAt: at.Add(time.Hour), FindingRecords: []*db.DBFinding{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}}
	if a.DocumentID() != "CW-7-20261109-083000" {
		t.Fatal(a.DocumentID())
	}
	if a.ContentFingerprint() != b.ContentFingerprint() || len(a.ContentFingerprint()) != 64 {
		t.Fatal("fingerprint must not depend on order or generation time")
	}
	b.FindingRecords[0].Status = db.FindingFixed
	if a.ContentFingerprint() == b.ContentFingerprint() {
		t.Fatal("fingerprint must change with the findings")
	}
}
