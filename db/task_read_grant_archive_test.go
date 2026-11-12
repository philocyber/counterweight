package db

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestArchiveReadGrantsPreserveAuthorizationAndRequireLegacyApproval(t *testing.T) {
	d, assets, companies := testSetup(t)
	t.Cleanup(func() { _ = d.Close() })
	root := fmt.Sprintf("archive-read-%d.invalid", time.Now().UnixNano())
	foreignRoot := "foreign-" + root
	company, _, err := companies.UpsertCompany(root, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM companies WHERE id=$1`, company) })
	if added, _, invalid, msgs := companies.AddScope(company, []string{root}, "operator approved"); added != 1 || invalid != 0 {
		t.Fatalf("company scope: %d %d %v", added, invalid, msgs)
	}
	var ids []int64
	t.Cleanup(func() { _, _ = assets.DeleteByIDs(ids) })
	service := func(host string) int64 {
		t.Helper()
		id, err := assets.UpsertHTTPService(UpsertHTTPServiceReq{URL: "https://" + host, Auth: []map[string]any{{"token": "ARCHIVE_CREDENTIAL"}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		return id
	}
	original := service("api." + root)
	foreign := service("api." + foreignRoot)
	task, err := d.CreateTaskWithOptions("archive read grant", "test", TaskCreateOptions{CompanyIDs: []int64{company}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	// This global asset has no task_ids or anchors. Its grant must still export
	// its identity, otherwise restored numeric IDs could identify another asset.
	snapshot, err := d.SnapshotTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	archivedAssets, err := decodeArchiveRows(snapshot.Tables["assets"])
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range archivedAssets {
		if id, _ := jsonInt64(row["id"]); id == original {
			found = true
		}
	}
	if !found {
		t.Fatal("snapshot omitted global asset referenced by operator grant")
	}
	scopes, err := decodeArchiveRows(snapshot.Tables["task_scope"])
	if err != nil || len(scopes) != 1 || scopes[0]["read_grant"] == nil {
		t.Fatalf("missing grant: %+v %v", scopes, err)
	}
	if added, _, invalid, msgs := companies.AddScope(company, []string{foreignRoot}, "later attribution"); added != 1 || invalid != 0 {
		t.Fatalf("mutation: %d %d %v", added, invalid, msgs)
	}
	restore := func(raw json.RawMessage) {
		t.Helper()
		if _, err := d.Exec(`DELETE FROM task_scope WHERE task_id=$1`, task.ID); err != nil {
			t.Fatal(err)
		}
		tx, err := d.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = restoreTaskScopes(tx, raw); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want int) {
		t.Helper()
		got, err := assets.GetByIDsInScope(task.ID, []int64{original, foreign})
		if err != nil || len(got) != want {
			t.Fatalf("restored authorization: %d want %d: %v", len(got), want, err)
		}
		for _, a := range got {
			if len(a.Auth) != 1 || a.Auth[0]["token"] != "ARCHIVE_CREDENTIAL" {
				t.Fatal("approved credentials changed")
			}
		}
		if want == 1 && got[0].ID != original {
			t.Fatal("restore widened immutable company grant")
		}
	}
	restore(snapshot.Tables["task_scope"])
	check(1)
	// Legacy archives cannot silently approve today's expanded company catalog.
	delete(scopes[0], "read_grant")
	legacy, err := json.Marshal(scopes)
	if err != nil {
		t.Fatal(err)
	}
	restore(legacy)
	check(0)
	// Running startup migration again must not turn this legacy history into a grant.
	reopened, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = reopened.Close()
	check(0)
	rows, err := assets.ListTaskScopeWithSources(task.ID)
	if err != nil || len(rows) != 1 || rows[0].OperatorAuthorized {
		t.Fatalf("legacy approval UI state: %+v %v", rows, err)
	}
	proposal, err := assets.AddAgentScope(task.ID, "company", fmt.Sprint(company), "agent retry", "agent")
	if err != nil || proposal.OperatorAuthorized {
		t.Fatalf("agent promoted legacy grant: %+v %v", proposal, err)
	}
	check(0)
	approved, err := assets.AddAgentScope(task.ID, "company", fmt.Sprint(company), "operator explicitly approved restored scope", "manual")
	if err != nil || !approved.OperatorAuthorized || approved.ID != rows[0].ID {
		t.Fatalf("explicit legacy approval: %+v %v", approved, err)
	}
	check(2)
}

func TestArchiveReadGrantIDsRemapWithoutReauthorizingMissingAssets(t *testing.T) {
	tables := map[string]json.RawMessage{
		"task_scope": json.RawMessage(`[{"source":"manual","kind":"company","read_grant":{"asset_ids":[11,12,"13","invalid"],"scope":[{"kind":"domain","domain":"approved.invalid"}]}}]`),
		"findings":   json.RawMessage(`[{"asset_ids":[11,12]}]`),
	}
	got, err := remapArchiveAssetReferences(tables, map[int64]int64{11: 101, 13: 103})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := decodeArchiveRows(got["task_scope"])
	if err != nil {
		t.Fatal(err)
	}
	grant := rows[0]["read_grant"].(map[string]any)
	ids := grant["asset_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("unmapped grant IDs survived: %v", ids)
	}
	for i, want := range []int64{101, 103} {
		if id, ok := jsonInt64(ids[i]); !ok || id != want {
			t.Fatalf("grant remap: %v", ids)
		}
	}
	networks := grant["scope"].([]any)
	if len(networks) != 1 || networks[0].(map[string]any)["domain"] != "approved.invalid" {
		t.Fatal("approved network lost during ID remap")
	}
	findingRows, _ := decodeArchiveRows(got["findings"])
	findingIDs := findingRows[0]["asset_ids"].([]any)
	if len(findingIDs) != 2 {
		t.Fatal("unmapped historical evidence dropped")
	}
	if id, _ := jsonInt64(findingIDs[1]); id != 12 {
		t.Fatal("historical evidence incorrectly rewritten")
	}
}
