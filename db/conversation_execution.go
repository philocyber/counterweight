package db

import (
	"database/sql"
	"encoding/json"
)

// BindConversationTasks records trusted trigger principals, never prompt text.
func (d *DB) BindConversationTasks(conversationID int64, taskIDs []int64) error {
	ids := make([]int64, 0, len(taskIDs))
	for _, id := range taskIDs {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = d.Exec(`INSERT INTO conversation_task_execution(conversation_id,task_ids) VALUES ($1,$2)`, conversationID, raw)
	return err
}

// ConversationTasks returns persisted execution identity for resumed turns.
func (d *DB) ConversationTasks(conversationID int64) ([]int64, error) {
	var raw []byte
	err := d.QueryRow(`SELECT task_ids FROM conversation_task_execution WHERE conversation_id=$1`, conversationID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, err
	}
	return ids, nil
}
