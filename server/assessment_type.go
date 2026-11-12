package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

const codeReviewCreateTimeout = 10 * time.Minute

func taskAssessmentType(t *Task) string {
	kind, _ := db.NormalizeAssessmentType(t.lifecycleSnapshot().AssessmentType)
	return kind
}

func reviewInput(repository, ref, mode string) (codesrc.Repo, codesrc.Mode, error) {
	repo, err := codesrc.ParseRepo(repository)
	if err != nil {
		return repo, "", err
	}
	if ref != "" {
		if repo.Ref != "" && repo.Ref != ref {
			return repo, "", locale.NewError("Conflicting repository refs")
		}
		repo, err = codesrc.ParseRepo(repo.Slug() + "#" + ref)
		if err != nil {
			return repo, "", err
		}
	}
	depth, err := codesrc.ParseMode(mode)
	return repo, depth, err
}

// createCodeReview clones before the atomic DB write. Callers admit the task
// only after both its row and repository target have committed.
func (s *Server) createCodeReview(ctx context.Context, repo codesrc.Repo, mode codesrc.Mode, instructions string, opts db.TaskCreateOptions) (*Task, error) {
	ctx, cancel := context.WithTimeout(ctx, codeReviewCreateTimeout)
	defer cancel()
	path, commit, err := s.cloneForReview(ctx, repo, mode)
	if err != nil {
		return nil, locale.Errorf("Could not clone %s: %w", repo.Slug(), err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.createCodeReviewFromClone(repo, mode, path, commit, instructions, opts)
}

func (s *Server) createCodeReviewFromClone(repo codesrc.Repo, mode codesrc.Mode, path, commit, instructions string, opts db.TaskCreateOptions) (*Task, error) {
	desc := locale.Text(opts.Language, "Code review: ") + repo.Slug()
	goal := composeCodeReviewGoal(repo, mode, path, opts.Language)
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		goal += locale.Text(opts.Language, " Additional operator instructions: ") + instructions
	}
	opts.AssessmentType = db.AssessmentCodeReview
	opts.CodeTarget = &db.TaskCodeTarget{Repository: repo.Slug(), URL: "https://github.com/" + repo.Slug(), Ref: repo.Ref, CommitSHA: commit, Mode: string(mode), LocalPath: path}
	opts.OperatorRequest = desc + "\n" + goal
	t, err := s.m.CreateTaskWithOptions(desc, goal, opts)
	if err != nil {
		return nil, locale.Errorf("Cloned, but could not create the review task: %w", err)
	}
	if id, err := strconv.ParseInt(t.ID, 10, 64); err == nil {
		s.startScanners(id, path)
	}
	return t, nil
}

func (s *Server) createCodeReviewHTTP(w http.ResponseWriter, r *http.Request, req createTaskReq) {
	repo, mode, err := reviewInput(req.Repository, req.Ref, req.Mode)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if req.Change != nil {
		if err := req.Change.Validate(); err != nil {
			writeError(w, 400, err)
			return
		}
	}
	if len(req.InterceptRules) > 0 || len(req.CompanyIDs) > 0 || len(req.SourceTaskIDs) > 0 || (req.SeedFirstIntent != nil && *req.SeedFirstIntent) {
		writeErr(w, 400, locale.Text(responseLanguage(w), "Code reviews do not accept network scope, inherited tasks, interception rules or seed_first_intent"))
		return
	}
	if len(req.AuthzAccounts) > 0 {
		writeErr(w, 400, locale.Text(responseLanguage(w), "An authorization check tests a running application; a code review task has none"))
		return
	}
	if len(req.LLMProfileIDs) == 0 && req.LLMProfileID != nil {
		req.LLMProfileIDs = []int64{*req.LLMProfileID}
	}
	if err := s.validateTaskProfileIDs(req.LLMProfileIDs); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := validateBudgetInput(req.TokenBudget, req.CostBudget, &req.CostCurrency); err != nil {
		writeError(w, 400, err)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(codeReviewCreateTimeout + time.Minute))
	t, _, err := s.createCodeReviewChange(r.Context(), repo, mode, req.Change, req.Instructions, db.TaskCreateOptions{
		Language: locale.FromRequest(r), Name: strings.TrimSpace(req.Name), CategoryID: req.CategoryID,
		LLMProfileIDs: req.LLMProfileIDs, TimeoutSeconds: req.TimeoutSeconds, PlanHeartbeatSeconds: req.PlanHeartbeatSeconds,
		TokenBudget: req.TokenBudget, CostBudget: req.CostBudget, CostCurrency: req.CostCurrency,
	}, false)
	if err != nil {
		if errors.Is(err, db.ErrTaskCategoryInvalid) || errors.Is(err, db.ErrTaskCategoryNotFound) {
			writeError(w, 400, err)
		} else {
			writeError(w, 422, err)
		}
		return
	}
	s.launchTask(t, t.Description+" "+t.Goal, false)
	writeJSON(w, 201, taskDTO(t, s.resolvedTaskStatus(t)))
}

func (s *Server) registerAssessmentTypeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /api/tasks/{id}/assessment-type", s.changeAssessmentType)
}

