package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/llmrec"
)

// ledgerProvider is a fake model that bills a fixed number of tokens per call
// and writes the usage row the way llmrec.Recorder does, so the budget gate sees
// exactly what production would.
type ledgerProvider struct {
	pg       *db.DB
	in, out  int
	delay    time.Duration
	mu       sync.Mutex
	calls    int
	active   int
	maxAtOnc int
}

func (p *ledgerProvider) bill(ctx context.Context) llm.Usage {
	p.mu.Lock()
	p.calls++
	p.active++
	p.maxAtOnc = max(p.maxAtOnc, p.active)
	p.mu.Unlock()
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	usage := llm.Usage{InputTokens: p.in, OutputTokens: p.out}
	_ = p.pg.InsertLLMUsage(&db.LLMUsage{TaskID: llmrec.TaskIDFrom(ctx), Model: "stub", ProfileName: "stub", Status: "ok",
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens})
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return usage
}

func (p *ledgerProvider) Complete(ctx context.Context, _ llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return llm.Message{}, "end_turn", p.bill(ctx), nil
}

func (p *ledgerProvider) Stream(ctx context.Context, _ llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		usage := p.bill(ctx)
		yield(llm.StreamEvent{Type: llm.SEMessageStart, Usage: usage}, nil)
	}
}

func i64p(v int64) *int64 { return &v }

type budgetFixture struct {
	s    *Server
	m    *Manager
	prov *ledgerProvider
}

