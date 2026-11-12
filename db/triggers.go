package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// AgentTrigger is one P3 trigger attached to a custom agent. Six trigger
// conditions can be on at once: interval / on_finding / on_goal_met /
// on_task_timeout / on_tool_call / on_task_create. ToolNames scopes the tool-call
// trigger to a non-empty set of tool keys (empty is rejected at the API layer for
// on_tool_call).
type AgentTrigger struct {
	ID                 int64      `json:"id"`
	AgentKey           string     `json:"agent_key"`
	Enabled            bool       `json:"enabled"`
	IntervalSec        int        `json:"interval_sec"`
	OnFinding          bool       `json:"on_finding"`
	OnGoalMet          bool       `json:"on_goal_met"`
	OnTaskTimeout      bool       `json:"on_task_timeout"`
	OnToolCall         bool       `json:"on_tool_call"`
	OnTaskCreate       bool       `json:"on_task_create"`
	IntervalMessage    string     `json:"interval_message"` // Separate user messages for each trigger condition.
	FindingMessage     string     `json:"finding_message"`
	GoalMessage        string     `json:"goal_message"`
	TaskTimeoutMessage string     `json:"task_timeout_message"`
	ToolCallMessage    string     `json:"tool_call_message"`
	TaskCreateMessage  string     `json:"task_create_message"`
	ToolNames          []string   `json:"tool_names"` // Selected tool keys, stored as JSON text.
	LastFire           *time.Time `json:"last_fire,omitempty"`
}

const triggerCols = `id, agent_key, enabled, interval_sec, on_finding, on_goal_met, on_task_timeout, on_tool_call, on_task_create, interval_message, finding_message, goal_message, task_timeout_message, tool_call_message, task_create_message, tool_names, last_fire`

// marshalToolNames encodes the tool-key list as JSON text for the tool_names column.
// A nil/empty list stores "" (not "null"/"[]") so the column default stays clean.
func marshalToolNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	b, err := json.Marshal(names)
	if err != nil {
		return ""
	}
	return string(b)
}

func scanTrigger(sc interface{ Scan(...any) error }) (*AgentTrigger, error) {
	var t AgentTrigger
	var lf sql.NullTime
	var toolNames string
	if err := sc.Scan(&t.ID, &t.AgentKey, &t.Enabled, &t.IntervalSec, &t.OnFinding, &t.OnGoalMet, &t.OnTaskTimeout, &t.OnToolCall, &t.OnTaskCreate,
		&t.IntervalMessage, &t.FindingMessage, &t.GoalMessage, &t.TaskTimeoutMessage, &t.ToolCallMessage, &t.TaskCreateMessage, &toolNames, &lf); err != nil {
		return nil, err
	}
	t.ToolNames = []string{}
	if toolNames != "" {
		_ = json.Unmarshal([]byte(toolNames), &t.ToolNames)
	}
	if lf.Valid {
		t.LastFire = &lf.Time
	}
	return &t, nil
}

// CreateTrigger inserts a trigger for agentKey and returns it.
func (d *DB) CreateTrigger(t *AgentTrigger) (*AgentTrigger, error) {
	row := d.QueryRow(`
INSERT INTO agent_triggers(agent_key, enabled, interval_sec, on_finding, on_goal_met, on_task_timeout, on_tool_call, on_task_create, interval_message, finding_message, goal_message, task_timeout_message, tool_call_message, task_create_message, tool_names)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING `+triggerCols,
		t.AgentKey, t.Enabled, t.IntervalSec, t.OnFinding, t.OnGoalMet, t.OnTaskTimeout, t.OnToolCall, t.OnTaskCreate,
		t.IntervalMessage, t.FindingMessage, t.GoalMessage, t.TaskTimeoutMessage, t.ToolCallMessage, t.TaskCreateMessage, marshalToolNames(t.ToolNames))
	return scanTrigger(row)
}

