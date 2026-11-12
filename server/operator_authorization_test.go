package server

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
)

func TestOperatorAuthorizationAvoidsGenericShellApproval(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	task, err := m.CreateTaskWithOptions("operator authorization fixture", "authorized read", db.TaskCreateOptions{OperatorRequest: "Assess only api.approved.test, non-destructively. Keep requests below 2/sec."})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) }()
	ic := m.interceptor
	prior := ic.GetJudgeConfig()
	tools, _ := ic.GetEnabledTools()
	defer ic.SetJudgeConfig(prior)
	defer ic.SetEnabledTools(tools)
	if err = ic.SetEnabledTools([]string{"Bash"}); err != nil {
		t.Fatal(err)
	}
	rule, err := m.pg.CreateInterceptRule("fixture restriction", "tool_input", "string", "FORBIDDEN_APPROVAL_FIXTURE", "deny", "Explicit denial", 100000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	defer m.pg.DeleteInterceptRule(rule.ID)
	ic.Invalidate()
	if err = ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true, Prompt: intercept.DefaultJudgePrompt}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ic.SetReviewer(func(ctx context.Context, _ int64, prompt string, input intercept.ReviewInput) (intercept.Decision, error) {
		calls++
		if !strings.Contains(prompt, "Assume the user completed the necessary due diligence") {
			t.Fatal("reviewer lost authorization policy")
		}
		if input.OperatorAuthorization == nil || len(input.OperatorAuthorization.Instructions) != 1 || len(input.OperatorAuthorization.Scope) != 1 || !strings.Contains(input.OperatorAuthorization.Instructions[0].Text, "below 2/sec") {
			t.Fatalf("reviewer lost original user limits: %+v", input)
		}
		return intercept.Decision{Action: "allow", ModelFallback: true, Message: "Routine authorized read"}, nil
	})
	ctx := intercept.WithTaskContext(context.Background(), task.ID, "worker", nil)
	if blocked, reason, _ := task.Guard.Hooks().PreToolUse(ctx, "Bash", []byte(`{"command":"curl https://api.approved.test"}`)); blocked {
		t.Fatalf("repeated human approval for authorized call: %s", reason)
	}
	if calls != 1 {
		t.Fatal("generic shell prompt did not use the effect reviewer")
	}
	if err = ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if blocked, reason, _ := task.Guard.Hooks().PreToolUse(ctx, "Bash", []byte(`{"command":"curl https://api.approved.test/status"}`)); blocked {
		t.Fatalf("disabled reviewer reintroduced generic approval: %s", reason)
	}
	if calls != 1 {
		t.Fatal("disabled reviewer was invoked")
	}
	if err = ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true, Prompt: intercept.DefaultJudgePrompt}); err != nil {
		t.Fatal(err)
	}

	if blocked, _, _ := task.Guard.Hooks().PreToolUse(ctx, "Bash", []byte(`{"command":"echo FORBIDDEN_APPROVAL_FIXTURE"}`)); !blocked {
		t.Fatal("authorization bypassed explicit deny")
	}
	if calls != 1 {
		t.Fatal("explicit denial was sent to model for override")
	}
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	state, err := m.pg.TaskApprovalState(id)
	if err != nil || len(state.Pending) != 0 {
		t.Fatal("approved call created a pending permission")
	}
	// Concrete effect uncertainty still waits for a human and stops task work.
	s := newAdmissionTestServer(m, nil)
	ic.SetTaskApprovalHandler(s.holdTaskForApproval)
	ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
		return intercept.Decision{Action: "ask", ModelFallback: true, Message: "Unknown effect on a real object"}, nil
	})
	base := s.engine.execContextFor(context.Background(), task.ID)
	ctx = intercept.WithTaskContext(base, task.ID, "worker", nil)
	if blocked, _, _ := task.Guard.Hooks().PreToolUse(ctx, "Bash", []byte(`{"command":"unknown-operation"}`)); !blocked || base.Err() == nil {
		t.Fatal("genuine permission wait did not stop the task")
	}
}
