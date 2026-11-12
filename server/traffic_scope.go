package server

import (
	"context"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/traffic"
)

// trafficCallerTasks names the tasks an agent run acts for, from the server's
// own records: the run identity, the conversation's recorded task binding or
// the task that owns the finding a finding-run works on. An operator's own
// chat is bound to no task and keeps its reach over the capture store. A run
// that belongs to a task whose identity cannot be read is refused.
func trafficCallerTasks(pg *db.DB) traffic.TaskFromContext {
	return func(ctx context.Context) ([]int64, bool, error) {
		run := agent.RunInfoFrom(ctx)
		if run.TaskID > 0 {
			return []int64{run.TaskID}, true, nil
		}
		if run.AssessmentType != "" || run.ExplorationID != 0 {
			return nil, true, nil
		}
		conversation := intercept.ConvIDFromContext(ctx)
		if conversation <= 0 {
			return nil, false, nil
		}
		ids, err := pg.ConversationTasks(conversation)
		if err != nil || len(ids) > 0 {
			return ids, true, err
		}
		retest, err := pg.FindingRetestForConversation(ctx, conversation)
		if err != nil || retest == nil {
			return nil, err != nil, err
		}
		finding, err := pg.GetFinding(retest.FindingID)
		if err != nil || finding == nil || finding.TaskID == nil {
			return nil, true, err
		}
		return []int64{*finding.TaskID}, true, nil
	}
}
