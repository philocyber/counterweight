package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/report"
)

// Automation API for pull request reviews. Reachable with a session or with an
// API token that has the scope (see automationScope). The server never posts
// anything to GitHub: automation reads the results and decides what to do.

func (s *Server) registerPRReviewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/reviews", s.startReview)
	mux.HandleFunc("GET /api/reviews/{id}", s.getReview)
	mux.HandleFunc("GET /api/reviews/{id}/sarif", s.getReviewSARIF)
	mux.HandleFunc("GET /api/reviews/{id}/summary", s.getReviewSummary)
	s.registerAPITokenRoutes(mux)
}

const maxReviewInstructions = 4000

var fullCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

type reviewStartReq struct {
	Repository string `json:"repository"`
	// PullNumber reviews a pull request; Head may then carry the commit the
	// caller expects, used only to answer repeated requests without cloning.
	PullNumber   int    `json:"pull_number"`
	Base         string `json:"base"`
	Head         string `json:"head"`
	Depth        string `json:"depth"`
	Instructions string `json:"instructions"`
}

type severityCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Info     int `json:"info"`
	Total    int `json:"total"`
}

func (c *severityCounts) add(severity string) {
	c.Total++
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		c.Critical++
	case "high":
		c.High++
	case "medium":
		c.Medium++
	case "low":
		c.Low++
	default:
		c.Info++
	}
}

type reviewDTO struct {
	ID            string `json:"id"`
	TaskID        string `json:"task_id"`
	Status        string `json:"status"` // queued, running, waiting, finished, failed
	WaitingReason string `json:"waiting_reason,omitempty"`
	// Existing is set when a start request returned a review that already existed.
	Existing bool   `json:"existing,omitempty"`
	URL      string `json:"url"`

	Repository string `json:"repository,omitempty"`
	PullNumber int    `json:"pull_number,omitempty"`
	BaseCommit string `json:"base_commit,omitempty"`
	HeadCommit string `json:"head_commit,omitempty"`
	Change     *struct {
		Files          int  `json:"files"`
		FilesTruncated bool `json:"files_truncated"`
		DiffTruncated  bool `json:"diff_truncated"`
	} `json:"change,omitempty"`
	// Counts are the open findings recorded so far, split by whether their
	// location lies inside the changed lines (decided by the server).
	Counts *struct {
		InChange      severityCounts `json:"in_change"`
		OutsideChange severityCounts `json:"outside_change"`
	} `json:"counts,omitempty"`
}

// reviewState maps a task's state to the review states automation sees.
func reviewState(dto TaskDTO) (status, reason string) {
	switch dto.Status {
	case "done":
		return "finished", ""
	case "failed":
		return "failed", ""
	case "timeout":
		return "failed", "timeout"
	case "waiting_approval":
		return "waiting", "approval"
	case "paused":
		switch {
		case dto.Budget.PausedForBudget:
			return "waiting", "budget"
		case dto.ApprovalState.Active() != nil:
			return "waiting", "approval"
		}
		return "waiting", "paused"
	case "queued", "created":
		return "queued", ""
	}
	if dto.LLMFailoverState == "chain_exhausted" {
		return "waiting", "model"
	}
	return "running", ""
}

// baseURL is where the console is reached: the configured public address, else
// the address of this request.
func (s *Server) baseURL(r *http.Request) string {
	if base := strings.TrimRight(strings.TrimSpace(notifyPublicBaseURL(s.m.pg)), "/"); base != "" {
		return base
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) reviewBase(r *http.Request, t *Task) reviewDTO {
	dto := taskDTO(t, s.resolvedTaskStatus(t))
	status, reason := reviewState(dto)
	return reviewDTO{
		ID: t.ID, TaskID: t.ID, Status: status, WaitingReason: reason,
		URL: s.baseURL(r) + "/function/tasks/detail/?id=" + t.ID,
	}
}

func (s *Server) startReview(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req reviewStartReq
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, locale.Text(responseLanguage(w), "bad json: ")+locale.ErrorMessage(responseLanguage(w), err))
		return
	}
	repo, err := codesrc.ParseRepo(req.Repository)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if repo.Ref != "" {
		writeErr(w, 400, locale.Text(responseLanguage(w), "A change review names its change; leave the ref out"))
		return
	}
	mode, err := codesrc.ParseMode(req.Depth)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if len([]rune(req.Instructions)) > maxReviewInstructions {
		writeErr(w, 400, locale.Text(responseLanguage(w), "instructions are too long"))
		return
	}
	change := &codesrc.ChangeRequest{PullNumber: req.PullNumber, Base: strings.TrimSpace(req.Base), Head: strings.TrimSpace(req.Head)}
	if req.PullNumber > 0 && change.Head != "" && !fullCommit.MatchString(change.Head) {
		writeErr(w, 400, locale.Text(responseLanguage(w), "With a pull request, head must be a full commit id or empty"))
		return
	}
	if err := change.Validate(); err != nil {
		writeError(w, 400, err)
		return
	}
	// A caller that knows the head commit gets an existing review without a clone. The
	// base is part of the change: a pull request named without one is asked for its own.
	if fullCommit.MatchString(change.Head) && change.PullNumber > 0 && change.Base == "" {
		if info, err := s.githubPullInfo(r.Context(), repo, change.PullNumber); err == nil {
			change.Base = info.Base.Ref
		}
	}
	if fullCommit.MatchString(change.Head) && change.Base != "" {
		changeReviewMu.Lock()
		found := s.existingChangeReview(repo.Slug(), change.Head, sameRequestedChange(change))
		changeReviewMu.Unlock()
		if found != nil {
			dto := s.reviewBase(r, found)
			dto.Existing = true
			writeJSON(w, 200, dto)
			return
		}
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(codeReviewCreateTimeout + time.Minute))
	t, existing, err := s.createCodeReviewChange(r.Context(), repo, mode, change, req.Instructions,
		db.TaskCreateOptions{Language: locale.FromRequest(r)}, true)
	if err != nil {
		writeError(w, 422, err)
		return
	}
	if existing {
		dto := s.reviewBase(r, t)
		dto.Existing = true
		writeJSON(w, 200, dto)
		return
	}
	s.launchTask(t, t.Description+" "+t.Goal, false)
	writeJSON(w, 201, s.reviewBase(r, t))
}

