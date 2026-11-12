package db

import (
	"bytes"
	"fmt"
	"testing"
)

func archiveFixtureCycle(t *testing.T, d *DB, taskID int64) (*TaskArchive, *TaskArchiveSnapshot, *bytes.Buffer) {
	t.Helper()
	if err := d.SetPaused(taskID, true); err != nil {
		t.Fatal(err)
	}
	archive, err := d.QueueTaskArchive(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := d.ClaimTaskArchiveJob(t.Context()); err != nil || claimed == nil || claimed.ID != archive.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	var records bytes.Buffer
	snapshot, err := d.SnapshotTaskArchiveWithLLMRecords(taskID, &records)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/approvals-test.tar.zst", "abc", 10, 5); err != nil {
		t.Fatal(err)
	}
	return archive, snapshot, &records
}

func countGrants(t *testing.T, d *DB, taskID int64) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM task_tool_grants WHERE task_id=$1`, fmt.Sprint(taskID)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestArchiveRestoreDropsGrantsAndReplayEligibility(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	if err := d.EnsureLLMRecordsTable(); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureLLMUsageTable(); err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTaskWithOptions("archive approvals", "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprint(task.ID)
	defer func() {
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM task_approval_holds WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM task_tool_grants WHERE task_id=$1`, key)
		_ = d.DeleteTask(task.ID)
	}()
	if _, err := d.Exec(`INSERT INTO task_tool_grants(task_id, scope, tool_name) VALUES ($1,'all','')`, key); err != nil {
		t.Fatal(err)
	}
	var allowedID, pendingID int64
	if err := d.QueryRow(`INSERT INTO intercept_pending(task_id,tool_name,tool_input,status,deferred_task,approval_timeout_action)
VALUES($1,'Bash','{"command":"id"}','allowed',true,'deny') RETURNING id`, key).Scan(&allowedID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO intercept_pending(task_id,tool_name,tool_input,status,deferred_task,approval_timeout_action)
VALUES($1,'Bash','{"command":"whoami"}','pending',true,'allow') RETURNING id`, key).Scan(&pendingID); err != nil {
		t.Fatal(err)
	}
	archive, snapshot, records := archiveFixtureCycle(t, d, task.ID)
	if n := countGrants(t, d, task.ID); n != 0 {
		t.Fatalf("archived task keeps %d grants", n)
	}
	// An archive written before grants were dropped at archive time.
	if _, err := d.Exec(`INSERT INTO task_tool_grants(task_id, scope, tool_name) VALUES ($1,'all','')`, key); err != nil {
		t.Fatal(err)
	}
	if _, err := d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := d.ClaimTaskArchiveJob(t.Context()); err != nil || claimed == nil {
		t.Fatalf("restore claim = %+v, %v", claimed, err)
	}
	if _, err := d.RestoreTaskArchiveWithLLMRecords(archive.ID, snapshot, 600, bytes.NewReader(records.Bytes())); err != nil {
		t.Fatal(err)
	}
	if n := countGrants(t, d, task.ID); n != 0 {
		t.Fatalf("restored task has %d grants", n)
	}
	var status, action string
	var deferred bool
	if err := d.QueryRow(`SELECT status,approval_timeout_action,deferred_task FROM intercept_pending WHERE id=$1`, pendingID).Scan(&status, &action, &deferred); err != nil {
		t.Fatal(err)
	}
	if status != "timeout" || action != "deny" || deferred {
		t.Fatalf("restored pending row status=%s action=%s deferred=%v", status, action, deferred)
	}
	if _, err := d.Exec(`INSERT INTO task_approval_holds(task_id) VALUES($1)`, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.RefreshTaskApprovalReplays(task.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := d.ConsumeTaskApprovalReplay(key, "Bash", []byte(`{"command":"id"}`)); err != nil || id != 0 {
		t.Fatalf("replay permission issued for restored approval %d (id=%d, err=%v)", allowedID, id, err)
	}
	if id, err := d.ConsumeTaskApprovalReplay(key, "Bash", []byte(`{"command":"whoami"}`)); err != nil || id != 0 {
		t.Fatalf("replay permission issued for restored timeout %d (id=%d, err=%v)", pendingID, id, err)
	}
}

func TestArchiveStubDeleteLeavesNoGrantsForReusedID(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	if err := d.EnsureLLMRecordsTable(); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureLLMUsageTable(); err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTaskWithOptions("archive stub grants", "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprint(task.ID)
	defer func() {
		_, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM task_tool_grants WHERE task_id=$1`, key)
		_ = d.DeleteTask(task.ID)
	}()
	archive, _, _ := archiveFixtureCycle(t, d, task.ID)
	// Rows an older build left behind for the stub.
	if _, err := d.Exec(`INSERT INTO task_tool_grants(task_id, scope, tool_name) VALUES ($1,'all','')`, key); err != nil {
		t.Fatal(err)
	}
	if _, err := d.QueueTaskArchiveDelete(archive.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := d.ClaimTaskArchiveJob(t.Context()); err != nil || claimed == nil || claimed.State != Deleting {
		t.Fatalf("delete claim = %+v, %v", claimed, err)
	}
	if err := d.DeleteTaskArchiveStub(archive.ID); err != nil {
		t.Fatal(err)
	}
	if n := countGrants(t, d, task.ID); n != 0 {
		t.Fatalf("deleted stub left %d grants", n)
	}
	// A grant planted for the free id is also dropped when the id is handed out.
	if _, err := d.Exec(`INSERT INTO task_tool_grants(task_id, scope, tool_name) VALUES ($1,'all','')`, key); err != nil {
		t.Fatal(err)
	}
	reused, err := d.CreateTaskWithOptions("archive stub reuse", "goal", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.DeleteTask(reused.ID) }()
	if reused.ID != task.ID {
		t.Skipf("id %d not reused (got %d)", task.ID, reused.ID)
	}
	if n := countGrants(t, d, reused.ID); n != 0 {
		t.Fatalf("new task inherited %d grants", n)
	}
}

func TestDeletedTaskIDDoesNotKeepConversationBinding(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer d.Close()
	first, err := d.CreateTask("binding-first", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(first.ID) })
	second, err := d.CreateTask("binding-second", "goal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(second.ID) })
	both, err := d.CreateConversation("test", "binding both", nil)
	if err != nil {
		t.Fatal(err)
	}
	only, err := d.CreateConversation("test", "binding only", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM conversations WHERE id IN ($1,$2)`, both.ID, only.ID)
	})
	if err := d.BindConversationTasks(both.ID, []int64{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	if err := d.BindConversationTasks(only.ID, []int64{first.ID}); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteTask(first.ID); err != nil {
		t.Fatal(err)
	}
	// Id 0 names no task: the conversation neither follows the id to a later
	// task nor becomes an unbound chat.
	if ids, err := d.ConversationTasks(both.ID); err != nil || len(ids) != 2 || ids[0] != 0 || ids[1] != second.ID {
		t.Fatalf("shared binding = %v, %v; want [0 %d]", ids, err, second.ID)
	}
	if ids, err := d.ConversationTasks(only.ID); err != nil || len(ids) != 1 || ids[0] != 0 {
		t.Fatalf("binding of the deleted task = %v, %v; want [0]", ids, err)
	}
}