// newBudgetFixture builds a server whose global provider is the ledger stub, so
// a taskLLMRuntime resolves to it without any real model.
func newBudgetFixture(t *testing.T, in, out int, delay time.Duration) *budgetFixture {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	t.Cleanup(func() { m.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	prov := &ledgerProvider{pg: m.pg, in: in, out: out, delay: delay}
	s := &Server{m: m, engine: NewEngine(m), ctx: ctx, chatBusy: map[string]bool{}, chatCancel: map[string]context.CancelCauseFunc{}}
	s.llmOn, s.llmProv, s.llmCfg = true, prov, agent.Config{}
	return &budgetFixture{s: s, m: m, prov: prov}
}

func (f *budgetFixture) task(t *testing.T, opts db.TaskCreateOptions) *Task {
	t.Helper()
	task, err := f.m.CreateTaskWithOptions("budget task", "budget goal", opts)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := parseTaskID(task.ID)
	t.Cleanup(func() {
		_, _ = f.m.pg.Exec(`DELETE FROM llm_usage WHERE task_id=$1`, task.ID)
		_ = f.m.pg.DeleteTask(id)
	})
	return task
}

func (f *budgetFixture) view(t *testing.T, task *Task) db.TaskBudgetView {
	t.Helper()
	id, _ := parseTaskID(task.ID)
	v, err := f.m.pg.TaskBudgetView(id)
	if err != nil || v == nil {
		t.Fatalf("budget view = %v, %v", v, err)
	}
	return *v
}

func (f *budgetFixture) activities(t *testing.T, task *Task, prefix string) int {
	t.Helper()
	list, _, err := task.Store.ActivityList(nil, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range list {
		if strings.HasPrefix(a.Summary, prefix) {
			n++
		}
	}
	return n
}

func (f *budgetFixture) runtime(task *Task) *taskLLMRuntime {
	return &taskLLMRuntime{s: f.s, taskID: task.ID, agentKey: "worker"}
}

func budgetCause(err error) *agent.AbortCause {
	var abort *agent.AbortCause
	if errors.As(err, &abort) && abort.Code == "budget_reached" {
		return abort
	}
	return nil
}

func TestBudgetPausesAtLimitThenRefusesResumeUntilRaised(t *testing.T) {
	f := newBudgetFixture(t, 500, 100, 0) // 600 tokens per call
	task := f.task(t, db.TaskCreateOptions{TokenBudget: i64p(1000)})
	rt := f.runtime(task)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, _, _, err := rt.Complete(ctx, llm.CompletionRequest{}); err != nil {
			t.Fatalf("call %d under the limit failed: %v", i+1, err)
		}
	}
	if v := f.view(t, task); v.Tokens != 1200 || v.ReachedKind() != "tokens" {
		t.Fatalf("after two calls: %+v", v)
	}
	// The third call finds the budget used up: it must not reach the provider, and
	// the task is paused with its own reason.
	_, _, _, err := rt.Complete(ctx, llm.CompletionRequest{})
	cause := budgetCause(err)
	if cause == nil || !strings.Contains(cause.Text, "1200 of 1000 tokens used") {
		t.Fatalf("third call error = %v", err)
	}
	if f.prov.calls != 2 {
		t.Fatalf("provider called %d times, want 2", f.prov.calls)
	}
	id, _ := parseTaskID(task.ID)
	state, err := f.m.pg.TaskApprovalState(id)
	if err != nil || state.Active() == nil || state.Active().Outcome != db.BudgetHoldOutcome ||
		!strings.Contains(state.Active().Reason, "1200 of 1000 tokens used") {
		t.Fatalf("persisted hold = %+v, %v", state.Active(), err)
	}
	if !task.lifecycleSnapshot().Paused || !f.s.engine.IsPaused(task.ID) {
		t.Fatal("task is not paused")
	}
	dto := taskDTO(task, f.s.resolvedTaskStatus(task))
	if dto.Status != "paused" || dto.Budget.State != "reached" || !dto.Budget.PausedForBudget || dto.Budget.TokensUsed != 1200 || dto.Budget.PercentUsed != 120 {
		t.Fatalf("dto = status %s budget %+v", dto.Status, dto.Budget)
	}
	// Every later call gets the same answer without a second hold or activity.
	if _, _, _, err = rt.Complete(ctx, llm.CompletionRequest{}); budgetCause(err) == nil {
		t.Fatalf("fourth call error = %v", err)
	}
	if n := f.activities(t, task, "Budget reached"); n != 1 {
		t.Fatalf("budget reached activity emitted %d times", n)
	}
	if state, _ = f.m.pg.TaskApprovalState(id); len(state.Holds) != 1 {
		t.Fatalf("holds = %d, want 1", len(state.Holds))
	}

	// Resuming without raising the budget leaves it paused for the same reason.
	resumer := newAdmissionTestServer(f.m, func(*Task) bool { return true })
	_, err = resumer.applyTaskControl(task, "resume")
	if !errors.Is(err, ErrTaskBudgetReached) || !strings.Contains(err.Error(), "1200 of 1000") {
		t.Fatalf("resume over budget = %v", err)
	}
	if !task.lifecycleSnapshot().Paused {
		t.Fatal("resume over budget un-paused the task")
	}
	rec := httptest.NewRecorder()
	writeError(rec, 500, err)
	if rec.Code != http.StatusConflict {
		t.Fatalf("budget refusal status = %d", rec.Code)
	}

	// Raising the budget (in any state) lets the operator resume.
	patch := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+task.ID+"/budget", strings.NewReader(body))
		req.SetPathValue("id", task.ID)
		rec := httptest.NewRecorder()
		f.s.patchTaskBudget(rec, req)
		return rec
	}
	rec = patch(`{"token_budget": 5000}`)
	if rec.Code != 200 {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	var raised TaskDTO
	if err = json.Unmarshal(rec.Body.Bytes(), &raised); err != nil || raised.Budget.TokenBudget != 5000 || raised.Budget.State != "ok" || !raised.Budget.PausedForBudget {
		t.Fatalf("after raise: %+v %v", raised.Budget, err)
	}
	if _, err = resumer.applyTaskControl(task, "resume"); err != nil {
		t.Fatalf("resume after raising: %v", err)
	}
	if task.lifecycleSnapshot().Paused {
		t.Fatal("task still paused after resume")
	}
	if state, _ = f.m.pg.TaskApprovalState(id); state.Active() != nil {
		t.Fatalf("hold still active after resume: %+v", state.Active())
	}
	if v := f.view(t, task); v.PausedForBudget {
		t.Fatal("view still reports a budget pause")
	}
}

func TestBudgetInFlightCallsFinishBeforeExecutionIsCancelled(t *testing.T) {
	f := newBudgetFixture(t, 500, 100, 300*time.Millisecond)
	task := f.task(t, db.TaskCreateOptions{TokenBudget: i64p(1000)})
	id, _ := parseTaskID(task.ID)
	// Put the task over budget with one slow call already running.
	if err := f.m.pg.InsertLLMUsage(&db.LLMUsage{TaskID: task.ID, Model: "stub", InputTokens: 900, OutputTokens: 300, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	release := func() func() {
		g := f.s.budgetGateFor(task.ID)
		g.mu.Lock()
		g.inflight++
		g.mu.Unlock()
		return g.release
	}()
	execCtx := f.s.engine.execContextFor(f.s.ctx, task.ID)

	result := make(chan error, 1)
	go func() {
		_, err := f.s.budgetAcquire(context.Background(), task.ID)
		result <- err
	}()
	time.Sleep(150 * time.Millisecond)
	if execCtx.Err() != nil {
		t.Fatal("execution was cancelled while a model call was still in flight")
	}
	if state, _ := f.m.pg.TaskApprovalState(id); state.Active() == nil {
		t.Fatal("the pause must be persisted at once")
	}
	select {
	case err := <-result:
		t.Fatalf("stopped call returned before the in-flight call finished: %v", err)
	default:
	}
	release()
	select {
	case err := <-result:
		if budgetCause(err) == nil {
			t.Fatalf("stopped call error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stopped call never returned after the in-flight call finished")
	}
	if cause := context.Cause(execCtx); budgetCause(cause) == nil || !taskExecutionPaused(cause) {
		t.Fatalf("execution cancel cause = %v", cause)
	}
}

// With budget for roughly one call, several workers racing for it must not all
// start: calls are serialized near the limit, so only the call that crosses the
// line overshoots, by at most one call.
func TestBudgetConcurrentWorkersOvershootByAtMostOneCall(t *testing.T) {
	f := newBudgetFixture(t, 700, 100, 30*time.Millisecond) // 800 tokens per call
	task := f.task(t, db.TaskCreateOptions{TokenBudget: i64p(1000)})
	rt := f.runtime(task)

	const workers = 6
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := rt.Complete(context.Background(), llm.CompletionRequest{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	stopped := 0
	for err := range errs {
		if err != nil {
			if budgetCause(err) == nil {
				t.Fatalf("unexpected error: %v", err)
			}
			stopped++
		}
	}
	v := f.view(t, task)
	if f.prov.maxAtOnc != 1 {
		t.Fatalf("%d calls ran at once next to the limit", f.prov.maxAtOnc)
	}
	if f.prov.calls != 2 || stopped != workers-2 {
		t.Fatalf("calls=%d stopped=%d, want 2 calls (one under the limit, one crossing it) and %d stopped", f.prov.calls, stopped, workers-2)
	}
	if v.Tokens > 1000+800 {
		t.Fatalf("task used %d tokens, more than the budget plus one call", v.Tokens)
	}
	if !task.lifecycleSnapshot().Paused {
		t.Fatal("task not paused")
	}
}

// Far from the limit nothing is serialized: reservation only bites near the budget.
func TestBudgetFarFromLimitCallsRunConcurrently(t *testing.T) {
	f := newBudgetFixture(t, 200, 100, 80*time.Millisecond)
	task := f.task(t, db.TaskCreateOptions{TokenBudget: i64p(10_000_000)})
	rt := f.runtime(task)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, _, err := rt.Complete(context.Background(), llm.CompletionRequest{}); err != nil {
				t.Errorf("call failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if f.prov.maxAtOnc < 2 {
		t.Fatalf("calls were serialized far from the limit (max at once %d)", f.prov.maxAtOnc)
	}
	if v := f.view(t, task); v.Tokens != 1500 || v.State() != "ok" {
		t.Fatalf("view = %+v", v)
	}
}

func TestBudgetStreamCallsAreGatedToo(t *testing.T) {
	f := newBudgetFixture(t, 800, 300, 0)
	task := f.task(t, db.TaskCreateOptions{TokenBudget: i64p(1000)})
	rt := f.runtime(task)
	if _, err := collectTaskLLMStream(rt.Stream(context.Background(), llm.CompletionRequest{})); err != nil {
		t.Fatalf("first stream: %v", err)
	}
	if _, err := collectTaskLLMStream(rt.Stream(context.Background(), llm.CompletionRequest{})); budgetCause(err) == nil {
		t.Fatalf("second stream error = %v", err)
	}
	if f.prov.calls != 1 {
		t.Fatalf("provider streamed %d times", f.prov.calls)
	}
}

func TestBudgetWarningFiresOnceAtEightyPercent(t *testing.T) {
	f := newBudgetFixture(t, 100, 50, 0)
	task := f.task(t, db.TaskCreateOptions{TokenBudget: i64p(1000)})
	rt := f.runtime(task)
	ctx := context.Background()
	call := func() {
		t.Helper()
		if _, _, _, err := rt.Complete(ctx, llm.CompletionRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ { // 750 tokens: 75 percent
		call()
	}
	if n := f.activities(t, task, "Budget warning"); n != 0 {
		t.Fatalf("warning before 80 percent: %d", n)
	}
	call() // reads 750 at admission, records up to 900
	call() // reads 900: crossed 80 percent, records up to 1050
	if n := f.activities(t, task, "Budget warning"); n != 1 {
		t.Fatalf("warning emitted %d times, want exactly once", n)
	}
	if _, _, _, err := rt.Complete(ctx, llm.CompletionRequest{}); budgetCause(err) == nil {
		t.Fatalf("call over the budget = %v", err)
	}
	if n := f.activities(t, task, "Budget warning"); n != 1 {
		t.Fatalf("warning repeated after the budget was reached: %d", n)
	}
	if v := f.view(t, task); v.WarnedAt == nil || v.State() != "reached" {
		t.Fatalf("view = %+v", v)
	}
	// Changing the budget re-arms the warning for the new limit.
	id, _ := parseTaskID(task.ID)
	if err := f.m.pg.UpdateTaskBudget(id, i64p(1100), nil, nil); err != nil {
		t.Fatal(err)
	}
	if v := f.view(t, task); v.WarnedAt != nil {
		t.Fatal("budget edit did not re-arm the warning")
	}
}

func TestBudgetCostBudgetPausesAndUnknownCostIsNeverZero(t *testing.T) {
	f := newBudgetFixture(t, 100, 100, 0)
	task := f.task(t, db.TaskCreateOptions{CostBudget: func() *float64 { v := 1.0; return &v }(), CostCurrency: "USD"})
	rt := f.runtime(task)
	// Unpriced calls cost nothing we can see, so they never trip a cost budget,
	// but they are reported as unknown.
	for i := 0; i < 3; i++ {
		if _, _, _, err := rt.Complete(context.Background(), llm.CompletionRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	dto := taskBudgetDTO(ptrView(f.view(t, task)))
	if !dto.UnknownCost || dto.EstimatedCost != 0 || dto.CostBudget == nil || dto.State != "ok" || dto.TokensUsed != 600 {
		t.Fatalf("dto = %+v", dto)
	}
	cost := 1.25
	if err := f.m.pg.InsertLLMUsage(&db.LLMUsage{TaskID: task.ID, Model: "stub", InputTokens: 10, OutputTokens: 10, Status: "ok", Cost: &cost, Currency: "USD"}); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := rt.Complete(context.Background(), llm.CompletionRequest{})
	cause := budgetCause(err)
	if cause == nil || !strings.Contains(cause.Text, "1.2500 of 1.0000 USD estimated") {
		t.Fatalf("cost budget error = %v", err)
	}
	if dto = taskBudgetDTO(ptrView(f.view(t, task))); dto.State != "reached" || dto.PercentUsed != 125 {
		t.Fatalf("dto = %+v", dto)
	}
}

func ptrView(v db.TaskBudgetView) *db.TaskBudgetView { return &v }

// Work an operator starts on a finished task counts against it, and is refused
// when the budget is already used up.
func TestBudgetExplicitActionsOnFinishedTaskAreRefused(t *testing.T) {
	f := newBudgetFixture(t, 100, 100, 0)
	task := f.task(t, db.TaskCreateOptions{TokenBudget: i64p(500)})
	id, _ := parseTaskID(task.ID)
	if err := f.m.pg.InsertLLMUsage(&db.LLMUsage{TaskID: task.ID, Model: "stub", InputTokens: 400, OutputTokens: 200, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.pg.Exec(`UPDATE tasks SET status='done', completed_at=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	task.updateLifecycle(func(state *taskLifecycleState) { state.Status = "done" })

	admitter := newAdmissionTestServer(f.m, func(*Task) bool { return true })
	if _, err := admitter.admitTask(task, "resume"); !errors.Is(err, ErrTaskBudgetReached) {
		t.Fatalf("admit finished task over budget = %v", err)
	}
	if err := f.s.taskBudgetAdmission(id); !errors.Is(err, ErrTaskBudgetReached) || !strings.Contains(err.Error(), "600 of 500 tokens used") {
		t.Fatalf("taskBudgetAdmission = %v", err)
	}
	// The same check guards retest, verification and fix generation through the finding's task.
	nodeID, err := task.Store.AddNode(db.KindFact, map[string]any{"summary": "fact"}, 1, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	finding, err := f.m.pg.AddFinding(id, nodeID, "budget-vuln", "title", db.SeverityLow, "summary", "evidence", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.findingBudgetCheck(finding); !errors.Is(err, ErrTaskBudgetReached) {
		t.Fatalf("findingBudgetCheck = %v", err)
	}
	if err = f.m.pg.UpdateTaskBudget(id, i64p(0), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = f.s.findingBudgetCheck(finding); err != nil {
		t.Fatalf("no budget must not refuse: %v", err)
	}
	// A finished task cannot be paused: a model call is refused outright.
	if err = f.m.pg.UpdateTaskBudget(id, i64p(500), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = f.runtime(task).Complete(context.Background(), llm.CompletionRequest{}); budgetCause(err) == nil {
		t.Fatalf("model call on a finished task over budget = %v", err)
	}
	if f.prov.calls != 0 {
		t.Fatal("provider was called")
	}
}

func TestTaskBudgetAPIShape(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	t.Cleanup(func() { m.Close() })
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	prevDefault := m.pg.DefaultTaskTokenBudget()
	t.Cleanup(func() { _ = m.pg.SetDefaultTaskTokenBudget(prevDefault) })

	create := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.createTask(rec, httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body)))
		return rec
	}
	var created []string
	t.Cleanup(func() {
		for _, id := range created {
			n, _ := parseTaskID(id)
			_, _ = m.pg.Exec(`DELETE FROM llm_usage WHERE task_id=$1`, id)
			_ = m.pg.DeleteTask(n)
		}
	})

	rec := create(`{"description":"api","goal":"g","token_budget":5000,"cost_budget":2.5,"cost_currency":"eur"}`)
	if rec.Code != 201 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	var dto TaskDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &dto)
	created = append(created, dto.ID)
	var budget map[string]any
	if err = json.Unmarshal(raw["budget"], &budget); err != nil {
		t.Fatalf("task dto has no budget object: %s", rec.Body.String())
	}
	for _, key := range []string{"token_budget", "tokens_used", "cost_budget", "estimated_cost", "currency", "unknown_cost", "percent_used", "state", "paused_for_budget"} {
		if _, ok := budget[key]; !ok {
			t.Fatalf("budget object lacks %q: %v", key, budget)
		}
	}
	if b := dto.Budget; b.TokenBudget != 5000 || b.CostBudget == nil || *b.CostBudget != 2.5 || b.Currency != "EUR" || b.State != "ok" || b.TokensUsed != 0 || b.UnknownCost || b.PausedForBudget {
		t.Fatalf("created budget = %+v", b)
	}

	// Detail and list carry the same object, with live usage.
	_ = m.pg.InsertLLMUsage(&db.LLMUsage{TaskID: dto.ID, Model: "stub", InputTokens: 3000, OutputTokens: 1200, CacheRead: 2500, Status: "ok"})
	get := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+dto.ID, nil)
	req.SetPathValue("id", dto.ID)
	s.getTask(get, req)
	var detail TaskDTO
	if err = json.Unmarshal(get.Body.Bytes(), &detail); err != nil || detail.Budget.TokensUsed != 4200 || detail.Budget.State != "warning" || detail.Budget.PercentUsed != 84 || !detail.Budget.UnknownCost {
		t.Fatalf("detail budget = %+v (%v)", detail.Budget, err)
	}
	list := httptest.NewRecorder()
	s.listTasks(list, httptest.NewRequest(http.MethodGet, "/api/tasks", nil))
	var listed struct {
		Tasks []TaskDTO `json:"tasks"`
	}
	if err = json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range listed.Tasks {
		if item.ID == dto.ID {
			found = true
			if !reflect.DeepEqual(item.Budget, detail.Budget) {
				t.Fatalf("list budget %+v differs from detail %+v", item.Budget, detail.Budget)
			}
		}
	}
	if !found {
		t.Fatal("task missing from the list")
	}

	// PATCH: partial updates, zero removes, validation, 404.
	patch := func(id, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+id+"/budget", strings.NewReader(body))
		req.SetPathValue("id", id)
		s.patchTaskBudget(rec, req)
		return rec
	}
	if rec = patch(dto.ID, `{"token_budget":0,"cost_budget":0}`); rec.Code != 200 {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &dto)
	if dto.Budget.TokenBudget != 0 || dto.Budget.CostBudget != nil || dto.Budget.State != "ok" || dto.Budget.Currency != "EUR" || dto.Budget.PercentUsed != 0 {
		t.Fatalf("removed budget = %+v", dto.Budget)
	}
	for _, body := range []string{`{}`, `{"token_budget":-1}`, `{"cost_budget":-2}`, `{"cost_currency":"dollars"}`, `{"token_budget":"x"}`} {
		if rec = patch(dto.ID, body); rec.Code != 400 {
			t.Fatalf("%s accepted: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec = patch("999999999", `{"token_budget":1}`); rec.Code != 404 {
		t.Fatalf("unknown task = %d", rec.Code)
	}
	for _, body := range []string{`{"description":"a","token_budget":-3}`, `{"description":"a","cost_currency":"xx"}`, `{"description":"a","cost_budget":-1}`} {
		if rec = create(body); rec.Code != 400 {
			t.Fatalf("create %s = %d %s", body, rec.Code, rec.Body.String())
		}
	}

	// Settings carry the default for new tasks that do not set one.
	put := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"default_task_token_budget":7000}`))
	s.putSettings(put, req)
	var settings map[string]any
	if err = json.Unmarshal(put.Body.Bytes(), &settings); err != nil || settings["default_task_token_budget"] != float64(7000) {
		t.Fatalf("settings = %v %v", settings, err)
	}
	rec = create(`{"description":"inherits","goal":"g"}`)
	var inherited TaskDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &inherited)
	created = append(created, inherited.ID)
	if rec.Code != 201 || inherited.Budget.TokenBudget != 7000 {
		t.Fatalf("task did not inherit the default: %d %+v", rec.Code, inherited.Budget)
	}
	rec = create(`{"description":"explicit zero","goal":"g","token_budget":0}`)
	var unlimited TaskDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &unlimited)
	created = append(created, unlimited.ID)
	if unlimited.Budget.TokenBudget != 0 {
		t.Fatalf("an explicit 0 must mean no limit even with a default: %+v", unlimited.Budget)
	}
	bad := httptest.NewRecorder()
	s.putSettings(bad, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"default_task_token_budget":-1}`)))
	if bad.Code != 400 {
		t.Fatalf("negative default = %d", bad.Code)
	}
}

func TestProfilePricesAPI(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	t.Cleanup(func() { m.Close() })
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	name := fmt.Sprintf("price-api-%d", time.Now().UnixNano())
	save := func(body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		s.pgSaveProfile(rec, httptest.NewRequest(http.MethodPost, "/api/llm/profiles", bytes.NewBufferString(body)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	listed := func() LLMProfileDTO {
		rec := httptest.NewRecorder()
		s.pgListProfiles(rec, httptest.NewRequest(http.MethodGet, "/api/llm/profiles", nil))
		var out struct {
			Profiles []LLMProfileDTO `json:"profiles"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		for _, p := range out.Profiles {
			if p.Name == name {
				return p
			}
		}
		t.Fatal("profile not listed")
		return LLMProfileDTO{}
	}
	code, out := save(fmt.Sprintf(`{"name":%q,"format":"openai","model":"m","api_key":"sk-test-1234","price_input_per_million":2.5,"price_cached_input_per_million":0.25,"price_output_per_million":10,"price_currency":"eur"}`, name))
	if code != 200 {
		t.Fatalf("create = %d %v", code, out)
	}
	id := int64(out["id"].(float64))
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM llm_profiles WHERE id=$1`, id) })
	p := listed()
	if p.PriceInput == nil || *p.PriceInput != 2.5 || *p.PriceCachedInput != 0.25 || *p.PriceOutput != 10 || p.PriceCurrency != "EUR" {
		t.Fatalf("returned prices = %+v", p)
	}
	// An update that does not mention prices keeps them; sending nulls clears them.
	if code, out = save(fmt.Sprintf(`{"id":%d,"name":%q,"format":"openai","model":"m2"}`, id, name)); code != 200 {
		t.Fatalf("update = %d %v", code, out)
	}
	if p = listed(); p.Model != "m2" || p.PriceInput == nil || *p.PriceInput != 2.5 {
		t.Fatalf("prices lost on an update without them: %+v", p)
	}
	if code, out = save(fmt.Sprintf(`{"id":%d,"name":%q,"format":"openai","model":"m2","price_input_per_million":null,"price_output_per_million":null}`, id, name)); code != 200 {
		t.Fatalf("clear = %d %v", code, out)
	}
	if p = listed(); p.PriceInput != nil || p.PriceOutput != nil || p.PriceCachedInput != nil || p.PriceCurrency != "USD" {
		t.Fatalf("prices not cleared: %+v", p)
	}
	for _, body := range []string{`"price_input_per_million":-1`, `"price_output_per_million":"free"`, `"price_currency":"dollars"`} {
		if code, _ = save(fmt.Sprintf(`{"id":%d,"name":%q,"format":"openai","model":"m2",%s}`, id, name, body)); code != 400 {
			t.Fatalf("%s accepted: %d", body, code)
		}
	}
}
