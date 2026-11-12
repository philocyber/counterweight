package db

import (
	"fmt"
	"testing"
	"time"
)

func TestAssetDSLORCannotEscapeIndependentFilters(t *testing.T) {
	d, assets, _ := testSetup(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTask("isolated DSL OR fixture", "verify asset query boundaries", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	marker := fmt.Sprintf("dsl-or-%d", time.Now().UnixNano())
	root := marker + ".invalid"
	if err := assets.upsertTaskScope(TaskScope{TaskID: task.ID, Kind: "root_domain", Domain: root, Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=ANY($1::bigint[])`, ids) })
	for _, row := range []struct {
		typ, domain, title string
		member             bool
	}{
		{"subdomain", "a." + root, marker + "-inside", true},
		{"subdomain", "outside." + root + ".other", marker + "-outside", false},
		{"service", "b." + root, marker + "-wrong-type", true},
	} {
		var id int64
		taskIDs := []int64{}
		rootDomain := "other.invalid"
		if row.member {
			taskIDs = []int64{task.ID}
			rootDomain = root
		}
		if err := d.QueryRow(`INSERT INTO assets(type,domain,root_domain,page_title,task_ids,last_seen) VALUES ($1,$2,$3,$4,$5,now()) RETURNING id`, row.typ, row.domain, rootDomain, row.title, taskIDs).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// Both OR branches match all fixture rows. Independent filters must apply
	// to the entire expression, including the first branch.
	dsl := fmt.Sprintf(`title="%s" OR domain="%s"`, marker, marker)
	for _, tc := range []struct {
		name, typ string
		taskID    int64
		scope     bool
		want      int
	}{
		{"type only", "subdomain", 0, false, 2},
		{"producer task only", "", task.ID, false, 2},
		{"type and producer task", "subdomain", task.ID, false, 1},
		{"scope membership only", "", task.ID, true, 2},
		{"type and scope membership", "subdomain", task.ID, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []*Asset
			var err error
			if tc.scope {
				got, err = assets.QueryDSLInScope(dsl, tc.typ, tc.taskID, 50, 0)
			} else {
				got, err = assets.QueryDSL(dsl, tc.typ, tc.taskID, 50, 0)
			}
			if err != nil || len(got) != tc.want {
				t.Fatalf("query escaped boundary: count=%d want=%d err=%v", len(got), tc.want, err)
			}
			if !tc.scope {
				count, err := assets.CountDSL(dsl, tc.typ, tc.taskID)
				if err != nil || count != tc.want {
					t.Fatalf("count escaped boundary: count=%d want=%d err=%v", count, tc.want, err)
				}
			}
		})
	}
	global, err := assets.QueryDSLInScope(dsl, "", 0, 50, 0)
	if err != nil || len(global) != 3 {
		t.Fatalf("legitimate global query changed: rows=%d err=%v", len(global), err)
	}
}
