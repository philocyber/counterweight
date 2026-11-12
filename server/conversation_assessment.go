package server

import (
	"context"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// Triggered conversations act inside their source tasks, including merged runs.
// Re-read persisted types every turn. Missing task identity fails closed.
func (s *Server) conversationAssessmentContext(ctx context.Context, conversationID int64) (context.Context, error) {
	ids, err := s.m.pg.ConversationTasks(conversationID)
	if err != nil {
		return ctx, err
	}
	for _, id := range ids {
		task, err := s.m.pg.GetTask(id)
		if err != nil {
			return ctx, err
		}
		if task == nil {
			return ctx, locale.NewError("Conversation task execution identity is unavailable")
		}
		if task.AssessmentType == db.AssessmentCodeReview {
			ri := agent.RunInfoFrom(ctx)
			ri.AssessmentType = task.AssessmentType
			ri.TaskID = task.ID
			ri.ExplorationID = task.ExplorationID
			ctx = agent.WithRunInfo(ctx, ri)
		}
	}
	return ctx, nil
}

// Only built-in observation, steering and report-record tools are constructed.
// Custom tools, MCP clients and traffic tools are never assembled on this path.
func (s *Server) reviewConversationTools(ctx context.Context) []actool.CoreTool {
	lang := locale.FromContext(ctx)
	return []actool.CoreTool{
		s.toolGetTaskGraph(lang), s.toolListTaskFindings(lang), s.toolGetTaskNodeDetail(lang),
		s.toolGetWorkerTrace(lang), s.toolListWorkerTraces(lang), s.toolSearchWorkerTraces(lang),
		s.toolAddHint(lang), s.toolPauseTask(lang), s.toolUpdateFindingReport(lang),
	}
}
