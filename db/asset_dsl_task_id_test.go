package db

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAssetDSLTaskIDOperatorsSQL(t *testing.T) {
	for _, tc := range []struct{ dsl, want string }{
		{"task_id=42", "task_ids @> ARRAY[$1::bigint]"},
		{"task_id==42", "task_ids @> ARRAY[$1::bigint]"},
		{"task_id!=42", "NOT (task_ids @> ARRAY[$1::bigint])"},
		{"task_id!=42 AND type=ip", "(NOT (task_ids @> ARRAY[$1::bigint])) AND"},
	} {
		node, err := ParseDSL(tc.dsl)
		if err != nil {
			t.Fatalf("%s: %v", tc.dsl, err)
		}
		where, _, err := buildDSLWhere(node)
		if err != nil {
			t.Fatalf("%s: %v", tc.dsl, err)
		}
		if !strings.Contains(where, tc.want) {
			t.Errorf("%s: got %q, want it to contain %q", tc.dsl, where, tc.want)
		}
	}
	for _, op := range []string{">", ">=", "<", "<="} {
		if err := ValidateDSL("task_id" + op + "42"); err == nil {
			t.Errorf("task_id%s42 must be rejected", op)
		}
	}
}

func TestAssetDSLTaskIDInequalityResults(t *testing.T) {
	d, assets, _ := testSetup(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTask("task_id DSL fixture", "verify task_id operators", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	marker := fmt.Sprintf("dsl-tid-%d", time.Now().UnixNano())
	var ids []int64
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=ANY($1::bigint[])`, ids) })
	for i, taskIDs := range [][]int64{{task.ID}, {}} {
		var id int64
		if err := d.QueryRow(`INSERT INTO assets(type,domain,root_domain,page_title,task_ids,last_seen) VALUES ('subdomain',$1,'tid.invalid',$2,$3,now()) RETURNING id`,
			fmt.Sprintf("h%d.%s.invalid", i, marker), marker, taskIDs).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, tc := range []struct {
		dsl  string
		want int
	}{
		{fmt.Sprintf(`title="%s" AND task_id=%d`, marker, task.ID), 1},
		{fmt.Sprintf(`title="%s" AND task_id==%d`, marker, task.ID), 1},
		// The asset with an empty task_ids array is not a member, so it is kept.
		{fmt.Sprintf(`title="%s" AND task_id!=%d`, marker, task.ID), 1},
		{fmt.Sprintf(`title="%s" AND (task_id=%d OR task_id!=%d)`, marker, task.ID, task.ID), 2},
	} {
		got, err := assets.CountDSL(tc.dsl, "", 0)
		if err != nil {
			t.Fatalf("%s: %v", tc.dsl, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.dsl, got, tc.want)
		}
	}
}
