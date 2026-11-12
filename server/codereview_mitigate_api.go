package server

import (
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// isFindingRunAgent lists the agents whose conversations are sealed finding runs.
func isFindingRunAgent(key string) bool {
	switch key {
	case db.FindingRetestAgentKey, db.CodeVerifierAgentKey, db.CodeFixerAgentKey, db.PatchReviewerAgentKey:
		return true
	}
	return false
}

// findingRunTools names the tools through which a finding-run agent reads its
// context and records its result.
func findingRunTools(key string) []string {
	switch key {
	case db.FindingRetestAgentKey:
		return []string{"get_finding_retest_context", "record_finding_retest_result"}
	case db.CodeVerifierAgentKey:
		return codeVerifierTools
	case db.CodeFixerAgentKey:
		return codeFixerTools
	case db.PatchReviewerAgentKey:
		return patchReviewerTools
	}
	return nil
}

func (s *Server) registerMitigationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tasks/{id}/mitigations", s.listTaskMitigations)
	mux.HandleFunc("POST /api/exploration/findings/{id}/mitigations", s.startMitigationHandler)
	mux.HandleFunc("POST /api/exploration/findings/{id}/verify", s.verifyFindingHandler)
	mux.HandleFunc("GET /api/tasks/{id}/automation", s.getTaskAutomationHandler)
	mux.HandleFunc("PUT /api/tasks/{id}/automation", s.putTaskAutomationHandler)
	mux.HandleFunc("POST /api/mitigations/{id}/publish", s.publishMitigationHandler)
	mux.HandleFunc("POST /api/mitigations/{id}/refresh", s.refreshMitigationHandler)
	mux.HandleFunc("POST /api/mitigations/{id}/recheck", s.recheckMitigationHandler)
	mux.HandleFunc("GET /api/github/token", s.getGitHubTokenHandler)
	mux.HandleFunc("PUT /api/github/token", s.putGitHubTokenHandler)
	mux.HandleFunc("DELETE /api/github/token", s.deleteGitHubTokenHandler)
}

type taskMitigationFinding struct {
	ID           int64                   `json:"id,string"`
	Name         string                  `json:"name"`
	VulnClass    string                  `json:"vulnclass"`
	Severity     string                  `json:"severity"`
	Status       string                  `json:"status"`
	Verification string                  `json:"verification"` // latest completed verify verdict, "" if none
	Mitigations  []*db.FindingMitigation `json:"mitigations"`
}

// listTaskMitigations feeds the Mitigation tab: the task's findings with their
// verification verdict and fix attempts, plus the GitHub token status.
func (s *Server) listTaskMitigations(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, locale.Text(responseLanguage(w), "bad task id"))
		return
	}
	out := map[string]any{"code_review": false, "github": s.githubStatusOrEmpty(), "findings": []taskMitigationFinding{}}
	target, err := pg.GetTaskCodeTarget(id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if target == nil {
		writeJSON(w, 200, out)
		return
	}
	out["code_review"] = true
	out["repository"] = target.Repository
	findings, _, err := pg.ListFindingsPage(db.FindingFilter{TaskID: strconv.FormatInt(id, 10), Sort: "severity"}, 1, 500)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	items := []taskMitigationFinding{}
	for _, f := range findings {
		item := taskMitigationFinding{ID: f.ID, Name: f.Name, VulnClass: f.VulnClass, Severity: f.Severity, Status: f.Status}
		runs, err := pg.ListFindingRetests(f.ID)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		for _, run := range runs {
			if run.Kind == db.RetestKindVerify && run.Status == "completed" {
				item.Verification = run.Verdict
				break
			}
		}
		if item.Mitigations, err = pg.ListFindingMitigations(r.Context(), f.ID); err != nil {
			writeError(w, 500, err)
			return
		}
		items = append(items, item)
	}
	out["findings"] = items
	writeJSON(w, 200, out)
}

func (s *Server) startMitigationHandler(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, locale.Text(responseLanguage(w), "bad finding id"))
		return
	}
	// unverified_ok: the operator saw the warning that an unverified finding may be
	// a false positive and chose to fix it anyway.
	var req struct {
		UnverifiedOK bool `json:"unverified_ok"`
	}
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			writeError(w, 400, err)
			return
		}
	}
	ctx := locale.WithLang(r.Context(), responseLanguage(w))
	run, created, err := s.startFindingMitigation(ctx, id, req.UnverifiedOK)
	if err != nil {
		writeErr(w, 409, locale.ErrorMessage(responseLanguage(w), err))
		return
	}
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"run": run, "created": created})
}

