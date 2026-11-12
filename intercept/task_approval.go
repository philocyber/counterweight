package intercept

import (
	"context"
	"time"
)

// TaskApprovalHandler persists a task-wide hold and cancels its active runs.
// A false result means a decision won the race before the hold was installed.
type TaskApprovalHandler func(taskID string, pendingID int64, expires *time.Time, timeoutAction string) (bool, error)

func (i *Interceptor) SetTaskApprovalHandler(h TaskApprovalHandler) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.taskApproval = h
}
func (i *Interceptor) taskApprovalHandler() TaskApprovalHandler {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.taskApproval
}

// TaskHeld applies to every tool, including tools outside the interception list.
func (i *Interceptor) TaskHeld(ctx context.Context) (bool, error) {
	if i.taskApprovalHandler() == nil {
		return false, nil
	}
	taskID, _ := taskInfoFromCtx(ctx)
	if taskID == "" {
		return false, nil
	}
	return i.db.TaskApprovalHeld(taskID)
}

// ReplayApproved consumes only the identical task/tool/JSON-input approval.
// Explicit deny rules are evaluated by the caller before using this permission.
func (i *Interceptor) ReplayApproved(ctx context.Context, tool string, input []byte, dec Decision) (bool, error) {
	taskID, _ := taskInfoFromCtx(ctx)
	if taskID == "" {
		return false, nil
	}
	id, err := i.db.ConsumeTaskApprovalReplay(taskID, tool, input)
	if err != nil || id == 0 {
		return false, err
	}
	audit := auditFor(ctx, dec, input, "allowed")
	if err = i.db.RebindTaskApprovalReplay(id, audit); err != nil {
		return false, err
	}
	i.bindResult(ctx, id, audit)
	return true, nil
}
