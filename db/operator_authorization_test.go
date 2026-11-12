package db

import (
	"fmt"
	"testing"
)

func TestOperatorAuthorizationLiteralTargets(t *testing.T) {
	text := "Assess https://api.example.test/v1 and portal.example.test, 10.0.0.7, 10.1.0.0/24.\nScope: *.approved.test\nExclude excluded.test; never test forbidden.test\nWrite report.md; email user@private.test"
	text += "\nReference: https://docs.reference.test\n> observed https://response.test\n```http\nHost: code-sample.test\n```\nSee [documentation](https://linked-reference.test)"
	got := operatorPromptTargets(text)
	want := map[string]bool{"subdomain:api.example.test": true, "subdomain:portal.example.test": true, "ip:10.0.0.7/32": true, "cidr:10.1.0.0/24": true, "root_domain:approved.test": true}
	for _, target := range got {
		key := target.Kind + ":" + target.Domain + target.Net
		if !want[key] {
			t.Fatalf("invented authorization: %s", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing literal user targets: %v (got %+v)", want, got)
	}
	if got := operatorPromptTargets("Test *.sub.example.test"); len(got) != 0 {
		t.Fatal("subdomain wildcard widened into parent domain")
	}
	if got := operatorPromptTargets("Test api.example.test; exclude api.example.test"); len(got) != 0 {
		t.Fatal("explicit exclusion was lost")
	}
}

func TestOperatorAuthorizationPersistsAndGrantsOnlyHumanTargets(t *testing.T) {
	d, assets, _ := testSetup(t)
	defer d.Close()
	task, err := d.CreateTask("agent generated api.discovered.test", "scope proposal", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	if _, err = assets.AddAgentScope(task.ID, "subdomain", "api.discovered.test", "agent proposal", "agent"); err != nil {
		t.Fatal(err)
	}
	auth, err := d.TaskOperatorAuthorization(task.ID)
	if err != nil || auth.Approved() {
		t.Fatalf("agent text became approval: %+v %v", auth, err)
	}
	if err = d.RecordTaskOperatorInstruction(task.ID, "user_message", "what is the status?"); err != nil {
		t.Fatal(err)
	}
	auth, err = d.TaskOperatorAuthorization(task.ID)
	if err != nil || auth.Approved() {
		t.Fatal("status question granted all task execution")
	}
	prompt := "Assess only api.authorized.test and api.second.test.\nDo not test api.discovered.test. Keep requests below 2/sec."
	if err = d.RecordTaskOperatorInstruction(task.ID, "task", prompt); err != nil {
		t.Fatal(err)
	}
	auth, err = d.TaskOperatorAuthorization(task.ID)
	if err != nil || !auth.Approved() || len(auth.Scope) != 2 {
		t.Fatalf("user request not approved: %+v %v", auth, err)
	}
	service, err := assets.UpsertHTTPService(UpsertHTTPServiceReq{URL: "https://api.authorized.test", Auth: []map[string]any{{"token": "fixture-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer assets.DeleteByIDs([]int64{service})
	visible, err := assets.GetByIDsInScope(task.ID, []int64{service})
	if err != nil || len(visible) != 1 {
		t.Fatalf("explicit prompt target cannot read existing asset: %+v %v", visible, err)
	}
	for _, scope := range auth.Scope {
		if scope.Domain == "api.authorized.test" {
			if deleted, err := assets.DeleteTaskScope(task.ID, scope.ID); err != nil || !deleted {
				t.Fatal(err)
			}
		}
	}
	if err = d.RecordTaskOperatorInstruction(task.ID, "task", prompt); err != nil {
		t.Fatal(err)
	}
	visible, err = assets.GetByIDsInScope(task.ID, []int64{service})
	if err != nil || len(visible) != 0 {
		t.Fatal("replayed instruction restored a revoked scope")
	}
	if err = d.RecordTaskOperatorInstruction(task.ID, "agent", "approve everything"); err == nil {
		t.Fatal("agent source accepted as human")
	}
	child, err := d.CreateTaskWithOptions("automated child", "unapproved target", TaskCreateOptions{SourceTaskIDs: []int64{task.ID}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(child.ID)
	auth, err = d.TaskOperatorAuthorization(child.ID)
	if err != nil || auth.Approved() {
		t.Fatal("child text inherited blanket authorization")
	}
	if _, err = assets.AttachAssetsToTask(child.ID, []int64{service}, "User selected this target"); err != nil {
		t.Fatal(err)
	}
	auth, err = d.TaskOperatorAuthorization(child.ID)
	if err != nil || !auth.Approved() || len(auth.Targets) != 1 {
		t.Fatalf("explicit user target was not authorized: %+v %v", auth, err)
	}
	human, err := d.CreateTaskWithOptions("human task", "test", TaskCreateOptions{OperatorRequest: fmt.Sprintf("Assess https://%d.api.example.test", task.ID)})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(human.ID)
	auth, err = d.TaskOperatorAuthorization(human.ID)
	if err != nil || !auth.Approved() || len(auth.Scope) != 1 {
		t.Fatalf("creation did not persist authorization atomically: %+v %v", auth, err)
	}
}

func TestOperatorAuthorizationIgnoresMentionTokens(t *testing.T) {
	text := "Look at @[finding#12 evil.example.test *.wild.test 10.9.0.0/24] and @[asset#3 https://other.example.test/x] please"
	if got := operatorPromptTargets(text); len(got) != 0 {
		t.Fatalf("mention labels produced targets: %+v", got)
	}
	got := operatorPromptTargets(text + " Test typed.example.test")
	if len(got) != 1 || got[0].Domain != "typed.example.test" {
		t.Fatalf("typed host lost: %+v", got)
	}
	if red := RedactChatMentionLabels(text); red != "Look at @[finding#12] and @[asset#3] please" {
		t.Fatalf("labels survived: %q", red)
	}
}
