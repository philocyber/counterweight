package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/scanner"
)

// registerScanRoutes mounts the supplementary-scanner API and gives the review
// agents their (conditional) resolve tool.
func (s *Server) registerScanRoutes(mux *http.ServeMux) {
	agent.ReviewExtraTools = s.reviewExtraTools
	mux.HandleFunc("GET /api/tasks/{id}/scan", s.getTaskScan)
	mux.HandleFunc("GET /api/scan/settings", s.getScanSettings)
	mux.HandleFunc("PUT /api/scan/settings", s.putScanSettings)
}

// scanHistoryNote states what the secret scanner does and does not cover.
const scanHistoryNote = "Secrets are searched in the working tree only; git history is not scanned (a shallow clone has none)."

// getTaskScan returns a task's scanner runs and leads with their states, and
// the availability of each scanner. Leads are unverified pointers, not findings.
func (s *Server) getTaskScan(w http.ResponseWriter, r *http.Request) {
	t := s.m.ResolveTask(r.PathValue("id"))
	if t == nil {
		writeErr(w, 404, "task not found")
		return
	}
	lang := responseLanguage(w)
	out := map[string]any{
		"applicable": taskAssessmentType(t) == db.AssessmentCodeReview,
		"enabled":    s.scannersEnabled(),
		"note":       locale.Text(lang, scanHistoryNote),
		"scanners":   s.scannerStatuses(lang),
		"runs":       []*db.ScanRun{},
		"leads":      []*db.ScanLead{},
		"counts":     map[string]int{db.LeadNew: 0, db.LeadDelivered: 0, db.LeadConfirmed: 0, db.LeadDismissed: 0},
	}
	id, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	reconcileScanLeads(s.m.pg, id, listTaskFindings(s.m.pg, id))
	runs, err := s.m.pg.ListScanRuns(id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	leads, err := s.m.pg.ListScanLeads(id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	counts := out["counts"].(map[string]int)
	for _, l := range leads {
		counts[l.State]++
	}
	out["runs"], out["leads"] = runs, leads
	writeJSON(w, 200, out)
}

func (s *Server) scanSettingsPayload(lang locale.Lang) map[string]any {
	paths := map[string]string{}
	for _, spec := range scanner.Specs() {
		v, _, _ := s.m.pg.GetSetting(fmt.Sprintf(settingScannerPathFmt, spec.Key))
		paths[spec.Key] = strings.TrimSpace(v)
	}
	return map[string]any{
		"enabled":  s.scannersEnabled(),
		"note":     locale.Text(lang, scanHistoryNote),
		"paths":    paths,
		"scanners": s.scannerStatuses(lang),
	}
}

func (s *Server) getScanSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.scanSettingsPayload(responseLanguage(w)))
}

// putScanSettings turns supplementary scanners on or off and sets explicit
// binary paths. A null field is left unchanged; an empty path clears it.
func (s *Server) putScanSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool              `json:"enabled"`
		Paths   map[string]*string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	lang := responseLanguage(w)
	for key, p := range req.Paths {
		if _, ok := scanner.Find(key); !ok {
			writeErr(w, 400, locale.Text(lang, "Unknown scanner: %s", key))
			return
		}
		if p == nil {
			continue
		}
		path := strings.TrimSpace(*p)
		if path != "" {
			info, err := os.Stat(path)
			if !filepath.IsAbs(path) || err != nil || info.IsDir() {
				writeErr(w, 400, locale.Text(lang, "The path for %s must be an absolute path to an existing executable file", key))
				return
			}
		}
	}
	if req.Enabled != nil {
		if err := s.m.pg.SetBool(settingScannersEnabled, *req.Enabled); err != nil {
			writeError(w, 500, err)
			return
		}
	}
	for key, p := range req.Paths {
		if p == nil {
			continue
		}
		if err := s.m.pg.SetSetting(fmt.Sprintf(settingScannerPathFmt, key), strings.TrimSpace(*p)); err != nil {
			writeError(w, 500, err)
			return
		}
	}
	writeJSON(w, 200, s.scanSettingsPayload(lang))
}
