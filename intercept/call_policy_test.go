package intercept

import (
	"context"
	"github.com/Philocyber/counterweight/db"
	"testing"
)

func TestCallPatternPolicyDenyAndRemember(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("test database not configured")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	tool := "ApprovalPolicyFixture"
	add := func(target, pattern, action string, priority int) {
		t.Helper()
		r, err := d.CreateInterceptRule("approval policy fixture", target, "string", pattern, action, "fixture", priority, true, false, 0, "deny")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.DeleteInterceptRule(r.ID) })
	}
	add("tool_call", `{"tool":"ApprovalPolicyFixture"}`, "allow", 100000)
	add("tool_call", `{"tool":"ApprovalPolicyFixture","arguments":{"operation":"delete"}}`, "deny", -100000)
	i := New(d)
	if dec, ok := i.Match(tool, []byte(`{"operation":"delete"}`)); !ok || dec.Action != "deny" {
		t.Fatalf("broad allow bypassed denial: %+v %v", dec, ok)
	}
	if dec, ok := i.Match(tool, []byte(`{"operation":"read"}`)); !ok || dec.Action != "allow" {
		t.Fatalf("allowed read: %+v %v", dec, ok)
	}
	add("tool_call", `{"tool":"ApprovalPolicyFixture","arguments":{"operation":"review"}}`, "ask", 100001)
	i.Invalidate()
	if dec, ok := i.Match(tool, []byte(`{"operation":"review"}`)); !ok || dec.Action != "ask" || dec.RememberAllowed {
		t.Fatalf("structured ask must remain human: %+v %v", dec, ok)
	}
	add("tool_name", "LegacyApprovalFixture", "ask", 100000)
	i.Invalidate()
	if dec, ok := i.Match("LegacyApprovalFixture", []byte(`{}`)); !ok || !dec.RememberAllowed {
		t.Fatalf("legacy tool prompt must support remembering: %+v %v", dec, ok)
	}
	task := "approval-policy-fixture"
	defer d.Exec(`DELETE FROM task_tool_grants WHERE task_id=$1`, task)
	g, err := d.SaveTaskToolGrant(task, "input", tool, `{"operation":"read"}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithTaskContext(context.Background(), task, "test", nil)
	if !i.Granted(ctx, []string{tool}, []byte(`{"operation":"read"}`)) {
		t.Fatal("saved approval missed")
	}
	if i.Granted(WithTaskContext(context.Background(), task+"other", "test", nil), []string{tool}, []byte(`{"operation":"read"}`)) {
		t.Fatal("grant leaked across tasks")
	}
	if _, err = d.DeleteTaskToolGrant(task, g.ID); err != nil {
		t.Fatal(err)
	}
	i.Invalidate()
	if i.Granted(ctx, []string{tool}, []byte(`{"operation":"read"}`)) {
		t.Fatal("revoked approval still active")
	}
}
