package db

import "time"

// TaskToolGrant is a standing approval for one task.
// Scope is "input" (complete exact arguments), "command" (legacy shell text),
// "tool" (one tool name), or "all".
type TaskToolGrant struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"task_id"`
	Scope     string    `json:"scope"`
	ToolName  string    `json:"tool_name"`
	Command   string    `json:"command"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTaskToolGrants returns grants for one task, or every task when taskID is empty.
func (d *DB) ListTaskToolGrants(taskID string) ([]TaskToolGrant, error) {
	rows, err := d.Query(`
SELECT id, task_id, scope, tool_name, command, created_at
FROM task_tool_grants
WHERE ($1='' OR task_id=$1)
ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskToolGrant
	for rows.Next() {
		var g TaskToolGrant
		if err := rows.Scan(&g.ID, &g.TaskID, &g.Scope, &g.ToolName, &g.Command, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveTaskToolGrant inserts a grant. Repeating the same grant returns the existing row.
func (d *DB) SaveTaskToolGrant(taskID, scope, toolName, command string) (TaskToolGrant, error) {
	if _, err := d.Exec(`
INSERT INTO task_tool_grants(task_id, scope, tool_name, command)
VALUES ($1, $2, $3, $4)
ON CONFLICT (task_id, scope, tool_name, command) DO NOTHING`, taskID, scope, toolName, command); err != nil {
		return TaskToolGrant{}, err
	}
	var g TaskToolGrant
	err := d.QueryRow(`
SELECT id, task_id, scope, tool_name, command, created_at
FROM task_tool_grants
WHERE task_id=$1 AND scope=$2 AND tool_name=$3 AND command=$4`,
		taskID, scope, toolName, command).Scan(&g.ID, &g.TaskID, &g.Scope, &g.ToolName, &g.Command, &g.CreatedAt)
	return g, err
}

// DeleteTaskToolGrant removes one grant belonging to the task. Missing rows report false.
func (d *DB) DeleteTaskToolGrant(taskID string, id int64) (bool, error) {
	res, err := d.Exec(`DELETE FROM task_tool_grants WHERE id=$1 AND task_id=$2`, id, taskID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
