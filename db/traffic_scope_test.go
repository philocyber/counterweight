package db

import (
	"fmt"
	"testing"
	"time"
)

func TestTrafficHostScopeUsesOperatorGrantsOnly(t *testing.T) {
	d, assets, _ := testSetup(t)
	t.Cleanup(func() { _ = d.Close() })
	mark := fmt.Sprintf("ts%d", time.Now().UnixNano())
	task, err := d.CreateTask("traffic scope fixture", "scope", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	other, err := d.CreateTask("traffic scope other", "scope", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(other.ID) })

	granted := mark + "-granted.invalid"
	proposed := mark + "-proposed.invalid"
	foreign := mark + "-foreign.invalid"
	for _, row := range []struct {
		task                int64
		kind, value, source string
	}{
		{task.ID, "root_domain", granted, "manual"},
		{task.ID, "root_domain", proposed, "agent"},
		{task.ID, "cidr", "198.51.100.0/24", "manual"},
		{task.ID, "cidr", "203.0.113.0/24", "agent"},
		{other.ID, "root_domain", foreign, "manual"},
	} {
		if _, err := assets.AddAgentScope(row.task, row.kind, row.value, "test", row.source); err != nil {
			t.Fatalf("%s %s: %v", row.kind, row.value, err)
		}
	}

	// An asset an agent recorded for the task widens nothing.
	recorded := mark + "-recorded.invalid"
	var assetID int64
	if err := d.QueryRow(`INSERT INTO assets(type,domain,root_domain,task_ids,last_seen) VALUES ('subdomain',$1,$1,$2,now()) RETURNING id`,
		recorded, fmt.Sprintf("{%d}", task.ID)).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM assets WHERE id=$1`, assetID) })

	scope, err := assets.TrafficHostScopeForTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{
		granted:                true,
		"www." + granted:       true,
		"WWW." + granted + ".": true,
		"x" + granted:          false,
		proposed:               false,
		recorded:               false,
		"www." + proposed:      false,
		foreign:                false,
		"198.51.100.7":         true,
		"203.0.113.7":          false,
		"192.0.2.1":            false,
		"":                     false,
	} {
		if got := scope.Allows(host); got != want {
			t.Errorf("Allows(%q) = %v, want %v", host, got, want)
		}
	}
	if (*TrafficHostScope)(nil).Allows(granted) {
		t.Error("nil scope must deny")
	}
}
