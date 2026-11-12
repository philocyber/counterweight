package db

import (
	"fmt"
	"github.com/Philocyber/counterweight/locale"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTaskLanguageAtomicCreation(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	marker := fmt.Sprintf("locale_atomic_%d", time.Now().UnixNano())
	// Fail only this test task's metadata write, within an isolated test database.
	// The trigger does not affect other settings or tasks and is removed on return.
	function := marker + "_fn"
	trigger := marker + "_tr"
	query := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.key LIKE 'task_language.%%' AND EXISTS(SELECT 1 FROM tasks WHERE description='%s' AND NEW.key='task_language.'||id::text) THEN
 RAISE EXCEPTION 'injected task language failure'; END IF; RETURN NEW; END $$`, function, marker)
	if _, err = d.Exec(query); err != nil {
		t.Fatal(err)
	}
	defer d.Exec("DROP FUNCTION " + function + "()")
	if _, err = d.Exec("CREATE TRIGGER " + trigger + " BEFORE INSERT ON settings FOR EACH ROW EXECUTE FUNCTION " + function + "()"); err != nil {
		t.Fatal(err)
	}
	defer d.Exec("DROP TRIGGER " + trigger + " ON settings")
	task, err := d.CreateTaskWithOptions(marker, "no network target", TaskCreateOptions{Language: locale.En})
	if err == nil || task != nil || !strings.Contains(err.Error(), "injected task language failure") {
		t.Fatalf("expected atomic metadata failure: task=%v err=%v", task, err)
	}
	for _, table := range []string{"tasks", "explorations"} {
		var count int
		if err = d.QueryRow("SELECT count(*) FROM "+table+" WHERE description=$1", marker).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial %s remained: count=%d err=%v", table, count, err)
		}
	}
}

func TestTaskLanguageSavedAndRemovedWithTask(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("language persistence", "no network target", TaskCreateOptions{Language: locale.En})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	key := "task_language." + strconv.FormatInt(task.ID, 10)
	value, ok, err := d.GetSetting(key)
	if err != nil || !ok || value != "en" {
		t.Fatalf("language missing: %q %v %v", value, ok, err)
	}
	if err = d.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = d.GetSetting(key); err != nil || ok {
		t.Fatalf("language remained after task deletion: %v %v", ok, err)
	}
}

// TestTaskLanguageSurvivesStaleSettingRow reproduces the settings_pkey crash:
// a task id is reused, but an orphaned task_language.<id> row from a prior task
// still exists. Creating the new task must overwrite it, not fail.
func TestTaskLanguageSurvivesStaleSettingRow(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("stale lang first", "no network target", TaskCreateOptions{Language: locale.En})
	if err != nil {
		t.Fatal(err)
	}
	key := "task_language." + strconv.FormatInt(task.ID, 10)
	// Drop only the task row, leaving the settings row orphaned (as a pre-cleanup
	// deletion did). nextReusableTaskID will hand the same id to the next task.
	if _, err = d.Exec("DELETE FROM tasks WHERE id=$1", task.ID); err != nil {
		t.Fatal(err)
	}
	// Mark the orphan row so the overwrite is observable.
	if err = d.SetSetting(key, "stale"); err != nil {
		t.Fatal(err)
	}
	reused, err := d.CreateTaskWithOptions("stale lang reused", "no network target", TaskCreateOptions{Language: locale.En})
	if err != nil {
		t.Fatalf("create with stale language row failed: %v", err)
	}
	defer d.DeleteTask(reused.ID)
	if reused.ID != task.ID {
		t.Fatalf("expected id reuse %d, got %d", task.ID, reused.ID)
	}
	value, ok, err := d.GetSetting(key)
	if err != nil || !ok || value != "en" {
		t.Fatalf("language not overwritten: %q %v %v", value, ok, err)
	}
}

// A review-target row must not survive its task or carry over to a reused task id.
func TestTaskCodeReviewTargetRemovedWithTaskAndOnReuse(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("code review target", "no network target", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	key := TaskCodeReviewKey(task.ID)
	if err = d.SetSetting(key, `{"repository":"acme/api"}`); err != nil {
		t.Fatal(err)
	}
	if err = d.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.GetSetting(key); err != nil || ok {
		t.Fatalf("review target remained after task deletion: %v %v", ok, err)
	}
	// Simulate a row orphaned before cleanup existed, then reuse the id.
	if err = d.SetSetting(key, `{"repository":"acme/api"}`); err != nil {
		t.Fatal(err)
	}
	reused, err := d.CreateTaskWithOptions("plain task", "no network target", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(reused.ID)
	if reused.ID != task.ID {
		t.Skipf("id not reused (%d vs %d); orphan path not exercised", reused.ID, task.ID)
	}
	if _, ok, err := d.GetSetting(key); err != nil || ok {
		t.Fatalf("stale review target attached to a reused task id: %v %v", ok, err)
	}
}
