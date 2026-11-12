package server

import (
	"context"
	"encoding/json"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/intercept"
)

func TestTaskApprovalHoldStopsTaskAndSurvivesCancellation(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	defer restoreConcurrencySetting(t, m)()
	if err = m.SetConcurrency(false, 1); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateTask("permission hold fixture", "wait for the user", nil, 3600, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) }()
	s := newAdmissionTestServer(m, nil)
	m.interceptor.SetTaskApprovalHandler(s.holdTaskForApproval)
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	// Simulate two active runs sharing the task execution context plus main chat.
	base := s.engine.execContextFor(context.Background(), task.ID)
	worker1, worker2 := base, base
	chat, chatCancel := context.WithCancelCause(context.Background())
	s.chatCancel[task.ID] = chatCancel
	if _, err = m.StampTaskFirstRun(task.ID); err != nil {
		t.Fatal(err)
	}
	var original time.Time
	if err = m.pg.QueryRow(`SELECT deadline_at FROM tasks WHERE id=$1`, id).Scan(&original); err != nil {
		t.Fatal(err)
	}
	ctx := intercept.WithTaskContext(base, task.ID, "worker fixture", nil)
	input := []byte(`{"command":"printf permission-fixture"}`)
	if m.interceptor.HandleAsk(ctx, 0, intercept.Decision{Action: "ask", Message: "Needs permission"}, "Bash", input) {
		t.Fatal("pending call executed")
	}
	if worker1.Err() == nil || worker2.Err() == nil || chat.Err() == nil || !s.engine.IsPaused(task.ID) || !task.lifecycleSnapshot().Paused {
		t.Fatal("hold did not stop every active task run")
	}
	if !taskExecutionPaused(context.Cause(worker1)) {
		t.Fatal("permission cancellation would incorrectly complete the intent")
	}
	state, err := m.pg.TaskApprovalState(id)
	if err != nil || len(state.Pending) != 1 || len(state.Holds) != 1 {
		t.Fatalf("hold: %+v %v", state, err)
	}
	pending := state.Pending[0].ID
	if _, err = s.admitPausedTask(task); err == nil {
		t.Fatal("resumed with unresolved permission")
	}
	// Hold blocks even tools outside the interception list and saved grants.
	hooks := task.Guard.Hooks()
	fresh := intercept.WithTaskContext(context.Background(), task.ID, "other worker", nil)
	blocked, reason, _ := hooks.PreToolUse(fresh, "Read", []byte(`{"file_path":"/tmp/fixture"}`))
	if !blocked || !strings.Contains(reason, intercept.ApprovalDeferredMarker) || strings.Contains(reason, "prohibited") {
		t.Fatalf("permission hold was presented as a denial: blocked=%v reason=%q", blocked, reason)
	}
	// Simulate a long human wait without sleeping; restart must retain both request and budget.
	if _, err = m.pg.Exec(`UPDATE task_approval_holds SET started_at=now()-interval '10 minutes' WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	before, err := m.pg.GetInterceptDetail(pending)
	if err != nil || before.Status != "pending" {
		t.Fatalf("cancellation resolved approval: %+v %v", before, err)
	}
	if err = m.interceptor.Decide(pending, true); err != nil {
		t.Fatal(err)
	}
	if !task.lifecycleSnapshot().Paused {
		t.Fatal("approval unexpectedly resumed task")
	}
	// Old worker still draining: admission must wait instead of creating another run.
	s.engine.beginTaskOperation(task.ID)
	if _, err = s.admitPausedTask(task); err == nil {
		t.Fatal("resumed while old worker was draining")
	}
	s.engine.decInflight(task.ID)
	if _, err = s.admitPausedTask(task); err != nil {
		t.Fatal(err)
	}
	state, err = m.pg.TaskApprovalState(id)
	if err != nil || state.Active() != nil {
		t.Fatalf("hold not closed: %+v %v", state, err)
	}
	var deadline time.Time
	if err = m.pg.QueryRow(`SELECT deadline_at FROM tasks WHERE id=$1`, id).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if delta := deadline.Sub(original); delta < 599*time.Second || delta > 602*time.Second {
		t.Fatalf("permission wait spent timeout budget: %s", delta)
	}
	// Exact input replay is scoped and single-use, including concurrent callers.
	if got, err := m.pg.ConsumeTaskApprovalReplay(task.ID, "Bash", []byte(`{"command":"another command"}`)); err != nil || got != 0 {
		t.Fatal("approval covered different input")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := m.pg.ConsumeTaskApprovalReplay(task.ID, "Bash", input)
			if err != nil {
				t.Error(err)
			}
			if got != 0 {
				mu.Lock()
				count++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if count != 1 {
		t.Fatalf("approval replayed %d times", count)
	}
}

func TestTaskApprovalHoldMultipleRequestsAndDurableTimeout(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	task, err := m.CreateTask("multiple permission requests", "wait for every request", nil, 120, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) }()
	s := newAdmissionTestServer(m, nil)
	m.interceptor.SetTaskApprovalHandler(s.holdTaskForApproval)
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	makeRequest := func(expiry *time.Time) int64 {
		t.Helper()
		id, err := m.pg.CreateInterceptPending(0, 0, task.ID, "worker", "Bash", []byte(`{"command":"pwd"}`), "permission fixture")
		if err != nil {
			t.Fatal(err)
		}
		if held, err := s.holdTaskForApproval(task.ID, id, expiry, "deny"); err != nil || !held {
			t.Fatalf("hold: %v %v", held, err)
		}
		return id
	}
	expired := time.Now().Add(-time.Second)
	first := makeRequest(&expired)
	second := makeRequest(nil)
	state, err := m.pg.TaskApprovalState(taskID)
	if err != nil || len(state.Holds) != 1 || len(state.Pending) != 2 {
		t.Fatalf("overlapping requests must share one hold: %+v %v", state, err)
	}
	start := state.Holds[0].StartedAt
	// A restart re-installs the barrier without resetting an approval's expiry.
	if held, err := s.holdTaskForApproval(task.ID, first, nil, "allow"); err != nil || !held {
		t.Fatalf("recover: %v %v", held, err)
	}
	if err = m.pg.ExpireTaskApprovals(); err != nil {
		t.Fatal(err)
	}
	saved, err := m.pg.GetInterceptDetail(first)
	if err != nil || saved.Status != "timeout" || saved.Audit.EffectiveAction != "deny" {
		t.Fatalf("timeout policy changed on restart: %+v %v", saved, err)
	}
	if _, err = s.admitPausedTask(task); err == nil {
		t.Fatal("first timeout resumed second pending approval")
	}
	if err = m.interceptor.Decide(second, false); err != nil {
		t.Fatal(err)
	}
	state, err = m.pg.TaskApprovalState(taskID)
	if err != nil || state.Active() == nil || !state.Active().StartedAt.Equal(start) || len(state.Pending) != 0 {
		t.Fatalf("denial must retain original hold until explicit Resume: %+v %v", state, err)
	}
	if !task.lifecycleSnapshot().Paused || !s.engine.IsPaused(task.ID) {
		t.Fatal("timeout or denial restarted the task")
	}
	if got, err := m.pg.ConsumeTaskApprovalReplay(task.ID, "Bash", []byte(`{"command":"pwd"}`)); err != nil || got != 0 {
		t.Fatal("denied request received an execution permission")
	}
}

// Exercise the real worker harness and state classification, without network or
// inference. A permission wait must never be marked done and wake the planner.
func TestTaskApprovalHoldWorkerRecovery(t *testing.T) {
	// Other server fixtures install process-wide tool resolvers for their own DB.
	// This harness fixture uses the standard local Write tool, independent of them.
	priorResolve, priorAugment := agent.ToolResolve, agent.ToolAugment
	agent.ToolResolve, agent.ToolAugment = nil, nil
	defer func() { agent.ToolResolve, agent.ToolAugment = priorResolve, priorAugment }()

	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	defer restoreConcurrencySetting(t, m)()
	if err = m.SetConcurrency(false, 1); err != nil {
		t.Fatal(err)
	}
	task, err := m.CreateTask("worker permission fixture", "write the approved local fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) }()
	s := newAdmissionTestServer(m, nil)
	m.interceptor.SetTaskApprovalHandler(s.holdTaskForApproval)
	rule, err := m.pg.CreateInterceptRule("permission fixture", "tool_name", "string", "Write", "ask", "Approve this file write", 99999, true, false, 60, "deny")
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteInterceptRule(rule.ID)
	m.interceptor.Invalidate()
	iid, err := task.Store.AddIntent(map[string]any{"summary": "Write a local permission test file"}, 5, nil, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(m.dir, "tasks", task.ID, "approved.txt")
	input, _ := json.Marshal(map[string]string{"file_path": target, "content": "permission fixture"})
	execute := func() {
		t.Helper()
		if claimed, err := task.Store.ClaimIntent(iid, "worker fixture"); err != nil || !claimed {
			t.Fatalf("claim: %v %v", claimed, err)
		}
		intent, err := task.Store.GetNode(iid)
		if err != nil {
			t.Fatal(err)
		}
		worker := agent.NewWorker(&customOperationProvider{name: "Write", input: input}, "fixture", m.dir, nil, 100000, 3)
		worker.SetNonStreaming(func() bool { return true })
		s.engine.runIntent(context.Background(), task, "worker fixture", worker, intent, "", "")
	}
	execute()
	intent, _ := task.Store.GetNode(iid)
	if intent.State != "open" {
		t.Fatalf("permission wait classified as %s instead of recoverable open intent", intent.State)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("unapproved file write executed")
	}
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	state, err := m.pg.TaskApprovalState(id)
	if err != nil || len(state.Pending) != 1 {
		t.Fatalf("pending permission: %+v %v", state, err)
	}
	if dto := taskDTO(task, "running"); dto.Status != "waiting_approval" {
		t.Fatalf("waiting task hidden from UI: %+v", dto)
	}
	if err = m.interceptor.Decide(state.Pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.admitPausedTask(task); err != nil {
		t.Fatal(err)
	}
	// An explicit deny added during the hold wins over a previously approved call.
	deny, err := m.pg.CreateInterceptRule("new deny fixture", "tool_name", "string", "Write", "deny", "Changed policy", 100000, true, false, 60, "deny")
	if err != nil {
		t.Fatal(err)
	}
	m.interceptor.Invalidate()
	replayCtx := intercept.WithTaskContext(context.Background(), task.ID, "worker fixture", nil)
	if blocked, _, _ := task.Guard.Hooks().PreToolUse(replayCtx, "Write", input); !blocked {
		t.Fatal("saved permission bypassed a new explicit deny")
	}
	if err = m.pg.DeleteInterceptRule(deny.ID); err != nil {
		t.Fatal(err)
	}
	m.interceptor.Invalidate()
	execute()
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "permission fixture" {
		t.Fatalf("approved recovery did not execute: %s %v", content, err)
	}
	intent, _ = task.Store.GetNode(iid)
	if intent.State != "done" {
		t.Fatalf("approved worker did not complete: %s", intent.State)
	}
	state, err = m.pg.TaskApprovalState(id)
	if err != nil || state.Active() != nil || len(state.Pending) != 0 {
		t.Fatalf("replayed call asked for approval again: %+v %v", state, err)
	}
	// Worker state must use the persisted domain state.
	if intent.Kind != db.KindIntent {
		t.Fatal("wrong worker node")
	}
}
