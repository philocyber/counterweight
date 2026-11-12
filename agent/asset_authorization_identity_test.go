package agent

import (
	"fmt"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

func TestInsertAssetsCannotBorrowUnrelatedAuthorization(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTask("asset identity authorization regression", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	allowed := fmt.Sprintf("allowed-%d.identity.example", time.Now().UnixNano())
	outside := "outside." + allowed
	if _, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "exact_domain", allowed, "fixture", true); err != nil {
		t.Fatal(err)
	}
	ts := NewToolSet(nil, "")
	ts.SetAssetStore(d.Assets(), d.Companies())
	ts.SetTaskID(task.ID)
	cases := []map[string]any{
		{"type": "root_domain", "domain": outside, "url": "https://" + allowed},
		{"type": "subdomain", "domain": outside, "bound_domains": []string{allowed}},
		{"type": "service", "url": "https://" + outside, "domain": allowed},
		{"type": "endpoint", "url": "https://" + outside + "/path", "method": "GET", "domain": allowed},
		{"type": "app", "app_name": "identity fixture", "domain": allowed},
		{"type": "ip", "ip": "192.0.2.19", "bound_domains": []string{allowed}},
		{"type": "service", "service_name": "ssh", "port": 22, "ip": "192.0.2.19", "domain": allowed},
	}
	for _, item := range cases {
		t.Run(fmt.Sprintf("%s/%v", item["type"], item), func(t *testing.T) {
			out := callInsertAssets(t, ts, map[string]any{"assets": []any{item}})
			results, _ := out["results"].([]any)
			errors, _ := out["errors"].([]any)
			if len(results) != 0 || len(errors) != 1 {
				t.Fatalf("borrowed authorization accepted: %+v", out)
			}
		})
	}
	if ts.writes.Assets != 0 {
		t.Fatal("rejected assets advanced the mutation counter")
	}
	if scopes, err := d.Assets().ListTaskScope(task.ID); err != nil || len(scopes) != 0 {
		t.Fatalf("rejected assets expanded task scope: %v %v", scopes, err)
	}
	if assets, err := d.Assets().QueryByTask(task.ID, "", 100, 0); err != nil || len(assets) != 0 {
		t.Fatalf("rejected assets were persisted: %v %v", assets, err)
	}

	// A domain-only allowlist still permits HTTP recon with an enrichment IP.
	url := "https://" + allowed
	out := callInsertAssets(t, ts, map[string]any{"assets": []any{map[string]any{
		"type": "service", "url": url, "service_ip": "192.0.2.19", "page_title": "retained title",
	}}})
	results, _ := out["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("authorized HTTP enrichment stopped working: %+v", out)
	}
	id := int64(results[0].(map[string]any)["id"].(float64))
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM assets WHERE url=$1 OR domain=$2 OR (type='ip' AND ip=$3)`, url, allowed, "192.0.2.19")
	})
	stored, err := d.Assets().GetByIDs([]int64{id})
	if err != nil || len(stored) != 1 || stored[0].IP != "192.0.2.19" || stored[0].PageTitle != "retained title" {
		t.Fatalf("HTTP evidence lost: %+v %v", stored, err)
	}
	if hits, err := d.Assets().CheckAssetsIntercept(task.ID, []int64{id}); err != nil || len(hits) != 0 {
		t.Fatalf("authorized persisted service rejected: %v %v", hits, err)
	}

	// An association stored outside insert_assets is still not task authority.
	ipID, err := d.Assets().UpsertIP(db.UpsertIPReq{IP: "192.0.2.19", BoundDomains: []string{allowed}})
	if err != nil {
		t.Fatal(err)
	}
	if hits, err := d.Assets().CheckAssetsIntercept(task.ID, []int64{ipID}); err != nil || len(hits) != 1 {
		t.Fatalf("forged persisted association authorized an IP: %v %v", hits, err)
	}
	if _, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "exact_ip", "192.0.2.19", "explicit IP authorization", true); err != nil {
		t.Fatal(err)
	}
	if hits, err := d.Assets().CheckAssetsIntercept(task.ID, []int64{ipID}); err != nil || len(hits) != 0 {
		t.Fatalf("explicitly authorized IP rejected: %v %v", hits, err)
	}
}

func TestPersistedIPAssociationDoesNotAuthorizeTarget(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTask("persisted association authorization regression", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	stamp := time.Now().UnixNano()
	domain := fmt.Sprintf("association-%d.identity.example", stamp)
	ip := fmt.Sprintf("2001:db8:%x:%x::%x", uint64(stamp)>>48, (uint64(stamp)>>32)&65535, uint64(stamp)&65535)
	if _, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "exact_domain", domain, "domain-only fixture", true); err != nil {
		t.Fatal(err)
	}
	id, err := d.Assets().UpsertIP(db.UpsertIPReq{IP: ip, BoundDomains: []string{domain}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Assets().DeleteByIDs([]int64{id}) })
	if hits, err := d.Assets().CheckAssetsIntercept(task.ID, []int64{id}); err != nil || len(hits) != 1 {
		t.Fatalf("domain metadata granted IP authorization: %v %v", hits, err)
	}
	if _, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "exact_ip", ip, "explicit IP fixture", true); err != nil {
		t.Fatal(err)
	}
	if hits, err := d.Assets().CheckAssetsIntercept(task.ID, []int64{id}); err != nil || len(hits) != 0 {
		t.Fatalf("explicit IP authorization rejected: %v %v", hits, err)
	}
}
