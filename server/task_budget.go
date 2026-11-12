package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/llmrec"
	"github.com/Philocyber/counterweight/locale"
)

// Per-task usage budget enforcement.
//
// Every model call a task makes (planner, workers, main agent, goals,
// compaction) goes through taskLLMRuntime, which calls budgetAcquire first. The
// gate compares the task's recorded usage (db.TaskBudgetView) with its budgets:
//
//   - budget reached: the task is paused with a persisted "budget" hold whose
//     reason carries the figures, calls already in flight are allowed to finish,
//     and only then is the task's execution context cancelled, so no intent is
//     lost and the interrupted work returns to the frontier like any pause;
//   - not reached but other calls are in flight and the recorded usage plus one
//     estimate per in-flight call (the largest call seen so far, at least
//     db.MinCallEstimate tokens) would pass the limit: the call waits for an
//     in-flight call to finish and is then evaluated again;
//   - otherwise the call proceeds.
//
// Worst-case overshoot: only the call that crosses the line can exceed the
// budget when calls run one at a time near the limit, so a task overshoots by at
// most the size of one call. A further call is admitted next to others only if
// the estimates fit, so the overshoot can grow beyond one call only by the
// amount calls exceed the largest call seen before.

// ErrTaskBudgetReached marks a refusal because a task's budget is used up.
var ErrTaskBudgetReached = locale.NewError("Task budget reached")

const (
	// budgetDrainMax bounds how long a budget pause waits for in-flight model
	// calls before cancelling the task's execution anyway.
	budgetDrainMax = 90 * time.Second
	// budgetRecheck is how often a waiting call re-reads the budget.
	budgetRecheck = 2 * time.Second
)

// budgetGate tracks the model calls of one task that are currently running.
type budgetGate struct {
	mu       sync.Mutex
	inflight int
	changed  chan struct{} // closed and replaced whenever a call finishes
	drained  chan struct{} // closed when a budget pause has drained; nil without one
}

func (g *budgetGate) release() {
	g.mu.Lock()
	g.inflight--
	close(g.changed)
	g.changed = make(chan struct{})
	g.mu.Unlock()
}

func (s *Server) budgetGateFor(taskID string) *budgetGate {
	if v, ok := s.budgetGates.Load(taskID); ok {
		return v.(*budgetGate)
	}
	v, _ := s.budgetGates.LoadOrStore(taskID, &budgetGate{changed: make(chan struct{})})
	return v.(*budgetGate)
}

// budgetAcquire admits one model call of a task. The returned release must be
// called when the call has finished and its usage has been recorded.
func (s *Server) budgetAcquire(ctx context.Context, taskID string) (func(), error) {
	if s == nil || s.m == nil || s.m.pg == nil {
		return func() {}, nil
	}
	id, err := parseTaskID(taskID)
	if err != nil {
		return func() {}, nil
	}
	g := s.budgetGateFor(taskID)
	for {
		g.mu.Lock()
		// Read under the gate lock so two callers cannot both act on one stale reading.
		view, err := s.m.pg.TaskBudgetView(id)
		if err != nil || view == nil || !view.Limited() {
			if err != nil {
				log.Printf("[budget] task %s usage lookup failed, call allowed: %v", taskID, err)
			}
			g.inflight++
			g.mu.Unlock()
			return g.release, nil
		}
		if kind := view.ReachedKind(); kind != "" {
			g.mu.Unlock()
			return nil, s.budgetStop(ctx, taskID, id, view, kind)
		}
		if g.inflight > 0 && !view.Fits(g.inflight) {
			wait := g.changed
			g.mu.Unlock()
			select {
			case <-wait:
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			case <-time.After(budgetRecheck):
			}
			continue
		}
		g.inflight++
		g.mu.Unlock()
		s.warnBudget(taskID, id, view)
		return g.release, nil
	}
}

// budgetFigures renders the used/limit figures of one kind of budget.
func budgetFigures(lang locale.Lang, v *db.TaskBudgetView, kind string) string {
	if kind == "cost" && v.CostBudget != nil {
		return locale.Text(lang, "%.4f of %.4f %s estimated", v.Cost, *v.CostBudget, v.CostCurrency)
	}
	return locale.Text(lang, "%d of %d tokens used", v.Tokens, v.TokenBudget)
}

// budgetKindForWarning picks the budget closer to its limit.
func budgetKindForWarning(v *db.TaskBudgetView) string {
	if v.CostBudget != nil && (v.TokenBudget == 0 || v.Cost / *v.CostBudget > float64(v.Tokens)/float64(v.TokenBudget)) {
		return "cost"
	}
	return "tokens"
}

