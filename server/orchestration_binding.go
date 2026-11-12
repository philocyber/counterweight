package server

import (
	"context"
	"strconv"
	"strings"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
)

// authorizeTaskReach limits the cross-task tools of a run that the server bound to tasks
// (the conversation's recorded task binding, or the run identity's task). Such a run may
// reach its bound tasks and, for reading only, the sources those tasks inherit from. A run
// with no binding, such as the operator's interactive assistant, keeps its full reach.
// The decision comes only from server-side state, never from ids in prompt text.
func (s *Server) authorizeTaskReach(ctx context.Context, taskID int64, write bool) error {
	bound, err := s.runTaskBinding(ctx)
	if err != nil {
		return err
	}
	if !s.taskWithinBinding(bound, taskID, write) {
		return locale.NewError("This run is not bound to that task")
	}
	return nil
}

// runTaskBinding lists the tasks the server bound the run to. It is empty for a run with
// no binding.
func (s *Server) runTaskBinding(ctx context.Context) (map[int64]bool, error) {
	bound := map[int64]bool{}
	if id := agent.RunInfoFrom(ctx).TaskID; id > 0 {
		bound[id] = true
	}
	if conv := intercept.ConvIDFromContext(ctx); conv > 0 {
		ids, err := s.m.pg.ConversationTasks(conv)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			bound[id] = true
		}
	}
	return bound, nil
}

// taskWithinBinding reports whether a run with the given binding may reach the task.
func (s *Server) taskWithinBinding(bound map[int64]bool, taskID int64, write bool) bool {
	if len(bound) == 0 || bound[taskID] {
		return true
	}
	if !write {
		for id := range bound {
			t, ok := s.m.Task(strconv.FormatInt(id, 10))
			if !ok {
				continue
			}
			for _, source := range t.lifecycleSnapshot().SourceTaskIDs {
				if source == taskID {
					return true
				}
			}
		}
	}
	return false
}

// boundTaskID parses a model-supplied task id into its canonical number.
func boundTaskID(raw string) (int64, bool) {
	canonical, ok := canonicalTaskID(strings.TrimSpace(raw))
	if !ok {
		return 0, false
	}
	n, _ := strconv.ParseInt(canonical, 10, 64)
	return n, true
}
