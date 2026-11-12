package db

import (
	"testing"
	"time"
)

func TestTaskCursorRoundTrip(t *testing.T) {
	want := TaskCursor{CreatedAt: time.Date(2026, 11, 9, 10, 4, 5, 123456000, time.UTC), ID: 42}
	got, ok := ParseTaskCursor(want.String())
	if !ok || !got.CreatedAt.Equal(want.CreatedAt) || got.ID != want.ID {
		t.Fatalf("round trip = %+v, %v; want %+v", got, ok, want)
	}
	for _, raw := range []string{"", "152", "bad|1", "2026-11-09T10:04:05Z|x"} {
		if _, ok := ParseTaskCursor(raw); ok {
			t.Fatalf("ParseTaskCursor(%q) accepted a legacy or malformed value", raw)
		}
	}
}

// A task that reuses a deleted task's id must still appear after the cursor.
func TestNewTasksSinceReturnsReusedTaskID(t *testing.T) {
	d, _, _ := testSetup(t)
	t.Cleanup(func() { _ = d.Close() })
	first, err := d.CreateTask("cursor fixture first", "task-create cursor", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(first.ID) })
	second, err := d.CreateTask("cursor fixture second", "task-create cursor", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	events, err := d.NewTasksSince(TaskCursor{})
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.TaskID != second.ID {
		t.Fatalf("newest event = task %d, want %d", last.TaskID, second.ID)
	}
	cursor := TaskCursor{CreatedAt: last.CreatedAt, ID: last.TaskID}
	if err := d.DeleteTask(second.ID); err != nil {
		t.Fatal(err)
	}
	reused, err := d.CreateTask("cursor fixture reused", "task-create cursor", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(reused.ID) })
	if reused.ID > second.ID {
		t.Fatalf("fixture expected id reuse: got %d after deleting %d", reused.ID, second.ID)
	}
	events, err = d.NewTasksSince(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].TaskID != reused.ID {
		t.Fatalf("events after cursor = %+v, want only task %d", events, reused.ID)
	}
}