func budgetMetadata(v *db.TaskBudgetView, kind string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"budget": map[string]any{
		"kind": kind, "token_budget": v.TokenBudget, "tokens_used": v.Tokens,
		"cost_budget": v.CostBudget, "estimated_cost": v.Cost, "currency": v.CostCurrency,
	}})
	return raw
}

// warnBudget emits the single 80 percent warning activity of a budget.
func (s *Server) warnBudget(taskID string, id int64, v *db.TaskBudgetView) {
	if v.WarnedAt != nil || v.State() != "warning" {
		return
	}
	if first, err := s.m.pg.MarkBudgetWarned(id); err != nil || !first {
		return
	}
	t, ok := s.m.Task(taskID)
	if !ok {
		return
	}
	lang := s.taskOutputLanguage(taskID)
	kind := budgetKindForWarning(v)
	s.engine.emitActivity(t, db.Activity{Worker: "system", Kind: "text",
		Summary:  locale.Text(lang, "Budget warning: %s (%d%% of the budget used)", budgetFigures(lang, v, kind), db.BudgetWarnPercent),
		Metadata: budgetMetadata(v, kind)})
}

func budgetReachedCause(lang locale.Lang, figures string) *agent.AbortCause {
	return agent.Causef("budget_reached", locale.Text(lang, "Task budget reached"),
		locale.Text(lang, "The task budget was reached (%s). The task is paused; raise the budget and resume to continue."), figures)
}