func (s *Server) changeAssessmentType(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AssessmentType string `json:"assessment_type"`
		Reclassify     bool   `json:"reclassify"`
		Repository     string `json:"repository"`
		Ref            string `json:"ref"`
		Mode           string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, err)
		return
	}
	if req.AssessmentType == "" {
		writeError(w, 400, db.ErrAssessmentTypeInvalid)
		return
	}
	kind, err := db.NormalizeAssessmentType(req.AssessmentType)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	id, _ := strconv.ParseInt(t.ID, 10, 64)
	// Validate before cloning, then serialize the final update with admission.
	started, err := s.m.pg.TaskStarted(id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	started = started || s.engine.Started(t.ID)
	if kind != taskAssessmentType(t) {
		if !req.Reclassify && started {
			writeError(w, 409, db.ErrTaskAlreadyStarted)
			return
		}
		if req.Reclassify && (!started || taskAssessmentType(t) != db.AssessmentGeneral) {
			if !started {
				writeError(w, 409, db.ErrReclassifyNotStarted)
			} else {
				writeError(w, 409, db.ErrReclassifyNotGeneral)
			}
			return
		}
	}
	if req.Reclassify && !isTerminalStatus(t.lifecycleSnapshot().Status) {
		writeErr(w, 409, locale.Text(responseLanguage(w), "Finish the task before reclassifying historical records"))
		return
	}
	if (req.Repository != "" || req.Ref != "" || req.Mode != "") && (kind != db.AssessmentCodeReview || kind == taskAssessmentType(t)) {
		writeErr(w, 400, locale.Text(responseLanguage(w), "Repository fields are accepted only when changing to code_review"))
		return
	}
	var target *db.TaskCodeTarget
	if kind == db.AssessmentCodeReview && kind != taskAssessmentType(t) {
		repo, mode, err := reviewInput(req.Repository, req.Ref, req.Mode)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		target = &db.TaskCodeTarget{Repository: repo.Slug(), URL: "https://github.com/" + repo.Slug(), Ref: repo.Ref, Mode: string(mode)}
		if !req.Reclassify {
			ctx, cancel := context.WithTimeout(r.Context(), codeReviewCreateTimeout)
			defer cancel()
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(codeReviewCreateTimeout + time.Minute))
			target.LocalPath, target.CommitSHA, err = s.cloneForReview(ctx, repo, mode)
			if err != nil {
				writeError(w, 422, err)
				return
			}
		}
	}
	s.concMu.Lock()
	defer s.concMu.Unlock()
	if current, exists := s.m.Task(t.ID); !exists || current != t || s.engine.IsDeleting(t.ID) {
		writeErr(w, 404, "task not found")
		return
	}
	started = started || s.engine.Started(t.ID)
	updated, err := s.m.pg.ChangeTaskAssessmentType(id, db.AssessmentTypeChange{Type: kind, Reclassify: req.Reclassify, Target: target, EngineStarted: started})
	if err != nil {
		writeError(w, 409, err)
		return
	}
	if updated == nil {
		writeErr(w, 404, "task not found")
		return
	}
	t.updateLifecycle(func(state *taskLifecycleState) {
		state.AssessmentType, state.AssessmentTypeExplicit, state.CodeTarget, state.CoverageEnabled = updated.AssessmentType, updated.AssessmentTypeExplicit, updated.CodeTarget, updated.CoverageEnabled
	})
	writeJSON(w, 200, taskDTO(t, s.resolvedTaskStatus(t)))
}
