package traffic

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// TaskFromContext identifies the calling run from server-side state, never
// from model input. bound reports whether the caller is a task-bound agent run;
// an unbound caller (an operator's interactive session) is not restricted. A
// run started for several tasks acts for each of them. An error, or bound
// without a positive id, means a task-bound run whose identity cannot be
// resolved, and every read is then refused.
type TaskFromContext func(ctx context.Context) (taskIDs []int64, bound bool, err error)

// ScopeSource returns the host filter for one task, or an error when the
// task's scope cannot be established.
type ScopeSource func(taskID int64) (allow func(host string) bool, err error)

// AssetScopeSource resolves task scopes from the asset store's operator grants.
func AssetScopeSource(assets *db.AssetStore) ScopeSource {
	return func(taskID int64) (func(string) bool, error) {
		if assets == nil {
			return nil, locale.NewError("Captured traffic is unavailable because the task scope could not be resolved")
		}
		scope, err := assets.TrafficHostScopeForTask(taskID)
		if err != nil {
			return nil, err
		}
		return scope.Allows, nil
	}
}

type scopeBinding struct {
	source ScopeSource
	task   TaskFromContext
}

type taskScopeKey struct{}

// WithTaskScope marks ctx as acting for one task, whatever its run identity
// says. Callers that only ever act for an agent (recording a finding) use it.
func WithTaskScope(ctx context.Context, taskID int64) context.Context {
	return context.WithValue(ctx, taskScopeKey{}, taskID)
}

// BindAgentScope confines task-bound callers of the agent traffic tools and of
// evidence preparation to the hosts in their task's operator-authorized scope
// (see db.TrafficHostScope). Without it nothing is confined.
func (t *Traffic) BindAgentScope(source ScopeSource, task TaskFromContext) {
	if t == nil {
		return
	}
	t.scope.Store(&scopeBinding{source: source, task: task})
}

// callerScope returns the host filter that applies to ctx: nil means the caller
// is unrestricted. It fails closed when the scope cannot be established.
func (t *Traffic) callerScope(ctx context.Context) (func(host string) bool, error) {
	if t == nil {
		return nil, nil
	}
	b := t.scope.Load()
	if b == nil {
		return nil, nil
	}
	var taskIDs []int64
	if forced, ok := ctx.Value(taskScopeKey{}).(int64); ok {
		taskIDs = []int64{forced}
	} else {
		ids, bound, err := b.task(ctx)
		if err != nil {
			return nil, locale.NewError("Captured traffic is unavailable because the calling task could not be resolved")
		}
		if !bound {
			return nil, nil
		}
		taskIDs = ids
	}
	if len(taskIDs) == 0 || b.source == nil {
		return nil, locale.NewError("Captured traffic is unavailable because the calling task could not be resolved")
	}
	var scopes []func(string) bool
	for _, taskID := range taskIDs {
		if taskID <= 0 {
			return nil, locale.NewError("Captured traffic is unavailable because the calling task could not be resolved")
		}
		allow, err := b.source(taskID)
		if err != nil || allow == nil {
			return nil, locale.NewError("Captured traffic is unavailable because the task scope could not be resolved")
		}
		scopes = append(scopes, allow)
	}
	return func(host string) bool {
		for _, allow := range scopes {
			if allow(host) {
				return true
			}
		}
		return false
	}, nil
}

// exchangeAllowed reports whether the exchange exists and its host passes allow.
func (t *Traffic) exchangeAllowed(id string, allow func(string) bool) (bool, error) {
	var host string
	err := t.db.QueryRow(`SELECT host FROM exchanges WHERE id=?`, id).Scan(&host)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return allow(host), nil
}

// blobAllowed reports whether any exchange that references the stored body has
// a host that passes allow.
func (t *Traffic) blobAllowed(hash string, allow func(string) bool) (bool, error) {
	rows, err := t.db.Query(`SELECT e.host FROM blob_refs r JOIN exchanges e ON e.id=r.exchange_id WHERE r.hash=?`, strings.ToLower(strings.TrimSpace(hash)))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return false, err
		}
		if allow(host) {
			return true, nil
		}
	}
	return false, rows.Err()
}
