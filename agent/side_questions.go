package agent

import (
	"context"
	"strconv"
	"strings"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Philocyber/counterweight/sidequestion"
)

// sideParentFor names the session a run's side-question checkpoints belong to.
func sideParentFor(ri RunInfo) sidequestion.Parent {
	p := sidequestion.Parent{TaskID: ri.TaskID, ExplorationID: ri.ExplorationID, IntentID: ri.IntentID}
	if strings.HasPrefix(ri.SessionID, "conv-") {
		p.ConversationID, _ = strconv.ParseInt(strings.TrimPrefix(ri.SessionID, "conv-"), 10, 64)
	}
	if p.ConversationID > 0 {
		// A conversation session is stored under its conversation alone, also when it
		// runs with a task's identity; a parent carrying both is rejected by the table.
		p.TaskID, p.ExplorationID, p.IntentID = 0, 0, 0
	}
	return p
}

func attachSideCapture(ctx context.Context, opts *agentcore.Options) context.Context {
	p := sideParentFor(RunInfoFrom(ctx))
	if p.ConversationID == 0 && (p.TaskID == 0 || p.ExplorationID == 0) {
		return ctx
	}
	ctx, opts.Deps = sidequestion.Attach(ctx, p, opts.Deps, opts.Provider)
	return ctx
}
