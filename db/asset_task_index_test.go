package db

import (
	"strings"
	"testing"
)

// The per-task asset predicate must be expressed with an array operator so the
// GIN index on assets.task_ids is usable.
func TestTaskAssetFilterUsesGINIndex(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()

	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(`EXPLAIN SELECT count(*) FROM assets WHERE task_ids @> ARRAY[$1::bigint]`, int64(1))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(plan, "\n")
	if !strings.Contains(text, "idx_av2_task_ids") {
		t.Fatalf("plan does not use idx_av2_task_ids:\n%s", text)
	}
	t.Log(text)
}
