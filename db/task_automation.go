package db

import (
	"encoding/json"
	"fmt"
)

// TaskAutomation says which code-review steps run on their own for one task.
// Both default to off: the operator chooses which findings get an independent
// verification and which get a fix, unless the task's switch says otherwise.
type TaskAutomation struct {
	Verify   bool `json:"verify"`   // verify each new finding as it is recorded
	Mitigate bool `json:"mitigate"` // write a fix for each finding a verification confirms
}

// TaskAutomationKey is the settings key holding a task's automation switches.
func TaskAutomationKey(taskID int64) string { return fmt.Sprintf("task_automation.%d", taskID) }

// GetTaskAutomation reads a task's switches; a task never set is fully manual.
func (d *DB) GetTaskAutomation(taskID int64) (TaskAutomation, error) {
	var a TaskAutomation
	raw, ok, err := d.GetSetting(TaskAutomationKey(taskID))
	if err != nil || !ok || raw == "" {
		return a, err
	}
	return a, json.Unmarshal([]byte(raw), &a)
}

func (d *DB) SetTaskAutomation(taskID int64, a TaskAutomation) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return d.SetSetting(TaskAutomationKey(taskID), string(raw))
}
