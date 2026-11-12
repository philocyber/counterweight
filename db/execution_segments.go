package db

import (
	"github.com/Philocyber/counterweight/execution"
	"time"
)

type ExecutionSegment struct {
	ID           int64                  `json:"id"`
	TaskID       int64                  `json:"task_id"`
	StartedAt    time.Time              `json:"started_at"`
	Mode         string                 `json:"mode"`
	Capabilities execution.Capabilities `json:"capabilities"`
	Version      string                 `json:"version"`
}

func (d *DB) AppendExecutionSegment(taskID int64, mode, version string) error {
	c := execution.Caps(mode)
	_, err := d.Exec(`INSERT INTO task_execution_segments(task_id,mode,files_confined,processes_confined,network_confined,app_version) VALUES($1,$2,$3,$4,$5,$6)`, taskID, mode, c.FilesConfined, c.ProcessesConfined, c.NetworkConfined, version)
	return err
}
func (d *DB) ExecutionInfo(taskID int64) (execution.Info, error) {
	info := execution.Info{Mode: execution.Host, Assumed: true}
	rows, err := d.Query(`SELECT mode,files_confined,processes_confined,network_confined FROM task_execution_segments WHERE task_id=$1 ORDER BY started_at,id`, taskID)
	if err != nil {
		return info, err
	}
	defer rows.Close()
	first := ""
	for rows.Next() {
		if err = rows.Scan(&info.Mode, &info.Capabilities.FilesConfined, &info.Capabilities.ProcessesConfined, &info.Capabilities.NetworkConfined); err != nil {
			return info, err
		}
		if first == "" {
			first = info.Mode
		}
		if first != info.Mode {
			info.Mixed = true
		}
		info.Assumed = false
	}
	return info, rows.Err()
}
func (s *ExplorationStore) ExecutionInfo(id int64) (execution.Info, error) {
	return s.db.ExecutionInfo(id)
}

// HoldExecution reuses the persistent pause/hold channel displayed by task APIs.
func (d *DB) HoldExecution(taskID int64, reason string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE tasks SET paused=true,queued=false,queued_at=NULL WHERE id=$1`, taskID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO task_approval_holds(task_id,outcome,reason) VALUES($1,'execution',$2) ON CONFLICT(task_id) WHERE ended_at IS NULL DO UPDATE SET reason=CASE WHEN position(EXCLUDED.reason IN task_approval_holds.reason)>0 THEN task_approval_holds.reason ELSE concat_ws(' ',NULLIF(task_approval_holds.reason,''),EXCLUDED.reason) END`, taskID, reason); err != nil {
		return err
	}
	return tx.Commit()
}
func (d *DB) ClearExecutionHold(taskID int64) error {
	_, err := d.Exec(`UPDATE task_approval_holds SET ended_at=now(),outcome='execution_resumed' WHERE task_id=$1 AND ended_at IS NULL AND outcome='execution'`, taskID)
	return err
}