// UpdateTrigger updates a trigger's fields (not last_fire).
func (d *DB) UpdateTrigger(t *AgentTrigger) error {
	_, err := d.Exec(`UPDATE agent_triggers SET enabled=$2, interval_sec=$3, on_finding=$4, on_goal_met=$5, on_task_timeout=$6, on_tool_call=$7, on_task_create=$8, interval_message=$9, finding_message=$10, goal_message=$11, task_timeout_message=$12, tool_call_message=$13, task_create_message=$14, tool_names=$15 WHERE id=$1`,
		t.ID, t.Enabled, t.IntervalSec, t.OnFinding, t.OnGoalMet, t.OnTaskTimeout, t.OnToolCall, t.OnTaskCreate,
		t.IntervalMessage, t.FindingMessage, t.GoalMessage, t.TaskTimeoutMessage, t.ToolCallMessage, t.TaskCreateMessage, marshalToolNames(t.ToolNames))
	return err
}

// DeleteTrigger removes a trigger.
func (d *DB) DeleteTrigger(id int64) error {
	_, err := d.Exec(`DELETE FROM agent_triggers WHERE id=$1`, id)
	return err
}

// ListTriggersFor returns an agent's triggers.
func (d *DB) ListTriggersFor(agentKey string) ([]*AgentTrigger, error) {
	return d.queryTriggers(`SELECT `+triggerCols+` FROM agent_triggers WHERE agent_key=$1 ORDER BY id`, agentKey)
}

// ListEnabledTriggers returns all enabled triggers (for the scheduler).
func (d *DB) ListEnabledTriggers() ([]*AgentTrigger, error) {
	return d.queryTriggers(`SELECT ` + triggerCols + ` FROM agent_triggers WHERE enabled ORDER BY id`)
}

