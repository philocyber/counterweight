package db

import "database/sql"

// TaskExecution reads the persisted type and repository target for a run.
// Legacy isolated explorations have no task row and retain general execution.
// Registry-backed explorations must match the requested task; read errors and
// deleted tasks must never select a less restricted tool set.
func (s *ExplorationStore) TaskExecution(taskID int64) (*Task, error) {
	if s == nil {
		return &Task{AssessmentType: AssessmentGeneral}, nil
	}
	var ownerID int64
	err := s.db.QueryRow(`SELECT id FROM tasks WHERE exploration_id=$1`, s.expID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		var exists bool
		if err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM explorations WHERE id=$1)`, s.expID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, sql.ErrNoRows
		}
		return &Task{AssessmentType: AssessmentGeneral}, nil
	}
	if err != nil {
		return nil, err
	}
	if taskID > 0 && taskID != ownerID {
		return nil, sql.ErrNoRows
	}
	task, err := s.db.GetTask(ownerID)
	if task == nil && err == nil {
		err = sql.ErrNoRows
	}
	return task, err
}