// verifyFindingHandler starts an independent verification the operator asked for,
// for a finding of a code review task (a pentest finding has no source to re-read).
func (s *Server) verifyFindingHandler(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, locale.Text(responseLanguage(w), "bad finding id"))
		return
	}
	f, err := pg.GetFinding(id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if f == nil {
		writeErr(w, 404, locale.Text(responseLanguage(w), "finding not found"))
		return
	}
	if _, err := s.findingCodeReviewTarget(f); err != nil {
		writeErr(w, 409, locale.ErrorMessage(responseLanguage(w), err))
		return
	}
	run, created, err := s.startFindingVerification(locale.WithLang(r.Context(), responseLanguage(w)), id, true)
	if err != nil {
		writeErr(w, 409, locale.ErrorMessage(responseLanguage(w), err))
		return
	}
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"retest": run, "created": created})
}

func (s *Server) getTaskAutomationHandler(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, locale.Text(responseLanguage(w), "bad task id"))
		return
	}
	s.writeTaskAutomation(w, pg, id)
}

// writeTaskAutomation answers with the switches and whether the task is a code
// review, the only kind of task they apply to.
func (s *Server) writeTaskAutomation(w http.ResponseWriter, pg *db.DB, taskID int64) {
	a, err := pg.GetTaskAutomation(taskID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	target, err := pg.GetTaskCodeTarget(taskID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"verify": a.Verify, "mitigate": a.Mitigate, "code_review": target != nil})
}

// putTaskAutomationHandler changes the switches; fields left out keep their value,
// so the Retest and Mitigation tabs each send only their own.
func (s *Server) putTaskAutomationHandler(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, locale.Text(responseLanguage(w), "bad task id"))
		return
	}
	var req struct {
		Verify   *bool `json:"verify"`
		Mitigate *bool `json:"mitigate"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	a, err := pg.GetTaskAutomation(id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if req.Verify != nil {
		a.Verify = *req.Verify
	}
	if req.Mitigate != nil {
		a.Mitigate = *req.Mitigate
	}
	if err := pg.SetTaskAutomation(id, a); err != nil {
		writeError(w, 500, err)
		return
	}
	s.writeTaskAutomation(w, pg, id)
}

func (s *Server) publishMitigationHandler(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, locale.Text(responseLanguage(w), "bad fix id"))
		return
	}
	url, err := s.publishMitigation(locale.WithLang(r.Context(), responseLanguage(w)), id)
	if err != nil {
		writeErr(w, 409, locale.ErrorMessage(responseLanguage(w), err))
		return
	}
	writeJSON(w, 200, map[string]any{"pr_url": url})
}

func (s *Server) refreshMitigationHandler(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, locale.Text(responseLanguage(w), "bad fix id"))
		return
	}
	m, err := s.refreshMitigationPR(r.Context(), id)
	if err != nil {
		writeErr(w, 502, locale.ErrorMessage(responseLanguage(w), err))
		return
	}
	writeJSON(w, 200, map[string]any{"mitigation": m})
}

// recheckMitigationHandler re-verifies a finding on the code its merged fix produced.
func (s *Server) recheckMitigationHandler(w http.ResponseWriter, r *http.Request) {
	if s.pg(w) == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, locale.Text(responseLanguage(w), "bad fix id"))
		return
	}
	run, created, err := s.startMitigationRecheck(locale.WithLang(r.Context(), responseLanguage(w)), id)
	if err != nil {
		writeErr(w, 409, locale.ErrorMessage(responseLanguage(w), err))
		return
	}
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"run": run, "created": created})
}

func (s *Server) githubStatusOrEmpty() githubStatus {
	c, err := s.loadGitHubCredential()
	if err != nil {
		return githubStatus{}
	}
	return c.status()
}

func (s *Server) getGitHubTokenHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.githubStatusOrEmpty())
}

func (s *Server) putGitHubTokenHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	st, err := s.saveGitHubToken(r.Context(), req.Token)
	if err != nil {
		writeErr(w, 400, locale.ErrorMessage(responseLanguage(w), err))
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) deleteGitHubTokenHandler(w http.ResponseWriter, _ *http.Request) {
	if err := removeIfExists(s.githubCredentialPath()); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, githubStatus{})
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
