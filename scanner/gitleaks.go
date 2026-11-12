package scanner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// gitleaksSpec scans the working tree only (no git history: a shallow clone has
// none) with redaction on, writing JSON to stdout. Exit status is forced to 0.
func gitleaksSpec() Spec {
	return Spec{
		Key:         Gitleaks,
		Kind:        KindSecret,
		Purpose:     "Committed secrets in the working tree (git history is not scanned)",
		ReleasesURL: "https://github.com/gitleaks/gitleaks/releases",
		Timeout:     5 * time.Minute,
		VersionArgs: []string{"version"},
		// The default rules, written outside the repository so a .gitleaks.toml
		// inside it cannot add allowlists or change rules.
		ConfigFiles: map[string]string{"gitleaks.toml": "title = \"supplementary scan\"\n\n[extend]\nuseDefault = true\n"},
		Args: func(root, workDir string) []string {
			return []string{"dir", root, "--config", filepath.Join(workDir, "gitleaks.toml"), "--redact",
				"--report-format", "json", "--report-path", "-", "--no-banner", "--exit-code", "0", "--log-level", "error"}
		},
		Parse: ParseGitleaks,
		// Older builds exit 1 when leaks are found even with the report written.
		OKExit: []int{1},
	}
}

// ParseGitleaks reads Gitleaks' JSON report. Only the rule id, description,
// file and start line are read: the report's Match and Secret fields, and every
// other field that can carry matched text, are deliberately never decoded.
func ParseGitleaks(root string, out []byte) ([]Lead, error) {
	out = bytes.TrimSpace(out)
	if len(out) == 0 || bytes.Equal(out, []byte("null")) {
		return nil, nil
	}
	var rows []struct {
		RuleID      string `json:"RuleID"`
		Description string `json:"Description"`
		File        string `json:"File"`
		StartLine   int    `json:"StartLine"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	var leads []Lead
	seen := map[string]bool{}
	for _, r := range rows {
		file := relPath(root, r.File)
		if r.RuleID == "" || file == "" {
			continue
		}
		fp := fingerprint(Gitleaks, r.RuleID, file, fmt.Sprint(r.StartLine))
		if seen[fp] {
			continue
		}
		seen[fp] = true
		title := strings.TrimSpace(r.Description)
		if title == "" {
			title = r.RuleID
		}
		leads = append(leads, Lead{
			Scanner: Gitleaks, Kind: KindSecret, RuleID: clip(r.RuleID, 80), Title: clip(title, maxLeadField),
			File: file, Line: r.StartLine, Fingerprint: fp,
			Detail: "Possible committed secret matched by rule " + clip(r.RuleID, 80) + "; the value is not stored.",
		})
	}
	return leads, nil
}
