package server

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// firedRun is one recorded StartTriggeredRun call.
type firedRun struct {
	agent, title, message string
	taskID                int64
}

// recordRuns replaces the scheduler's run starter with a recorder.
func recordRuns(sc *Scheduler) *[]firedRun {
	var runs []firedRun
	sc.start = func(agentKey, title, message string, taskID int64, _ bool, _, _ string, _ ...locale.Lang) {
		runs = append(runs, firedRun{agentKey, title, message, taskID})
	}
	return &runs
}

func openSchedulerTestDB(t *testing.T) *db.DB {
	t.Helper()
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skipf("no database config (%v) — skipping", err)
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { pg.Close() })
	return pg
}

// With no trigger enabled the watermarks still advance, so a trigger enabled later
// fires only for events created after it was enabled.
func TestSchedulerWatermarksAdvanceWithoutTriggers(t *testing.T) {
	pg := openSchedulerTestDB(t)
	task, err := pg.CreateTask("watermark fixture", "scheduler watermark", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DeleteTask(task.ID) })
	sc := &Scheduler{pg: pg}
	runs := recordRuns(sc)

	addFinding := func(name string) {
		if _, err := pg.Exec(`INSERT INTO exploration_nodes(exploration_id, kind, state, payload)
VALUES ($1,'finding','confirmed',jsonb_build_object('summary',$2::text,'vulnclass','xss','severity','low'))`, task.ExplorationID, name); err != nil {
			t.Fatal(err)
		}
	}
	addToolCall := func(out string) {
		if _, err := pg.Exec(`INSERT INTO activity(exploration_id, kind, tool, tool_use_id, detail)
VALUES ($1,'tool_result','nmap','u',$2)`, task.ExplorationID, out); err != nil {
			t.Fatal(err)
		}
	}
	triggers := []*db.AgentTrigger{
		{AgentKey: "wm-finding", OnFinding: true},
		{AgentKey: "wm-goal", OnGoalMet: true},
		{AgentKey: "wm-tool", OnToolCall: true, ToolNames: []string{"nmap"}},
		{AgentKey: "wm-task", OnTaskCreate: true},
	}

	// Make the stored watermarks current, then record history with no trigger.
	sc.stepWith(nil)
	for i := range 3 {
		addFinding(fmt.Sprintf("old-finding-%d", i))
		addToolCall(fmt.Sprintf("old-out-%d", i))
	}
	if _, err := pg.Exec(`INSERT INTO exploration_nodes(exploration_id, kind, state, payload)
VALUES ($1,'goal','met','{"text":"old-goal"}')`, task.ExplorationID); err != nil {
		t.Fatal(err)
	}
	other, err := pg.CreateTask("watermark fixture two", "scheduler watermark", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DeleteTask(other.ID) })
	sc.stepWith(nil)

	// Enabling the triggers now replays nothing.
	sc.stepWith(triggers)
	if len(*runs) != 0 {
		t.Fatalf("old events fired: %+v", *runs)
	}

	// One new event per stream fires exactly once, for the matching trigger.
	addFinding("new-finding")
	addToolCall("new-out")
	third, err := pg.CreateTask("watermark fixture three", "scheduler watermark", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DeleteTask(third.ID) })
	sc.stepWith(triggers)
	want := map[string]func(firedRun) bool{
		"wm-finding": func(r firedRun) bool {
			return strings.HasPrefix(r.title, "Finding trigger") && strings.Contains(r.message, "new-finding") && r.taskID == task.ID
		},
		"wm-tool": func(r firedRun) bool {
			return strings.HasPrefix(r.title, "Tool trigger") && strings.Contains(r.message, "new-out") && r.taskID == task.ID
		},
		"wm-task": func(r firedRun) bool {
			return strings.HasPrefix(r.title, "Task creation trigger") && r.taskID == third.ID
		},
	}
	if len(*runs) != len(want) {
		t.Fatalf("fires = %+v, want one per stream", *runs)
	}
	for _, r := range *runs {
		check, ok := want[r.agent]
		if !ok || !check(r) {
			t.Fatalf("unexpected fire %+v", r)
		}
		delete(want, r.agent)
	}

	// A second pass has nothing new.
	*runs = nil
	sc.stepWith(triggers)
	if len(*runs) != 0 {
		t.Fatalf("second pass fired again: %+v", *runs)
	}
}