// reviewTask resolves {id} to a change-scoped code review, or writes a 404. A
// whole-repository review is not served here, so a token that may read reviews
// cannot read arbitrary tasks.
func (s *Server) reviewTask(w http.ResponseWriter, r *http.Request) (*Task, *db.TaskCodeTarget, bool) {
	pg := s.pg(w)
	if pg == nil {
		return nil, nil, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil && id > 0 {
		if t, ok := s.m.Task(i64s(id)); ok {
			if target, err := pg.GetTaskCodeTarget(id); err == nil && target != nil && target.Change != nil {
				return t, target, true
			}
		}
	}
	writeErr(w, 404, "Review not found")
	return nil, nil, false
}

// changeClassifier decides, from the stored ranges, whether a finding's location is
// inside the change. Locations are read from the finding's text, including citations of
// any file in the change whatever its name, relative or with the checkout prefix. A
// finding whose location cannot be established counts as inside: a gate must not pass
// on missing information.
func changeClassifier(c *codesrc.ChangeSet, checkout string) func(*db.DBFinding) bool {
	paths := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		paths = append(paths, f.Path)
	}
	return func(f *db.DBFinding) bool {
		locations := report.FindingLocationsIn(f, paths, checkout)
		for _, loc := range locations {
			if c.Contains(loc.File, loc.Start, loc.End) {
				return true
			}
		}
		return len(locations) == 0
	}
}

func (s *Server) reviewFindings(taskID string) ([]*db.DBFinding, error) {
	return s.m.pg.ListFindingsForExport(db.FindingFilter{TaskID: taskID}, nil)
}

func (s *Server) getReview(w http.ResponseWriter, r *http.Request) {
	t, target, ok := s.reviewTask(w, r)
	if !ok {
		return
	}
	findings, err := s.reviewFindings(t.ID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	inChange := changeClassifier(target.Change, target.LocalPath)
	counts := &struct {
		InChange      severityCounts `json:"in_change"`
		OutsideChange severityCounts `json:"outside_change"`
	}{}
	for _, f := range findings {
		if !report.OpenReviewFinding(f) {
			continue
		}
		if inChange(f) {
			counts.InChange.add(f.Severity)
		} else {
			counts.OutsideChange.add(f.Severity)
		}
	}
	dto := s.reviewBase(r, t)
	dto.Repository, dto.PullNumber = target.Repository, target.Change.PullNumber
	dto.BaseCommit, dto.HeadCommit = target.Change.BaseCommit, target.Change.HeadCommit
	dto.Change = &struct {
		Files          int  `json:"files"`
		FilesTruncated bool `json:"files_truncated"`
		DiffTruncated  bool `json:"diff_truncated"`
	}{target.Change.TotalFiles, target.Change.FilesTruncated, target.Change.DiffTruncated}
	dto.Counts = counts
	writeJSON(w, 200, dto)
}

func (s *Server) getReviewSARIF(w http.ResponseWriter, r *http.Request) {
	t, target, ok := s.reviewTask(w, r)
	if !ok {
		return
	}
	only := false
	switch v := r.URL.Query().Get("only_change"); v {
	case "", "false":
	case "true":
		only = true
	default:
		writeErr(w, 400, locale.Text(responseLanguage(w), "only_change must be true or false"))
		return
	}
	findings, err := s.reviewFindings(t.ID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	lifecycle, err := s.findingLifecycles(r.Context(), findings)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	inChange := changeClassifier(target.Change, target.LocalPath)
	in := report.SARIFInput{
		Findings: findings, GeneratedAt: time.Now(), Version: BuildVersion,
		CodeReview: func(*db.DBFinding) *report.CodeReview { return s.taskCodeReview(t) },
		Lifecycle:  lifecycle, InChange: inChange, RepoRelative: true,
	}
	if only {
		in.Keep = inChange
	}
	raw, err := report.SARIF(in)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", report.SARIFMediaType)
	_, _ = w.Write(raw)
}

func (s *Server) getReviewSummary(w http.ResponseWriter, r *http.Request) {
	t, target, ok := s.reviewTask(w, r)
	if !ok {
		return
	}
	findings, err := s.reviewFindings(t.ID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	lifecycle, err := s.findingLifecycles(r.Context(), findings)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	dto := s.reviewBase(r, t)
	base := s.baseURL(r)
	md := report.ReviewSummaryMarkdown(report.ReviewSummaryInput{
		Repository: target.Repository, PullNumber: target.Change.PullNumber, HeadCommit: target.Change.HeadCommit,
		Status: dto.Status, WaitingReason: dto.WaitingReason, TaskURL: dto.URL,
		FindingURL: func(id int64) string { return base + "/function/findings/detail?id=" + strconv.FormatInt(id, 10) },
		Findings:   findings, InChange: changeClassifier(target.Change, target.LocalPath), Lifecycle: lifecycle,
		ChangedFiles: target.Change.TotalFiles, FilesTruncated: target.Change.FilesTruncated, DiffTruncated: target.Change.DiffTruncated,
	})
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(md))
}
