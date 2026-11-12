package server

import (
	"testing"

	"github.com/Philocyber/counterweight/db"
)

// fires reports whether fireTaskTimeouts tried to start a run. The scheduler
// has no server, so an attempted trigger panics and is recovered here.
func fires(sc *Scheduler) (fired bool) {
	defer func() {
		if recover() != nil {
			fired = true
		}
	}()
	sc.fireTaskTimeouts([]*db.AgentTrigger{{OnTaskTimeout: true, AgentKey: "reporter"}})
	return false
}

func TestInitialTaskCursorMigratesLegacyWatermark(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skipf("no database config (%v) — skipping", err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer pg.Close()
	task, err := pg.CreateTask("cursor migration fixture", "scheduler cursor", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DeleteTask(task.ID) })
	t.Cleanup(func() { _, _ = pg.Exec(`DELETE FROM scheduler_state WHERE key=$1`, schedKeyLegacyTaskCreate) })
	sc := &Scheduler{pg: pg}

	if _, err := pg.Exec(`DELETE FROM scheduler_state WHERE key=$1`, schedKeyLegacyTaskCreate); err != nil {
		t.Fatal(err)
	}
	evs, err := pg.NewTasksSince(db.TaskCursor{})
	if err != nil {
		t.Fatal(err)
	}
	newest := evs[len(evs)-1]
	got, err := sc.initialTaskCursor()
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != newest.TaskID || !got.CreatedAt.Equal(newest.CreatedAt) {
		t.Fatalf("fresh install cursor = %+v, want newest task %d", got, newest.TaskID)
	}

	// A legacy watermark of 0 means no task had fired yet: every task is pending.
	if err := pg.SetSchedState(schedKeyLegacyTaskCreate, "0"); err != nil {
		t.Fatal(err)
	}
	got, err = sc.initialTaskCursor()
	if err != nil {
		t.Fatal(err)
	}
	if got != (db.TaskCursor{}) {
		t.Fatalf("legacy 0 cursor = %+v, want the zero cursor", got)
	}
}

// A lower task id that times out after a higher one must still fire once.
func TestTaskTimeoutTriggerFiresOutOfOrder(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skipf("no database config (%v) — skipping", err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer pg.Close()

	low, err := pg.CreateTask("timeout fixture low", "scheduler cursor", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DeleteTask(low.ID) })
	high, err := pg.CreateTask("timeout fixture high", "scheduler cursor", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DeleteTask(high.ID) })
	setStatus := func(id int64) {
		if _, err := pg.Exec(`UPDATE tasks SET status='timeout' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}

	sc := &Scheduler{pg: pg}
	// Treat any pre-existing timed-out rows as history.
	sc.fireTaskTimeouts(nil)

	setStatus(high.ID)
	if !fires(sc) {
		t.Fatal("first timeout did not fire")
	}
	sc.fireTaskTimeouts(nil) // record it, as the recovered run could not
	if fires(sc) {
		t.Fatal("recorded timeout fired again")
	}
	setStatus(low.ID)
	if !fires(sc) {
		t.Fatal("lower-id timeout after a higher one did not fire")
	}
}