func (d *DB) queryTriggers(q string, args ...any) ([]*AgentTrigger, error) {
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AgentTrigger{}
	for rows.Next() {
		t, err := scanTrigger(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TouchTriggerFire records an interval trigger's fire time (now).
func (d *DB) TouchTriggerFire(id int64) error {
	_, err := d.Exec(`UPDATE agent_triggers SET last_fire=now() WHERE id=$1`, id)
	return err
}

// DeleteTriggersForAgent removes all triggers of an agent (custom agent delete).
func (d *DB) DeleteTriggersForAgent(agentKey string) error {
	_, err := d.Exec(`DELETE FROM agent_triggers WHERE agent_key=$1`, agentKey)
	return err
}

// ---------- scheduler_state (kv watermarks) ----------

func (d *DB) GetSchedState(key string) (string, error) {
	var v string
	err := d.QueryRow(`SELECT value FROM scheduler_state WHERE key=$1`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (d *DB) SetSchedState(key, value string) error {
	_, err := d.Exec(`INSERT INTO scheduler_state(key,value) VALUES ($1,$2)
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, key, value)
	return err
}

// ---------- event queries (cross-exploration, for the scheduler) ----------

// TaskEvent is a finding/goal event carrying the owning task's info, used to
// compose the trigger message context.
type TaskEvent struct {
	NodeID     int64  `json:"node_id"`
	TaskID     int64  `json:"task_id"`
	TaskDesc   string `json:"task_description"`
	TaskGoal   string `json:"task_goal"`
	Summary    string `json:"summary"`     // finding summary / goal text
	VulnClass  string `json:"vulnclass"`   // finding only
	Severity   string `json:"severity"`    // finding only
	Tool       string `json:"tool"`        // tool-call only: tool name
	ToolInput  string `json:"tool_input"`  // Tool-call only: arguments as JSON text.
	ToolOutput string `json:"tool_output"` // Tool-call only: returned content.
	ToolIsErr  bool   `json:"tool_is_err"` // Tool-call only: whether the tool result is an error.
	// ToolInputLen and ToolOutputLen are the full character counts of the
	// (possibly truncated) tool-call input and output.
	ToolInputLen, ToolOutputLen int `json:"-"`
	// CreatedAt is set for task-create events; with TaskID it forms the cursor.
	CreatedAt time.Time `json:"-"`
}

// TaskCursor is the task-create watermark. Task ids are reused after deletion,
// so (created_at, id) orders creations; an id alone would skip reused ids.
type TaskCursor struct {
	CreatedAt time.Time
	ID        int64
}

// String encodes the cursor for scheduler_state.
func (c TaskCursor) String() string {
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(c.ID, 10)
}

// ParseTaskCursor decodes String output; ok is false for empty or legacy values.
func ParseTaskCursor(raw string) (TaskCursor, bool) {
	ts, id, found := strings.Cut(raw, "|")
	if !found {
		return TaskCursor{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return TaskCursor{}, false
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return TaskCursor{}, false
	}
	return TaskCursor{CreatedAt: t, ID: n}, true
}

// NewFindingsSince returns findings with node id > lastID across all live tasks,
// ordered by id (monotonic watermark → no double-fire).
func (d *DB) NewFindingsSince(lastID int64) ([]TaskEvent, error) {
	rows, err := d.Query(`
SELECT n.id, t.id, t.description, t.goal, n.payload
FROM exploration_nodes n JOIN tasks t ON t.exploration_id = n.exploration_id
WHERE n.kind='finding' AND n.id > $1 AND t.deleted_at IS NULL
ORDER BY n.id`, lastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskEvent{}
	for rows.Next() {
		var e TaskEvent
		var payload []byte
		if err := rows.Scan(&e.NodeID, &e.TaskID, &e.TaskDesc, &e.TaskGoal, &payload); err != nil {
			return nil, err
		}
		var p struct{ Summary, Vulnclass, Severity string }
		_ = json.Unmarshal(payload, &p)
		e.Summary, e.VulnClass, e.Severity = p.Summary, p.Vulnclass, p.Severity
		out = append(out, e)
	}
	return out, rows.Err()
}

// TimedOutTasks returns every live task currently in status='timeout'. Tasks time
// out in any order and ids are reused, so the scheduler tracks which ones it has
// already fired (keyed by TaskCursor) instead of an id watermark.
func (d *DB) TimedOutTasks() ([]TaskEvent, error) {
	rows, err := d.Query(`
SELECT id, description, goal, created_at FROM tasks
WHERE status='timeout' AND deleted_at IS NULL
ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskEvent{}
	for rows.Next() {
		var e TaskEvent
		if err := rows.Scan(&e.NodeID, &e.TaskDesc, &e.TaskGoal, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.TaskID = e.NodeID
		e.Summary = e.TaskGoal
		out = append(out, e)
	}
	return out, rows.Err()
}

// NewTasksSince returns tasks created after the cursor (excluding deleted),
// ordered by (created_at, id) so reused task ids still fire exactly once.
// Triggered agent runs are conversations, not tasks, so this never fires on its
// own output.
func (d *DB) NewTasksSince(after TaskCursor) ([]TaskEvent, error) {
	rows, err := d.Query(`
SELECT id, description, goal, created_at FROM tasks
WHERE deleted_at IS NULL AND (created_at, id) > ($1, $2)
ORDER BY created_at, id`, after.CreatedAt, after.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskEvent{}
	for rows.Next() {
		var e TaskEvent
		if err := rows.Scan(&e.NodeID, &e.TaskDesc, &e.TaskGoal, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.TaskID = e.NodeID
		e.Summary = e.TaskGoal
		out = append(out, e)
	}
	return out, rows.Err()
}

// NewToolCallsSince returns up to limit completed tool calls (a tool_result row)
// with activity id > lastID across all live tasks, ordered by id (monotonic
// watermark → no double-fire); callers page by passing the last id they saw. It is
// driven by tool_result rows (the tool finished, so both input and output are
// available) and joins back to the paired tool_use row for the input. ToolInput and
// ToolOutput are cut to detailMax characters in SQL; ToolInputLen and ToolOutputLen
// carry the full lengths. Only task-execution activity is scanned: triggered agent
// runs are conversations (conversation_activities), so a tool-call trigger never
// fires on its own output.
func (d *DB) NewToolCallsSince(lastID int64, limit, detailMax int) ([]TaskEvent, error) {
	rows, err := d.Query(`
SELECT r.id, t.id, t.description, t.goal, r.tool,
       LEFT(COALESCE(u.detail,''), $3), char_length(COALESCE(u.detail,'')),
       LEFT(COALESCE(r.detail,''), $3), char_length(COALESCE(r.detail,'')), r.is_error
FROM activity r
JOIN tasks t ON t.exploration_id = r.exploration_id
LEFT JOIN activity u ON u.exploration_id = r.exploration_id AND u.tool_use_id = r.tool_use_id AND u.kind='tool_use'
WHERE r.kind='tool_result' AND r.id > $1 AND r.tool <> '' AND t.deleted_at IS NULL
ORDER BY r.id
LIMIT $2`, lastID, limit, detailMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskEvent{}
	for rows.Next() {
		var e TaskEvent
		if err := rows.Scan(&e.NodeID, &e.TaskID, &e.TaskDesc, &e.TaskGoal, &e.Tool, &e.ToolInput, &e.ToolInputLen, &e.ToolOutput, &e.ToolOutputLen, &e.ToolIsErr); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MaxFindingID returns the highest finding node id across live tasks (0 if none),
// the same stream NewFindingsSince reads.
func (d *DB) MaxFindingID() (int64, error) {
	var id int64
	err := d.QueryRow(`
SELECT COALESCE(max(n.id),0)
FROM exploration_nodes n JOIN tasks t ON t.exploration_id = n.exploration_id
WHERE n.kind='finding' AND t.deleted_at IS NULL`).Scan(&id)
	return id, err
}

// MaxToolCallID returns the highest tool_result activity id across live tasks (0
// if none), the same stream NewToolCallsSince reads.
func (d *DB) MaxToolCallID() (int64, error) {
	var id int64
	err := d.QueryRow(`
SELECT COALESCE(max(r.id),0)
FROM activity r JOIN tasks t ON t.exploration_id = r.exploration_id
WHERE r.kind='tool_result' AND r.tool <> '' AND t.deleted_at IS NULL`).Scan(&id)
	return id, err
}

// NewestTaskCursor returns the (created_at, id) of the newest live task, or the
// zero cursor when there is none: the end of the stream NewTasksSince reads.
func (d *DB) NewestTaskCursor() (TaskCursor, error) {
	var c TaskCursor
	err := d.QueryRow(`
SELECT created_at, id FROM tasks WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&c.CreatedAt, &c.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskCursor{}, nil
	}
	return c, err
}

// MetGoalIDs returns the node ids of every met goal across live tasks, the ids
// MetGoals reads without loading the rows.
func (d *DB) MetGoalIDs() ([]int64, error) {
	rows, err := d.Query(`
SELECT n.id FROM exploration_nodes n JOIN tasks t ON t.exploration_id = n.exploration_id
WHERE n.kind='goal' AND n.state='met' AND t.deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MetGoals returns all met goals across live tasks (the scheduler filters out the
// ones it already fired for via the persisted fired-set).
func (d *DB) MetGoals() ([]TaskEvent, error) {
	rows, err := d.Query(`
SELECT n.id, t.id, t.description, t.goal, n.payload
FROM exploration_nodes n JOIN tasks t ON t.exploration_id = n.exploration_id
WHERE n.kind='goal' AND n.state='met' AND t.deleted_at IS NULL
ORDER BY n.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskEvent{}
	for rows.Next() {
		var e TaskEvent
		var payload []byte
		if err := rows.Scan(&e.NodeID, &e.TaskID, &e.TaskDesc, &e.TaskGoal, &payload); err != nil {
			return nil, err
		}
		var p struct{ Text, Summary string }
		_ = json.Unmarshal(payload, &p)
		if p.Text != "" {
			e.Summary = p.Text
		} else {
			e.Summary = p.Summary
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
