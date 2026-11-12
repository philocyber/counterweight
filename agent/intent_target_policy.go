package agent

import (
	"context"
	"encoding/json"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// Read all supported persisted anchor fields. Invalid supplied IDs must not
// silently turn a directed intent into an unanchored reconnaissance direction.
func persistedIntentTargets(intent *db.Node) ([]int64, error) {
	if intent == nil {
		return nil, locale.NewError("Intent target metadata is invalid")
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(intent.Payload, &payload) != nil || payload == nil {
		return nil, locale.NewError("Intent target metadata is invalid")
	}
	var targets []json.RawMessage
	for _, field := range []string{"asset_ids", "target_ids", "target_id"} {
		raw, exists := payload[field]
		if !exists || string(raw) == "null" || (field == "target_id" && string(raw) == `""`) {
			continue
		}
		items := []json.RawMessage{raw}
		if field != "target_id" && json.Unmarshal(raw, &items) != nil {
			return nil, locale.NewError("Intent target metadata is invalid")
		}
		targets = append(targets, items...)
	}
	return checkedIntentTargetIDs(targets)
}

func checkedIntentTargetIDs(targets []json.RawMessage) ([]int64, error) {
	var ids []int64
	seen := make(map[int64]bool)
	for _, target := range targets {
		id := pid(target)
		if id <= 0 {
			return nil, locale.NewError("Intent target metadata is invalid")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, nil
}

func validateWorkerIntentTargets(ctx context.Context, taskID int64, store *db.AssetStore, intent *db.Node) ([]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids, err := persistedIntentTargets(intent)
	if err == nil && RunInfoFrom(ctx).AssessmentType == db.AssessmentCodeReview {
		if len(ids) > 0 {
			return nil, locale.NewError("Code review intents use repository paths, not network asset IDs")
		}
		return nil, nil
	}
	if err != nil || len(ids) == 0 {
		return ids, err
	}
	if store == nil || taskID <= 0 {
		return nil, locale.NewError("Intent target policy is unavailable")
	}
	hits, err := store.CheckAssetsInterceptForLanguage(locale.FromContext(ctx), taskID, ids)
	if err != nil {
		return nil, locale.Errorf("Asset interception validation failed: %w", err)
	}
	if len(hits) != 0 {
		return nil, locale.NewError("Intent targets are no longer authorized by the task asset policy")
	}
	return ids, ctx.Err()
}