// budgetStop handles a call that found the budget used up: it pauses the task,
// then holds the call until in-flight calls have drained and the task's
// execution has been cancelled, so the interrupted work is returned to the
// frontier. Finished tasks cannot be paused and are refused straight away.
func (s *Server) budgetStop(ctx context.Context, taskID string, id int64, v *db.TaskBudgetView, kind string) error {
	lang := s.taskOutputLanguage(taskID)
	cause := budgetReachedCause(lang, budgetFigures(lang, v, kind))
	t, ok := s.m.Task(taskID)
	if !ok || isTerminalStatus(t.lifecycleSnapshot().Status) {
		return cause
	}
	s.pauseForBudget(t, id, v, kind)
	g := s.budgetGateFor(taskID)
	g.mu.Lock()
	drained := g.drained
	g.mu.Unlock()
	if drained == nil {
		return cause
	}
	select {
	case <-drained:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	return cause
}

// pauseForBudget persists the budget pause (once) and starts the drain.
func (s *Server) pauseForBudget(t *Task, id int64, v *db.TaskBudgetView, kind string) {
	lang := s.taskOutputLanguage(t.ID)
	figures := budgetFigures(lang, v, kind)
	s.concMu.Lock()
	defer s.concMu.Unlock()
	if s.engine.IsDeleting(t.ID) || !s.engine.beginTaskOperation(t.ID) {
		return
	}
	defer s.engine.decInflight(t.ID)
	s.m.taskStateMu.Lock()
	inserted, err := s.m.pg.BeginBudgetHold(id, locale.Text(lang, "Budget reached: %s", figures))
	if err == nil {
		// Publish the persistent pause before anything can classify an interrupted worker.
		t.updateLifecycle(func(state *taskLifecycleState) { state.Paused = true; state.Queued = false; state.QueuedAt = 0 })
		s.engine.paused.Store(t.ID, true)
	}
	s.m.taskStateMu.Unlock()
	if err != nil {
		log.Printf("[budget] task %s could not be paused: %v", t.ID, err)
		return
	}
	if !inserted {
		return
	}
	g := s.budgetGateFor(t.ID)
	drained := make(chan struct{})
	g.mu.Lock()
	g.drained = drained
	g.mu.Unlock()
	s.engine.emitActivity(t, db.Activity{Worker: "system", Kind: "text", IsError: true,
		Summary:  locale.Text(lang, "Budget reached: %s. The task is paused. Raise the budget and resume to continue.", figures),
		Metadata: budgetMetadata(v, kind)})
	log.Printf("[budget] task %s paused: %s", t.ID, figures)
	go s.drainBudgetPause(t, g, drained, budgetReachedCause(lang, figures))
	go s.reconcileConcurrency()
}

// drainBudgetPause lets in-flight model calls finish and then cancels the
// task's execution so interrupted intents return to the frontier.
func (s *Server) drainBudgetPause(t *Task, g *budgetGate, drained chan struct{}, cause error) {
	timer := time.NewTimer(budgetDrainMax)
	defer timer.Stop()
wait:
	for {
		g.mu.Lock()
		n, changed := g.inflight, g.changed
		g.mu.Unlock()
		if n == 0 {
			break
		}
		select {
		case <-changed:
		case <-timer.C:
			break wait
		case <-s.ctx.Done():
			close(drained)
			return
		}
	}
	s.engine.cancelExec(t.ID, cause)
	s.cancelTaskChat(t.ID, cause)
	close(drained)
}

// taskBudgetAdmission refuses to start or resume work for a task whose budget
// is used up. Raising the budget is how the operator lets it continue.
func (s *Server) taskBudgetAdmission(taskID int64) error {
	if s.m == nil || s.m.pg == nil {
		return nil
	}
	v, err := s.m.pg.TaskBudgetView(taskID)
	if err != nil || v == nil {
		return err
	}
	kind := v.ReachedKind()
	if kind == "" {
		return nil
	}
	lang := locale.ServerDefault()
	return locale.Errorf("%w (%s). Raise the task budget to continue", ErrTaskBudgetReached, budgetFigures(lang, v, kind))
}

// findingBudgetCheck applies taskBudgetAdmission to the task that owns a finding,
// for explicit actions (retest, verification, fix generation) on that finding.
func (s *Server) findingBudgetCheck(findingID int64) error {
	f, err := s.m.pg.GetFinding(findingID)
	if err != nil || f == nil || f.TaskID == nil {
		return nil
	}
	return s.taskBudgetAdmission(*f.TaskID)
}

// attributeFindingRunUsage counts a finding run's model calls against the task
// that owns the finding.
func (s *Server) attributeFindingRunUsage(ctx context.Context, findingID int64) context.Context {
	if f, err := s.m.pg.GetFinding(findingID); err == nil && f != nil && f.TaskID != nil {
		return llmrec.WithTaskID(ctx, i64s(*f.TaskID))
	}
	return ctx
}

// TaskBudgetDTO is the compact budget object of the task list and detail.
type TaskBudgetDTO struct {
	TokenBudget     int64    `json:"token_budget"`      // 0 = no token limit
	TokensUsed      int64    `json:"tokens_used"`       // input + output tokens; cached tokens are a subset of input
	CostBudget      *float64 `json:"cost_budget"`       // null = no cost limit
	EstimatedCost   float64  `json:"estimated_cost"`    // from calls whose profile had prices, in currency
	Currency        string   `json:"currency"`          // currency of cost_budget and estimated_cost
	UnknownCost     bool     `json:"unknown_cost"`      // some calls had no price (or another currency)
	PercentUsed     float64  `json:"percent_used"`      // larger of the token and cost shares; 0 without a budget
	State           string   `json:"state"`             // ok | warning | reached
	PausedForBudget bool     `json:"paused_for_budget"` // paused by a budget hold that has not been resumed yet
}

func taskBudgetDTO(v *db.TaskBudgetView) TaskBudgetDTO {
	if v == nil {
		return TaskBudgetDTO{Currency: db.DefaultCurrency, State: "ok"}
	}
	return TaskBudgetDTO{
		TokenBudget: v.TokenBudget, TokensUsed: v.Tokens, CostBudget: v.CostBudget,
		EstimatedCost: v.Cost, Currency: v.CostCurrency, UnknownCost: v.UnknownCostCalls > 0,
		PercentUsed: v.Percent(), State: v.State(), PausedForBudget: v.PausedForBudget,
	}
}

// validateBudgetInput checks the budget fields of a create or patch request.
func validateBudgetInput(token *int64, cost *float64, currency *string) error {
	if token != nil && (*token < 0 || *token > db.MaxTaskTokenBudget) {
		return db.ErrBudgetInvalid
	}
	if cost != nil && (math.IsNaN(*cost) || math.IsInf(*cost, 0) || *cost < 0) {
		return db.ErrBudgetInvalid
	}
	if currency != nil {
		if _, err := db.NormalizeCurrency(*currency); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) registerBudgetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /api/tasks/{id}/budget", s.patchTaskBudget)
}

// patchTaskBudget changes a task's budgets in any state. Omitted fields are
// unchanged; token_budget 0 or cost_budget 0 removes that limit. It does not
// resume the task: after raising a budget the operator resumes it.
func (s *Server) patchTaskBudget(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTaskMetadataRequestSize)
	var req struct {
		TokenBudget  *int64   `json:"token_budget"`
		CostBudget   *float64 `json:"cost_budget"`
		CostCurrency *string  `json:"cost_currency"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.TokenBudget == nil && req.CostBudget == nil && req.CostCurrency == nil {
		writeErr(w, http.StatusBadRequest, locale.Text(responseLanguage(w), "Provide at least one of token_budget, cost_budget or cost_currency"))
		return
	}
	if err := validateBudgetInput(req.TokenBudget, req.CostBudget, req.CostCurrency); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id, _ := parseTaskID(t.ID)
	if err := s.m.pg.UpdateTaskBudget(id, req.TokenBudget, req.CostBudget, req.CostCurrency); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	log.Printf("[budget] task %s budget updated", t.ID)
	writeJSON(w, http.StatusOK, taskDTO(t, s.resolvedTaskStatus(t)))
}
