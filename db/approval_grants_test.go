package db

import "testing"

func TestApprovalGrantAtomicResolution(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task := "approval-atomic-fixture"
	defer d.Exec(`DELETE FROM task_tool_grants WHERE task_id=$1`, task)
	defer d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, task)
	id, err := d.CreateInterceptPending(0, 0, task, "test", "Write", []byte(`{"content":"test"}`), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	g := &TaskToolGrant{TaskID: task, Scope: "input", ToolName: "Write", Command: `{"content":"test"}`}
	ok, err := d.ResolveIntercept(id, "denied", "deny", "denied")
	if err != nil || !ok {
		t.Fatalf("deny: %v %v", ok, err)
	}
	ok, err = d.ResolveIntercept(id, "allowed", "allow", "stale allow", g)
	if err != nil || ok {
		t.Fatalf("stale allow: %v %v", ok, err)
	}
	grants, err := d.ListTaskToolGrants(task)
	if err != nil || len(grants) != 0 {
		t.Fatalf("stale decision persisted a grant: %v %v", grants, err)
	}
	id, err = d.CreateInterceptPending(0, 0, task, "test", "Write", []byte(`{}`), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	bad := *g
	bad.Scope = "invalid-scope"
	if ok, err = d.ResolveIntercept(id, "allowed", "allow", "failed grant", &bad); err == nil || ok {
		t.Fatal("invalid grant must roll back")
	}
	pending, err := d.GetInterceptPending(id)
	if err != nil || pending.Status != "pending" {
		t.Fatal("failed grant settled approval")
	}

	ok, err = d.ResolveIntercept(id, "allowed", "allow", "allowed", g)
	if err != nil || !ok {
		t.Fatalf("allow: %v %v", ok, err)
	}
	grants, err = d.ListTaskToolGrants(task)
	if err != nil || len(grants) != 1 {
		t.Fatalf("grant missing: %v %v", grants, err)
	}
}

func TestRetiredAssetDefaults(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	if err = d.QueryRow(`SELECT count(*) FROM asset_intercept_rules WHERE builtin AND pattern IN ('.gov.cn','.edu.cn')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("retired defaults: %d %v", n, err)
	}
}
