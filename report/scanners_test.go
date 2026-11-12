package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func scannerInput() Input {
	in := codeReviewInput()
	in.CodeReview.Inferred = false
	in.Advisories = []Advisory{
		{Package: "lodash", Version: "4.17.15", ID: "GHSA-p6mc-m468-83gw", Fixed: "4.17.19", File: "web/package-lock.json",
			Link: "https://osv.dev/vulnerability/GHSA-p6mc-m468-83gw", Review: "Not reviewed"},
		{Package: "Django", Version: "2.2.0", ID: "PYSEC-2019-1", File: "api/requirements.txt",
			Link: "https://osv.dev/vulnerability/PYSEC-2019-1", Review: "Dismissed by the reviewer"},
	}
	in.SecretLeads = []SecretLead{{File: "config/prod.env", Line: 14, Rule: "aws-access-token", Review: "Covered by finding 7"}}
	return in
}

func TestScannerSectionsFollowFindingsAndAreLabelled(t *testing.T) {
	md := Markdown(scannerInput())
	find := func(s string) int {
		i := strings.Index(md, s)
		if i < 0 {
			t.Fatalf("markdown lacks %q", s)
		}
		return i
	}
	findings, adv, sec, rec := find("\n## Findings"), find("\n## Dependency advisories"), find("\n## Possible committed secrets"), find("\n## Recorded exploration conclusions")
	if !(findings < adv && adv < sec && sec < rec) {
		t.Fatalf("section order: findings=%d advisories=%d secrets=%d records=%d", findings, adv, sec, rec)
	}
	for _, want := range []string{"not verified for reachability", "not registered as findings", "[GHSA-p6mc-m468-83gw](https://osv.dev/vulnerability/GHSA-p6mc-m468-83gw)",
		"| lodash | 4.17.15 |", "| 4.17.19 |", "git history is not scanned", "never stored or shown", "| `config/prod.env` | 14 | aws-access-token | Covered by finding 7 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	// Leads are not findings: the registered-findings count is unchanged.
	count := func(s string) string {
		i := strings.Index(s, "Registered findings: **")
		return s[i : i+strings.Index(s[i:], "\n")]
	}
	if count(md) != count(Markdown(codeReviewInput())) {
		t.Fatalf("scanner leads must not change the finding count: %q", count(md))
	}
}

func TestNoScannerSectionsWithoutLeads(t *testing.T) {
	in := codeReviewInput()
	md := Markdown(in)
	if strings.Contains(md, "Dependency advisories") || strings.Contains(md, "Possible committed secrets") {
		t.Fatal("sections must not appear without leads")
	}
	v := BuildView(in)
	if len(v.Advisories) != 0 || len(v.SecretLeads) != 0 || v.Advisories == nil {
		t.Fatalf("view must carry empty lists: %+v", v)
	}
}

func TestScannerSectionsInPDFAndView(t *testing.T) {
	in := scannerInput()
	out, err := PDF(in, PDFOptions{})
	if err != nil || !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatalf("PDF: %v", err)
	}
	w := &pdfWriter{in: in, opt: PDFOptions{}, findings: reportFindings(in)}
	keys := map[string]bool{}
	for _, e := range w.reportContents() {
		keys[e.key] = true
	}
	if !keys["advisories"] || !keys["secret-leads"] {
		t.Fatalf("contents lack the scanner sections: %v", keys)
	}
	if _, err := PDF(in, PDFOptions{Variant: "executive"}); err != nil {
		t.Fatalf("executive PDF: %v", err)
	}
	raw, _ := json.Marshal(BuildView(in))
	var back struct {
		Advisories  []Advisory   `json:"advisories"`
		SecretLeads []SecretLead `json:"secret_leads"`
	}
	if err := json.Unmarshal(raw, &back); err != nil || len(back.Advisories) != 2 || len(back.SecretLeads) != 1 || back.SecretLeads[0].Line != 14 {
		t.Fatalf("view JSON: %s (%v)", raw, err)
	}
}
