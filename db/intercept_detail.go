package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// InterceptContextEntry is a bounded, recorded session event, not model reasoning.
type InterceptContextEntry struct {
	Kind      string `json:"kind"`
	Tool      string `json:"tool,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Text      string `json:"text"`
	IsError   bool   `json:"is_error,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// InterceptAudit is captured at review time. It is deliberately excluded from
// polling/list responses; old rows have no audit instead of reconstructed data.
type InterceptAudit struct {
	RunID            string                  `json:"run_id,omitempty"`
	ToolUseID        string                  `json:"tool_use_id,omitempty"`
	Correlation      string                  `json:"correlation"` // exact | ambiguous | unavailable
	InputDigest      string                  `json:"input_digest"`
	UserMessage      string                  `json:"user_message"`
	UserTruncated    bool                    `json:"user_truncated,omitempty"`
	Context          []InterceptContextEntry `json:"context"`
	ContextTruncated bool                    `json:"context_truncated,omitempty"`
	CapturedAt       time.Time               `json:"captured_at"`
	ModelFallback    bool                    `json:"model_fallback,omitempty"`
	ModelInput       json.RawMessage         `json:"model_input,omitempty"`
	ModelInputDigest string                  `json:"model_input_digest,omitempty"`
	InitialAction    string                  `json:"initial_action"`
	InitialReason    string                  `json:"initial_reason"`
	EffectiveAction  string                  `json:"effective_action,omitempty"`
	DecisionReason   string                  `json:"decision_reason,omitempty"`
	RuleName         string                  `json:"rule_name,omitempty"`
	GrantScope       string                  `json:"grant_scope,omitempty"`
	ConfigDigest     string                  `json:"config_digest,omitempty"`
	ProfileID        int64                   `json:"profile_id,omitempty"`
	ExecutionStatus  string                  `json:"execution_status"`
	Output           string                  `json:"output,omitempty"`
	OutputTruncated  bool                    `json:"output_truncated,omitempty"`
	ExecutionEndedAt *time.Time              `json:"execution_ended_at,omitempty"`
}

type InterceptDetail struct {
	InterceptApprovalRow
	Audit *InterceptAudit `json:"audit"`
}

func (d *DB) GetInterceptDetail(id int64) (*InterceptDetail, error) {
	var out InterceptDetail
	var raw []byte
	err := d.QueryRow(approvalRowSelectWithAudit+` WHERE ip.id=$1`, id).Scan(
		&out.ID, &out.RuleID, &out.ConversationID, &out.TaskID, &out.AgentName,
		&out.ToolName, &out.ToolInput, &out.Status, &out.Reason, &out.DecidedAt, &out.CreatedAt,
		&out.DecisionSource, &out.ConvTitle, &out.ConvAgentKey, &out.RuleName, &raw,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.Audit); err != nil {
			return nil, err
		}
	}
	return &out, nil
}

// ResolveIntercept atomically settles a pending request. A timeout cannot
// overwrite a human decision and repeat decisions cannot rewrite history.
func (d *DB) ResolveIntercept(id int64, status, action, reason string, grants ...*TaskToolGrant) (bool, error) {
	execution := "not_executed"
	if action == "allow" {
		execution = "awaiting_result"
	}
	fields := map[string]any{
		"effective_action": action, "decision_reason": reason, "execution_status": execution,
	}
	if action == "allow" && status == "allowed" {
		fields["grant_scope"] = "once"
		if len(grants) > 0 && grants[0] != nil && grants[0].Scope != "" {
			fields["grant_scope"] = grants[0].Scope
		}
	}
	patch, err := json.Marshal(fields)
	if err != nil {
		return false, err
	}
	tx, err := d.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`UPDATE intercept_pending SET status=$2, reason=$5, decided_at=NOW(),
		audit=CASE WHEN audit IS NULL THEN NULL ELSE audit || $3::jsonb ||
        CASE WHEN $4='allow' AND audit->>'correlation' IS DISTINCT FROM 'exact'
        THEN '{"execution_status":"unknown"}'::jsonb ELSE '{}'::jsonb END END
        WHERE id=$1 AND status='pending'`, id, status, patch, action, reason)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	if len(grants) > 0 && grants[0] != nil {
		g := grants[0]
		if action != "allow" {
			return false, fmt.Errorf("standing approvals require an allow decision")
		}
		_, err = tx.Exec(`INSERT INTO task_tool_grants(task_id,scope,tool_name,command)
    VALUES ($1,$2,$3,$4) ON CONFLICT (task_id,scope,tool_name,command) DO NOTHING`, g.TaskID, g.Scope, g.ToolName, g.Command)
		if err != nil {
			return false, err
		}
	}
	if action == "allow" {
		_, err = tx.Exec(`INSERT INTO task_approval_replays(pending_id,task_id,tool_name,tool_input)
   SELECT p.id,t.id,p.tool_name,p.tool_input FROM intercept_pending p JOIN tasks t ON p.task_id=t.id::text
   WHERE p.id=$1 AND p.deferred_task AND EXISTS(SELECT 1 FROM task_approval_holds WHERE task_id=t.id AND ended_at IS NULL)
   ON CONFLICT(pending_id) DO NOTHING`, id)
		if err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// CompleteIntercept only updates the exact recorded call after it was allowed.
// A blocked tool_result must never be presented as a failed execution.
func (d *DB) CompleteIntercept(id int64, runID, toolUseID, status, output string, truncated bool) error {
	patch, err := json.Marshal(map[string]any{
		"execution_status": status, "output": output, "output_truncated": truncated,
		"execution_ended_at": time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE intercept_pending SET audit=audit || $4::jsonb
		WHERE id=$1 AND audit->>'run_id'=$2 AND audit->>'tool_use_id'=$3
		AND audit->>'effective_action'='allow' AND audit->>'execution_status'='awaiting_result'`,
		id, runID, toolUseID, patch)
	return err
}
