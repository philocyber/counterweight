package report

import "github.com/Philocyber/counterweight/execution"

// ExecutionLookup is wired by the server for task exports. Standalone renderers
// can supply Input.Isolation directly and need no database connection.
var ExecutionLookup func(taskID string) execution.Info

func (in Input) ExecutionIsolation() execution.Info {
	if in.Isolation.Mode != "" {
		return in.Isolation
	}
	if ExecutionLookup != nil && in.TaskID != "" {
		return ExecutionLookup(in.TaskID)
	}
	return execution.Info{Mode: execution.Host, Assumed: true}
}
