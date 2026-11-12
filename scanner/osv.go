package scanner

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// osvSpec runs OSV-Scanner over the clone's lockfiles. Exit status 1 means
// advisories were found and 128 means no package files exist; both are normal.
func osvSpec() Spec {
	return Spec{
		Key:         OSV,
		Kind:        KindAdvisory,
		Purpose:     "Dependency advisories from lockfiles",
		ReleasesURL: "https://github.com/google/osv-scanner/releases",
		Timeout:     10 * time.Minute,
		VersionArgs: []string{"--version"},
		// An empty configuration file keeps a repository's own osv-scanner.toml,
		// which can silence advisories, from applying.
		ConfigFiles: map[string]string{"osv-scanner.toml": ""},
		Args: func(root, workDir string) []string {
			return []string{"--format", "json", "--config", filepath.Join(workDir, "osv-scanner.toml"), "--recursive", root}
		},
		Parse:     ParseOSV,
		OKExit:    []int{1},
		EmptyExit: []int{128},
	}
}

type osvOutput struct {
	Results []struct {
		Source struct {
			Path string `json:"path"`
		} `json:"source"`
		Packages []struct {
			Package struct {
				Name      string `json:"name"`
				Version   string `json:"version"`
				Ecosystem string `json:"ecosystem"`
			} `json:"package"`
			Vulnerabilities []osvVuln `json:"vulnerabilities"`
			Groups          []struct {
				IDs         []string `json:"ids"`
				Aliases     []string `json:"aliases"`
				MaxSeverity string   `json:"max_severity"`
			} `json:"groups"`
		} `json:"packages"`
	} `json:"results"`
}

type osvVuln struct {
	ID       string   `json:"id"`
	Aliases  []string `json:"aliases"`
	Summary  string   `json:"summary"`
	Affected []struct {
		Package struct {
			Name      string `json:"name"`
			Ecosystem string `json:"ecosystem"`
		} `json:"package"`
		Ranges []struct {
			Events []struct {
				Fixed string `json:"fixed"`
			} `json:"events"`
		} `json:"ranges"`
	} `json:"affected"`
}

// ParseOSV reads OSV-Scanner's JSON report. Advisories that are aliases of one
// another (a GHSA and its CVE) for the same package become a single lead.
func ParseOSV(root string, out []byte) ([]Lead, error) {
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}
	var doc osvOutput
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, err
	}
	var leads []Lead
	seen := map[string]bool{}
	for _, res := range doc.Results {
		file := relPath(root, res.Source.Path)
		for _, pkg := range res.Packages {
			byID := map[string]osvVuln{}
			for _, v := range pkg.Vulnerabilities {
				byID[v.ID] = v
			}
			handled := map[string]bool{}
			emit := func(rep string, group []string, extraAliases []string, severity float64) {
				v, ok := byID[rep]
				if !ok || rep == "" {
					return
				}
				var vulns []osvVuln
				aliasSet := map[string]bool{}
				for _, id := range group {
					handled[id] = true
					if g, ok := byID[id]; ok {
						vulns = append(vulns, g)
						for _, a := range g.Aliases {
							aliasSet[a] = true
						}
					}
					if id != rep {
						aliasSet[id] = true
					}
				}
				for _, a := range extraAliases {
					aliasSet[a] = true
				}
				delete(aliasSet, rep)
				aliases := make([]string, 0, len(aliasSet))
				for a := range aliasSet {
					aliases = append(aliases, clip(a, 60))
				}
				sort.Strings(aliases)
				fixed := fixedVersions(vulns, pkg.Package.Name, pkg.Package.Version)
				fp := fingerprint(OSV, rep, pkg.Package.Ecosystem, pkg.Package.Name, pkg.Package.Version, file)
				if seen[fp] {
					return
				}
				seen[fp] = true
				title := strings.TrimSpace(v.Summary)
				if title == "" {
					title = rep
				}
				leads = append(leads, Lead{
					Scanner: OSV, Kind: KindAdvisory, RuleID: clip(rep, 60), Title: clip(title, maxLeadField),
					File: file, Package: clip(pkg.Package.Name, 120), Version: clip(pkg.Package.Version, 60),
					FixedVersion: clip(fixed, 120), Aliases: aliases, Fingerprint: fp, Severity: severity,
					Detail: clip("Dependency "+pkg.Package.Name+" "+pkg.Package.Version+" ("+pkg.Package.Ecosystem+") is listed as affected by "+rep+".", maxLeadField),
				})
			}
			for _, g := range pkg.Groups {
				rep := pickAdvisory(g.IDs, byID)
				sev, _ := strconv.ParseFloat(strings.TrimSpace(g.MaxSeverity), 64)
				emit(rep, g.IDs, g.Aliases, sev)
			}
			for _, v := range pkg.Vulnerabilities {
				if !handled[v.ID] {
					emit(v.ID, []string{v.ID}, nil, 0)
				}
			}
		}
	}
	return leads, nil
}

// pickAdvisory chooses the representative id of an alias group: a GitHub
// advisory, else a CVE, else the first id the report carries details for.
func pickAdvisory(ids []string, byID map[string]osvVuln) string {
	var first, cve string
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			continue
		}
		if strings.HasPrefix(id, "GHSA-") {
			return id
		}
		if cve == "" && strings.HasPrefix(id, "CVE-") {
			cve = id
		}
		if first == "" {
			first = id
		}
	}
	if cve != "" {
		return cve
	}
	return first
}

// fixedVersions reduces the fixed values the advisories name for pkg to
// release versions (see PickFixed).
func fixedVersions(vulns []osvVuln, pkg, installed string) string {
	var all []string
	for _, v := range vulns {
		for _, a := range v.Affected {
			if !strings.EqualFold(a.Package.Name, pkg) {
				continue
			}
			for _, r := range a.Ranges {
				for _, e := range r.Events {
					if e.Fixed != "" {
						all = append(all, e.Fixed)
					}
				}
			}
		}
	}
	return PickFixed(installed, all)
}
