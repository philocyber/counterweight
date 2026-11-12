package db

import (
	"fmt"
	"testing"
	"time"
)

func TestOperatorReadGrantsAreIndependentOfCoverage(t *testing.T) {
	d, assets, _ := testSetup(t)
	t.Cleanup(func() { _ = d.Close() })
	grand, err := d.CreateTask("indirect grant fixture", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	source, err := d.CreateTaskWithOptions("direct grant fixture", "test", TaskCreateOptions{SourceTaskIDs: []int64{grand.ID}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := d.CreateTaskWithOptions("current grant fixture", "test", TaskCreateOptions{SourceTaskIDs: []int64{source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(current.ID); _ = d.DeleteTask(source.ID); _ = d.DeleteTask(grand.ID) })
	marker := fmt.Sprintf("read-grant-%d", time.Now().UnixNano())
	var ids []int64
	t.Cleanup(func() { _, _ = assets.DeleteByIDs(ids) })
	for i, row := range []struct {
		task   int64
		origin string
	}{
		{current.ID, "manual"}, {current.ID, "agent"}, {current.ID, "auto"},
		{source.ID, "manual"}, {source.ID, "agent"}, {grand.ID, "manual"},
	} {
		host := fmt.Sprintf("%s-%d.invalid", marker, i)
		id, err := assets.UpsertHTTPService(UpsertHTTPServiceReq{URL: "https://" + host, Auth: []map[string]any{{"token": "FOREIGN_CREDENTIAL"}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if row.origin == "auto" {
			err = assets.AddAutoScope(row.task, "service", host, "https://"+host, "")
		} else {
			_, err = assets.AddAgentScope(row.task, "subdomain", host, "fixture", row.origin)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	check := func(want ...int64) {
		t.Helper()
		for _, byID := range []bool{false, true} {
			var got []*Asset
			var err error
			if byID {
				got, err = assets.GetByIDsInScope(current.ID, ids)
			} else {
				got, err = assets.QueryDSLInScope(marker, "service", current.ID, 50, 0)
			}
			if err != nil {
				t.Fatal(err)
			}
			allowed := map[int64]bool{}
			for _, id := range want {
				allowed[id] = true
			}
			if len(got) != len(want) {
				t.Fatalf("byID=%v: got %d assets, want %d", byID, len(got), len(want))
			}
			for _, asset := range got {
				if !allowed[asset.ID] || len(asset.Auth) != 1 || asset.Auth[0]["token"] != "FOREIGN_CREDENTIAL" {
					t.Fatalf("read boundary or approved credentials changed: %+v", asset)
				}
			}
		}
	}
	check(ids[0], ids[3])
	cov, err := assets.TaskCoverageWithSources(current.ID)
	if err != nil || cov.Denominator != 10 {
		t.Fatalf("agent/auto coverage changed: %+v %v", cov, err)
	}
	approved, err := assets.AddAgentScope(current.ID, "subdomain", fmt.Sprintf("%s-1.invalid", marker), "operator approval", "manual")
	if err != nil || approved.ID <= 0 || approved.Source != "manual" {
		t.Fatalf("proposal approval failed: %+v %v", approved, err)
	}
	duplicate, err := assets.AddAgentScope(current.ID, "subdomain", fmt.Sprintf("%s-1.invalid", marker), "agent overwrite", "agent")
	if err != nil || duplicate.ID != approved.ID || duplicate.Source != "manual" || duplicate.Reason != "operator approval" {
		t.Fatalf("agent replaced operator grant: %+v %v", duplicate, err)
	}
	check(ids[0], ids[1], ids[3])
	rows, err := assets.ListTaskScope(current.ID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("approval duplicated coverage row: %+v %v", rows, err)
	}
	if _, err := assets.DeleteTaskScope(current.ID, approved.ID); err != nil {
		t.Fatal(err)
	}
	check(ids[0], ids[3])
	if _, err := assets.AttachAssetsToTask(current.ID, []int64{ids[2]}, "operator selected this exact asset"); err != nil {
		t.Fatal(err)
	}
	if err := assets.SetTaskAssetSource(current.ID, ids[2], "agent", "agent rediscovery", nil); err != nil {
		t.Fatal(err)
	}
	check(ids[0], ids[2], ids[3])
	if _, err := assets.DetachAssetFromTask(current.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	check(ids[0], ids[3])
}

func TestMutableAttributionCannotWidenOperatorReadGrants(t *testing.T) {
	d, assets, companies := testSetup(t)
	t.Cleanup(func() { _ = d.Close() })
	for _, creation := range []bool{false, true} {
		t.Run(fmt.Sprintf("company-selected-at-creation=%v", creation), func(t *testing.T) {
			marker := fmt.Sprintf("frozen-grant-%d", time.Now().UnixNano())
			root := marker + ".invalid"
			foreignRoot := marker + "-foreign.invalid"
			company, _, err := companies.UpsertCompany(marker, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM companies WHERE id=$1`, company) })
			if added, _, invalid, msgs := companies.AddScope(company, []string{root}, "operator approved"); added != 1 || invalid != 0 {
				t.Fatalf("company scope: %d %d %v", added, invalid, msgs)
			}
			var ids []int64
			t.Cleanup(func() { _, _ = assets.DeleteByIDs(ids) })
			service := func(host, ip string) int64 {
				t.Helper()
				id, err := assets.UpsertHTTPService(UpsertHTTPServiceReq{URL: "https://" + host, IP: ip, Auth: []map[string]any{{"token": "CREDENTIAL_SENTINEL"}}})
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
				return id
			}
			original := service("original."+root, "")
			foreign := service("api."+foreignRoot, "")
			opts := TaskCreateOptions{}
			if creation {
				opts.CompanyIDs = []int64{company}
			}
			fixture, err := d.CreateTaskWithOptions("immutable operator grant", "test", opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = d.DeleteTask(fixture.ID) })
			if !creation {
				if _, err := assets.AddAgentScope(fixture.ID, "company", fmt.Sprint(company), "operator approved", "manual"); err != nil {
					t.Fatal(err)
				}
			}
			var prior string
			if err := d.QueryRow(`SELECT read_grant::text FROM task_scope WHERE task_id=$1 AND kind='company'`, fixture.ID).Scan(&prior); err != nil {
				t.Fatal(err)
			}
			// Actual company-scope mutation reattributes an existing credential-bearing
			// asset. This must not alter a task's earlier authorization decision.
			if added, _, invalid, msgs := companies.AddScope(company, []string{foreignRoot}, "agent attribution claim"); added != 1 || invalid != 0 {
				t.Fatalf("attribution update: %d %d %v", added, invalid, msgs)
			}
			var attributed int64
			if err := d.QueryRow(`SELECT company_id FROM assets WHERE id=$1`, foreign).Scan(&attributed); err != nil || attributed != company {
				t.Fatalf("attribution bypass not exercised: %d %v", attributed, err)
			}
			future := service("future."+root, "")
			if _, err := assets.AddAgentScope(fixture.ID, "company", fmt.Sprint(company), "agent proposal", "agent"); err != nil {
				t.Fatal(err)
			}
			if _, err := assets.AddAgentScope(fixture.ID, "company", fmt.Sprint(company), "duplicate operator request", "manual"); err != nil {
				t.Fatal(err)
			}
			var after string
			if err := d.QueryRow(`SELECT read_grant::text FROM task_scope WHERE task_id=$1 AND kind='company'`, fixture.ID).Scan(&after); err != nil || after != prior {
				t.Fatalf("grant changed after attribution/duplicate: %v", err)
			}
			got, err := assets.GetByIDsInScope(fixture.ID, []int64{original, foreign, future})
			if err != nil || len(got) != 2 {
				t.Fatalf("company authorization widened or future discovery broke: %d %v", len(got), err)
			}
			for _, a := range got {
				if a.ID == foreign {
					t.Fatal("foreign credential asset became authorized through attribution")
				}
			}
			// DNS enrichment cannot turn an unrelated HTTP origin into an IP grant.
			if _, err := assets.AddAgentScope(fixture.ID, "ip", "198.51.100.17", "operator approved", "manual"); err != nil {
				t.Fatal(err)
			}
			_ = service("api."+foreignRoot, "198.51.100.17")
			got, err = assets.GetByIDsInScope(fixture.ID, []int64{foreign})
			if err != nil || len(got) != 0 {
				t.Fatalf("enrichment IP widened HTTP origin authorization: %+v %v", got, err)
			}
			// Existing ICP matches can be approved, but later unverified ICP claims
			// must not authorize another stored credential-bearing asset.
			icp := fmt.Sprintf("fixture-icp-%d", fixture.ID)
			if _, err := d.Exec(`UPDATE assets SET icp=$1 WHERE id=$2`, icp, original); err != nil {
				t.Fatal(err)
			}
			if _, err := assets.AddAgentScope(fixture.ID, "icp", icp, "operator approved", "manual"); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`UPDATE assets SET icp=$1 WHERE id=$2`, icp, foreign); err != nil {
				t.Fatal(err)
			}
			got, err = assets.GetByIDsInScope(fixture.ID, []int64{foreign})
			if err != nil || len(got) != 0 {
				t.Fatalf("unverified ICP widened authorization: %+v %v", got, err)
			}
		})
	}
}