// A matching trigger over a backlog larger than one page receives every event
// exactly once, in id order.
func TestFireToolCallsDeliversEveryEventAcrossPages(t *testing.T) {
	pg := openSchedulerTestDB(t)
	task, err := pg.CreateTask("paging fixture", "scheduler paging", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DeleteTask(task.ID) })
	sc := &Scheduler{pg: pg}
	runs := recordRuns(sc)
	sc.stepWith(nil)

	n := toolCallPage*2 + 100
	if _, err := pg.Exec(`INSERT INTO activity(exploration_id, kind, tool, tool_use_id, detail)
SELECT $1,'tool_result','nmap','u'||g,'out-'||g FROM generate_series(1,$2) g ORDER BY g`, task.ExplorationID, n); err != nil {
		t.Fatal(err)
	}
	trig := []*db.AgentTrigger{{AgentKey: "wm-page", OnToolCall: true, ToolNames: []string{"nmap"}}}
	sc.stepWith(trig)
	if len(*runs) != n {
		t.Fatalf("delivered %d events, want %d", len(*runs), n)
	}
	for i, r := range *runs {
		if !strings.HasSuffix(r.message, "out-"+strconv.Itoa(i+1)) {
			t.Fatalf("event %d out of order or duplicated: %q", i+1, r.message)
		}
	}
	*runs = nil
	sc.stepWith(trig)
	if len(*runs) != 0 {
		t.Fatalf("second pass fired %d events", len(*runs))
	}
}

// failOnce wraps a lookup so its first call fails, as a transient database error.
func failOnce[T any](real func() (T, error)) func() (T, error) {
	failed := false
	return func() (T, error) {
		if !failed {
			failed = true
			var zero T
			return zero, errors.New("forced failure")
		}
		return real()
	}
}

// A stream whose seeding failed is not processed: enabling a trigger fires
// nothing, the next pass seeds it at the current maximum, and only later events
// fire.
func TestSchedulerFindingSeedFailureDoesNotReplayHistory(t *testing.T) {
	pg := openSchedulerTestDB(t)
	task, err := pg.CreateTask("seed failure fixture", "scheduler seed", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DeleteTask(task.ID) })
	addFinding := func(name string) {
		if _, err := pg.Exec(`INSERT INTO exploration_nodes(exploration_id, kind, state, payload)
VALUES ($1,'finding','confirmed',jsonb_build_object('summary',$2::text,'vulnclass','xss','severity','low'))`, task.ExplorationID, name); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		addFinding(fmt.Sprintf("old-%d", i))
	}
	if _, err := pg.Exec(`DELETE FROM scheduler_state WHERE key=$1`, schedKeyLastFinding); err != nil {
		t.Fatal(err)
	}

	sc := &Scheduler{pg: pg}
	sc.maxFinding = failOnce(pg.MaxFindingID)
	runs := recordRuns(sc)
	trig := []*db.AgentTrigger{{AgentKey: "seed-finding", OnFinding: true}}

	sc.stepWith(trig)
	if len(*runs) != 0 {
		t.Fatalf("fired with an unseeded watermark: %+v", *runs)
	}
	if v, _ := pg.GetSchedState(schedKeyLastFinding); v != "" {
		t.Fatalf("watermark written after a failed lookup: %q", v)
	}

	sc.stepWith(trig)
	maxID, _ := pg.MaxFindingID()
	if len(*runs) != 0 {
		t.Fatalf("old findings fired after seeding: %+v", *runs)
	}
	if v, _ := pg.GetSchedState(schedKeyLastFinding); v != strconv.FormatInt(maxID, 10) {
		t.Fatalf("watermark = %q, want %d", v, maxID)
	}

	addFinding("new-finding")
	sc.stepWith(trig)
	if len(*runs) != 1 || !strings.Contains((*runs)[0].message, "new-finding") {
		t.Fatalf("new finding fires = %+v, want exactly one", *runs)
	}
}

func TestSchedulerTaskCreateSeedFailureDoesNotReplayHistory(t *testing.T) {
	pg := openSchedulerTestDB(t)
	for i := range 2 {
		old, err := pg.CreateTask(fmt.Sprintf("old task %d", i), "scheduler seed", nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pg.DeleteTask(old.ID) })
	}
	if _, err := pg.Exec(`DELETE FROM scheduler_state WHERE key IN ($1,$2)`, schedKeyLastTaskCreate, schedKeyLegacyTaskCreate); err != nil {
		t.Fatal(err)
	}

	sc := &Scheduler{pg: pg}
	sc.newestTask = failOnce(pg.NewestTaskCursor)
	runs := recordRuns(sc)
	trig := []*db.AgentTrigger{{AgentKey: "seed-task", OnTaskCreate: true}}

	sc.stepWith(trig)
	if len(*runs) != 0 {
		t.Fatalf("fired with an unseeded watermark: %+v", *runs)
	}
	if v, _ := pg.GetSchedState(schedKeyLastTaskCreate); v != "" {
		t.Fatalf("cursor written after a failed lookup: %q", v)
	}

	sc.stepWith(trig)
	if len(*runs) != 0 {
		t.Fatalf("old tasks fired after seeding: %+v", *runs)
	}
	newest, _ := pg.NewestTaskCursor()
	if v, _ := pg.GetSchedState(schedKeyLastTaskCreate); v != newest.String() {
		t.Fatalf("cursor = %q, want %q", v, newest.String())
	}

	fresh, err := pg.CreateTask("new task", "scheduler seed", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DeleteTask(fresh.ID) })
	sc.stepWith(trig)
	if len(*runs) != 1 || (*runs)[0].taskID != fresh.ID {
		t.Fatalf("new task fires = %+v, want exactly one for task %d", *runs, fresh.ID)
	}
}
