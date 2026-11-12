package agent

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

func TestIntentTargetAdmissionRetainsRequiredAnchors(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	for _, lang := range []locale.Lang{locale.En} {
		t.Run(string(lang), func(t *testing.T) {
			task, err := d.CreateTask("intent admission policy", "fixture", nil, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer d.DeleteTask(task.ID)
			allowedURL := fmt.Sprintf("https://admission-%d.example.test", task.ID)
			outsideURL := allowedURL + ".outside"
			allowed, err := d.Assets().UpsertHTTPService(db.UpsertHTTPServiceReq{URL: allowedURL})
			if err != nil {
				t.Fatal(err)
			}
			defer d.Exec(`DELETE FROM assets WHERE id=$1`, allowed)
			outside, err := d.Assets().UpsertHTTPService(db.UpsertHTTPServiceReq{URL: outsideURL})
			if err != nil {
				t.Fatal(err)
			}
			defer d.Exec(`DELETE FROM assets WHERE id=$1`, outside)
			if _, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "exact_url", allowedURL, "fixture", true); err != nil {
				t.Fatal(err)
			}
			store := d.Exploration(task.ExplorationID)
			tools := NewToolSet(store, "planner", lang)
			tools.SetTaskID(task.ID)
			tools.SetAssetStore(d.Assets(), d.Companies())
			cases := []struct {
				name, input string
				created     int
				batch       bool
				errorIndex  string
			}{
				{"invalid-string", `{"summary":"fixture","asset_ids":["invalid"]}`, 0, false, ""},
				{"null-element", `{"summary":"fixture","asset_ids":[null]}`, 0, false, ""},
				{"zero", `{"summary":"fixture","asset_ids":[0]}`, 0, false, ""},
				{"negative", `{"summary":"fixture","asset_ids":[-1]}`, 0, false, ""},
				{"fraction", `{"summary":"fixture","asset_ids":[1.5]}`, 0, false, ""},
				{"wrong-field-type", `{"summary":"fixture","asset_ids":"invalid"}`, 0, false, ""},
				{"legacy-blocked", fmt.Sprintf(`{"summary":"fixture","target_ids":[%d]}`, outside), 0, false, ""},
				{"legacy-single-blocked", fmt.Sprintf(`{"summary":"fixture","asset_ids":null,"target_id":"%d"}`, outside), 0, false, ""},
				{"conflicting-fields", fmt.Sprintf(`{"summary":"fixture","asset_ids":[%d],"target_ids":[%d]}`, allowed, outside), 0, false, ""},
				{"mixed-ids", fmt.Sprintf(`{"summary":"fixture","asset_ids":[%d,"invalid"]}`, allowed), 0, false, ""},
				{"allowed", fmt.Sprintf(`{"summary":"fixture","asset_ids":[%d]}`, allowed), 1, false, ""},
				{"allowed-string", fmt.Sprintf(`{"summary":"fixture","asset_ids":["%d"]}`, allowed), 1, false, ""},
				{"allowed-legacy", fmt.Sprintf(`{"summary":"fixture","target_id":"%d"}`, allowed), 1, false, ""},
				{"unanchored", `{"summary":"fixture"}`, 1, false, ""},
				{"batch-invalid-id", fmt.Sprintf(`{"intents":[{"summary":"fixture","asset_ids":[%d]},{"summary":"fixture","asset_ids":["invalid"]}]}`, allowed), 1, true, "1"},
				{"batch-invalid-shape", fmt.Sprintf(`{"intents":[{"summary":"fixture","asset_ids":[%d]},{"summary":"fixture","asset_ids":"invalid"}]}`, allowed), 1, true, "1"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					before, err := store.Frontier(100)
					if err != nil {
						t.Fatal(err)
					}
					revived := 0
					tools.resumeTask = func() { revived++ }
					result, err := tools.addIntent().Call(locale.WithLang(t.Context(), lang), json.RawMessage(tc.input), nil)
					if err != nil {
						t.Fatal(err)
					}
					after, err := store.Frontier(100)
					if err != nil {
						t.Fatal(err)
					}
					if len(after)-len(before) != tc.created || (revived > 0) != (tc.created > 0) {
						t.Fatalf("admission side effects: created=%d want=%d revived=%d output=%q", len(after)-len(before), tc.created, revived, result.Flatten())
					}
					if tc.batch {
						var out struct {
							IDs    []int64           `json:"ids"`
							Errors map[string]string `json:"errors"`
						}
						if json.Unmarshal([]byte(result.Flatten()), &out) != nil || len(out.IDs) != 2 || out.IDs[0] <= 0 || out.IDs[1] != 0 || out.Errors[tc.errorIndex] == "" {
							t.Fatalf("batch result lost item positions: %q", result.Flatten())
						}
					} else if result.IsError != (tc.created == 0) {
						t.Fatalf("admission result error=%v output=%q", result.IsError, result.Flatten())
					}
					if tc.created == 1 && !tc.batch && tc.name != "unanchored" {
						var newest *db.Node
						for _, node := range after {
							if newest == nil || node.ID > newest.ID {
								newest = node
							}
						}
						ids, err := persistedIntentTargets(newest)
						if err != nil || len(ids) != 1 || ids[0] != allowed {
							t.Fatalf("authorized anchors lost during persistence: ids=%v error=%v", ids, err)
						}
					}
				})
			}
		})
	}
}
