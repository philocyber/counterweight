package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type TaskApprovalHold struct {
	Reason    string     `json:"reason,omitempty"`
	ID        int64      `json:"id"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	Outcome   string     `json:"outcome"`
}
type TaskApprovalRequest struct {
	ID     int64  `json:"id"`
	Tool   string `json:"tool"`
	Agent  string `json:"agent"`
	Reason string `json:"reason"`
}
type TaskApprovalState struct {
	Holds   []TaskApprovalHold    `json:"holds"`
	Pending []TaskApprovalRequest `json:"pending"`
}

func (s TaskApprovalState) Active() *TaskApprovalHold {
	for i := range s.Holds {
		if s.Holds[i].EndedAt == nil {
			return &s.Holds[i]
		}
	}
	return nil
}
func (d *DB) TaskApprovalState(taskID int64) (TaskApprovalState, error) {
	s := TaskApprovalState{Holds: []TaskApprovalHold{}, Pending: []TaskApprovalRequest{}}
	rows, err := d.Query(`SELECT id,started_at,ended_at,outcome,reason FROM task_approval_holds WHERE task_id=$1 ORDER BY started_at`, taskID)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var h TaskApprovalHold
		if err = rows.Scan(&h.ID, &h.StartedAt, &h.EndedAt, &h.Outcome, &h.Reason); err != nil {
			rows.Close()
			return s, err
		}
		s.Holds = append(s.Holds, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	rows, err = d.Query(`SELECT id,tool_name,agent_name,reason FROM intercept_pending WHERE task_id=$1 AND status='pending' ORDER BY id`, fmt.Sprint(taskID))
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var p TaskApprovalRequest
		if err = rows.Scan(&p.ID, &p.Tool, &p.Agent, &p.Reason); err != nil {
			return s, err
		}
		s.Pending = append(s.Pending, p)
	}
	return s, rows.Err()
}
func (s *ExplorationStore) TaskApprovalState(taskID int64) (TaskApprovalState, error) {
	return s.db.TaskApprovalState(taskID)
}
func (d *DB) BeginTaskApprovalHold(taskID, pendingID int64, expires *time.Time, action string) (bool, error) {
	if action != "allow" {
		action = "deny"
	}
	tx, err := d.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRow(`SELECT status FROM tasks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, taskID).Scan(&status); err != nil {
		return false, err
	}
	if status == "done" || status == "failed" || status == "timeout" {
		return false, errors.New("Task has already finished")
	}
	r, err := tx.Exec(`UPDATE intercept_pending SET deferred_task=true,approval_expires_at=CASE WHEN deferred_task THEN approval_expires_at ELSE $3 END,approval_timeout_action=CASE WHEN deferred_task THEN approval_timeout_action ELSE $4 END WHERE id=$1 AND task_id=$2 AND status='pending'`, pendingID, fmt.Sprint(taskID), expires, action)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	_, err = tx.Exec(`INSERT INTO task_approval_holds(task_id) VALUES($1) ON CONFLICT(task_id) WHERE ended_at IS NULL DO NOTHING`, taskID)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`UPDATE tasks SET paused=true,queued=false,queued_at=NULL WHERE id=$1`, taskID)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func (d *DB) ConsumeTaskApprovalReplay(taskID, tool string, input []byte) (int64, error) {
	var id int64
	err := d.QueryRow(`UPDATE task_approval_replays SET consumed_at=now() WHERE pending_id=(SELECT r.pending_id FROM task_approval_replays r JOIN intercept_pending p ON p.id=r.pending_id WHERE r.task_id::text=$1 AND r.tool_name=$2 AND r.tool_input=$3::jsonb AND r.consumed_at IS NULL AND (p.status='allowed' OR(p.status='timeout' AND p.approval_timeout_action='allow')) ORDER BY r.pending_id LIMIT 1 FOR UPDATE OF r SKIP LOCKED) RETURNING pending_id`, taskID, tool, string(input)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
func (d *DB) RebindTaskApprovalReplay(id int64, audit *InterceptAudit) error {
	raw, err := json.Marshal(audit)
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE intercept_pending SET audit=(COALESCE(audit,'{}'::jsonb)-'output'-'output_truncated'-'execution_ended_at')||($2::jsonb-'user_message'-'context'-'initial_action'-'initial_reason'-'model_fallback'-'captured_at') WHERE id=$1`, id, raw)
	return err
}

// RefreshTaskApprovalReplays covers recovery after a process exit between a
// decision and its in-memory notification. The unique key prevents reuse.
func (d *DB) RefreshTaskApprovalReplays(taskID int64) error {
	_, err := d.Exec(`INSERT INTO task_approval_replays(pending_id,task_id,tool_name,tool_input)
 SELECT p.id,$1,p.tool_name,p.tool_input FROM intercept_pending p
 WHERE p.task_id=$1::bigint::text AND p.deferred_task AND (p.status='allowed' OR(p.status='timeout' AND p.approval_timeout_action='allow'))
 AND EXISTS(SELECT 1 FROM task_approval_holds WHERE task_id=$1 AND ended_at IS NULL)
 ON CONFLICT(pending_id) DO NOTHING`, taskID)
	return err
}

// ApprovalReplay is one approved call that Resume has not used yet.
type ApprovalReplay struct {
	ToolName  string
	ToolInput string
}

// ListUnconsumedApprovalReplays returns approved calls still waiting for their one retry.
func (d *DB) ListUnconsumedApprovalReplays(taskID string) ([]ApprovalReplay, error) {
	rows, err := d.Query(`SELECT r.tool_name, r.tool_input::text
FROM task_approval_replays r
JOIN intercept_pending p ON p.id=r.pending_id
WHERE r.task_id::text=$1 AND r.consumed_at IS NULL
  AND (p.status='allowed' OR (p.status='timeout' AND p.approval_timeout_action='allow'))
ORDER BY r.pending_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApprovalReplay
	for rows.Next() {
		var r ApprovalReplay
		if err = rows.Scan(&r.ToolName, &r.ToolInput); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *ExplorationStore) ListTaskToolGrants(taskID string) ([]TaskToolGrant, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	return s.db.ListTaskToolGrants(taskID)
}

func (s *ExplorationStore) ListUnconsumedApprovalReplays(taskID string) ([]ApprovalReplay, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	return s.db.ListUnconsumedApprovalReplays(taskID)
}

func (d *DB) TaskApprovalHeld(taskID string) (bool, error) {
	var held bool
	err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM task_approval_holds WHERE task_id::text=$1 AND ended_at IS NULL)`, taskID).Scan(&held)
	return held, err
}
func (d *DB) ExpireTaskApprovals() error {
	_, err := d.Exec(`WITH expired AS (
 UPDATE intercept_pending SET status='timeout',decided_at=now(),audit=COALESCE(audit,'{}'::jsonb)||jsonb_build_object('effective_action',approval_timeout_action,'decision_reason','Approval timed out; the task remains paused until Resume','execution_status',CASE WHEN approval_timeout_action='allow' THEN 'awaiting_result' ELSE 'not_executed' END)
 WHERE deferred_task AND status='pending' AND approval_expires_at<=now() RETURNING task_id)
 UPDATE task_approval_holds SET outcome='timeout' WHERE ended_at IS NULL AND task_id::text IN(SELECT task_id FROM expired)`)
	return err
}
