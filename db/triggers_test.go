package db

import (
	"strings"
	"testing"
)

// NewToolCallsSince pages by id, cuts the detail in SQL and reports full lengths.
func TestNewToolCallsSincePagesAndTruncates(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	task, err := d.CreateTask("tool call paging fixture", "paging", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)

	long := strings.Repeat("x", 40)
	var ids []int64
	for i := 0; i < 5; i++ {
		var id int64
		err := d.QueryRow(`
INSERT INTO activity(exploration_id, kind, tool, tool_use_id, detail)
VALUES ($1,'tool_result','nmap',$2,$3) RETURNING id`, task.ExplorationID, "use-"+strings.Repeat("a", i+1), long).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if _, err := d.Exec(`INSERT INTO activity(exploration_id, kind, tool, tool_use_id, detail)
VALUES ($1,'tool_use','nmap',$2,$3)`, task.ExplorationID, "use-"+strings.Repeat("a", i+1), "input"); err != nil {
			t.Fatal(err)
		}
	}

	var got []int64
	last := ids[0] - 1
	for {
		page, err := d.NewToolCallsSince(last, 2, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > 2 {
			t.Fatalf("page has %d rows, limit 2", len(page))
		}
		for _, e := range page {
			got = append(got, e.NodeID)
			last = e.NodeID
			if e.ToolOutput != strings.Repeat("x", 10) || e.ToolOutputLen != 40 {
				t.Fatalf("output = %q (len %d), want 10 chars of 40", e.ToolOutput, e.ToolOutputLen)
			}
			if e.ToolInput != "input" || e.ToolInputLen != 5 {
				t.Fatalf("input = %q (len %d)", e.ToolInput, e.ToolInputLen)
			}
		}
		if len(page) == 0 {
			break
		}
	}
	if len(got) != len(ids) {
		t.Fatalf("got ids %v, want %v", got, ids)
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("got ids %v, want %v", got, ids)
		}
	}
	if max, err := d.MaxToolCallID(); err != nil || max < ids[len(ids)-1] {
		t.Fatalf("MaxToolCallID = %d, %v; want >= %d", max, err, ids[len(ids)-1])
	}
}
