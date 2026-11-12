package server

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/sidequestion"
)

var abortWaitingForApproval = agent.Causef("waiting_for_approval", "Waiting for tool permission", "All task work stopped for a tool permission request. Resolve the pending requests and click Resume to continue.")

func (s *Server) holdTaskForApproval(taskID string, pendingID int64, expires *time.Time, action string) (bool, error) {
	s.concMu.Lock()
	defer s.concMu.Unlock()
	t, ok := s.m.Task(taskID)
	if !ok || s.engine.IsDeleting(taskID) {
		return false, fmt.Errorf("task is unavailable")
	}
	if !s.engine.beginTaskOperation(taskID) {
		return false, fmt.Errorf("task is being deleted")
	}
	defer s.engine.decInflight(taskID)
	id, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil {
		return false, err
	}
	s.m.taskStateMu.Lock()
	held, err := s.m.pg.BeginTaskApprovalHold(id, pendingID, expires, action)
	if held || err != nil {
		// Publish the persistent pause before cancellation can classify any worker.
		t.updateLifecycle(func(state *taskLifecycleState) { state.Paused = true; state.Queued = false; state.QueuedAt = 0 })
		s.engine.Pause(taskID, abortWaitingForApproval)
		if stop, ok := s.engine.approvalFinalCancel.Load(taskID); ok {
			stop.(context.CancelCauseFunc)(abortWaitingForApproval)
		}
	}
	s.m.taskStateMu.Unlock()
	if !held && err == nil {
		return false, nil
	}
	s.cancelTaskChat(taskID, abortWaitingForApproval)
	if s.side != nil {
		s.side.commands.Lock()
		s.cancelSideWhere(func(p sidequestion.Parent) bool { return p.TaskID == id })
		s.side.commands.Unlock()
	}
	s.engine.emitActivity(t, db.Activity{Worker: "system", Kind: "text", Summary: fmt.Sprintf("Waiting for tool permission (#%d). All task workers and planning have stopped. Active duration and task timeout are frozen. Resolve the request in Approvals, then click Resume.", pendingID)})
	go s.reconcileConcurrency()
	return held, err
}

// Durable timeouts affect the approval record, never task admission. A denied
// or timed-out request leaves the task held for the user to review and resume.
func (s *Server) runApprovalTimeouts(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if err := s.m.pg.ExpireTaskApprovals(); err != nil {
			log.Printf("[approval] expire task requests: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (s *Server) taskApprovalHeld(taskID string) bool {
	if s.m.pg == nil {
		return false
	}
	held, err := s.m.pg.TaskApprovalHeld(taskID)
	return err != nil || held
}
