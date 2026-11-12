package guard

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
)

// Monitor starts a host shell command outside the controls that apply to Bash,
// so the gate refuses it whatever is configured and records the attempt.
func TestMonitorNeverRuns(t *testing.T) {
	g := New()
	blocked, message, _ := g.Hooks().PreToolUse(context.Background(), "Monitor", []byte(`{"command":"echo fixture-monitor"}`))
	if !blocked || !strings.Contains(message, "Bash") {
		t.Fatalf("Monitor was not refused with a pointer to Bash: blocked=%v message=%q", blocked, message)
	}
	audit := g.Audit()
	if len(audit) != 1 || audit[0].Tool != "Monitor" || audit[0].Action != "block" || audit[0].Command != "echo fixture-monitor" {
		t.Fatalf("refused Monitor call is not recorded with its command: %+v", audit)
	}
	if blocked, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", []byte(`{"command":"echo fixture-monitor","run_in_background":true}`)); blocked {
		t.Fatal("the Bash replacement for Monitor was refused as well")
	}
}

// The audit line of shell_send shows every channel that writes to the session.
func TestShellSendAuditShowsEveryChannel(t *testing.T) {
	g := New()
	g.Hooks().PreToolUse(context.Background(), "shell_send", []byte(`{"session_id":"sh_1","text":"echo fixture","keys":["up","enter"],"hex":["0x72","0x6d"],"paste":"pasted fixture"}`))
	g.Hooks().PreToolUse(context.Background(), "shell_send", []byte(`{"session_id":"sh_1","text":"echo fixture","submit":true}`))
	audit := g.Audit()
	if len(audit) != 2 {
		t.Fatalf("audit entries: %+v", audit)
	}
	for _, part := range []string{"echo fixture", "up enter", "0x72 0x6d", "pasted fixture"} {
		if !strings.Contains(audit[0].Command, part) {
			t.Errorf("audit line %q does not show %q", audit[0].Command, part)
		}
	}
	if audit[1].Command != "echo fixture" {
		t.Errorf("text-only audit line changed: %q", audit[1].Command)
	}
}

// In a task the operator created with a request, shell_send input the gate
// cannot read as a command must reach a person, and a command pasted instead
// of typed meets the same rules as typed text.
func TestShellSendChannelsReachRulesAndPeople(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("shell channel fixture", "fixture", db.TaskCreateOptions{OperatorRequest: "Assess https://api.example.test only; no production writes"})
	if err != nil {
		t.Fatal(err)
	}
	taskID := strconv.FormatInt(task.ID, 10)
	defer d.DeleteTask(task.ID)
	defer d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID)
	ic := intercept.New(d)
	tools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	config := ic.GetJudgeConfig()
	defer func() { _ = ic.SetEnabledTools(tools); _ = ic.SetJudgeConfig(config) }()
	if err := ic.SetEnabledTools([]string{"Bash", "shell_open", "shell_send"}); err != nil {
		t.Fatal(err)
	}
	rule, err := d.CreateInterceptRule("shell channel fixture", "tool_input", "regex", `\bshred\s`, "deny", "", 1000000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteInterceptRule(rule.ID)
	ic.Invalidate()
	reviews := 0
	ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
		reviews++
		return intercept.Decision{Action: "allow"}, nil
	})
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true, AskTimeoutSeconds: 1, AskTimeoutAction: "allow"}); err != nil {
		t.Fatal(err)
	}
	g := NewWithInterceptor(ic)
	for _, input := range []string{
		`{"session_id":"sh_1","hex":["0x73","0x68","0x72","0x65","0x64","0x20","0x78","0x0d"]}`,
		`{"session_id":"sh_1","keys":["up","enter"]}`,
		`{"session_id":"sh_1","text":"\u001b[A","submit":true}`,
	} {
		for _, response := range []string{"allow", "deny", "no-decision"} {
			t.Run(input+"/"+response, func(t *testing.T) {
				base, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
				defer cancel()
				var pendingID int64
				ctx := intercept.WithTaskContext(base, taskID, "fixture", func(activity db.Activity) {
					if activity.Kind != "intercept_request" {
						return
					}
					var detail struct {
						ID int64 `json:"pending_id"`
					}
					if err := json.Unmarshal([]byte(activity.Detail), &detail); err != nil {
						t.Error(err)
						return
					}
					pendingID = detail.ID
					if response != "no-decision" {
						if err := ic.Decide(pendingID, response == "allow"); err != nil {
							t.Error(err)
						}
					}
				})
				blocked, _, _ := g.Hooks().PreToolUse(ctx, "shell_send", []byte(input))
				if pendingID == 0 {
					t.Fatal("no pending approval was created")
				}
				if blocked == (response == "allow") {
					t.Fatalf("dispatch does not follow the human decision: blocked=%v response=%s", blocked, response)
				}
			})
		}
	}
	if reviews != 0 {
		t.Fatal("model reviewer was consulted for input that needs a person")
	}
	ctx := intercept.WithTaskContext(t.Context(), taskID, "fixture", func(db.Activity) {})
	if blocked, _, _ := g.Hooks().PreToolUse(ctx, "shell_send", []byte(`{"session_id":"sh_1","paste":"/usr/bin/shr'e'd fixture\n"}`)); !blocked {
		t.Fatal("a pasted command escaped the rule that denies it when typed")
	}
	// Plain typed text keeps being answered by the task request and the reviewer.
	if blocked, _, _ := g.Hooks().PreToolUse(ctx, "shell_send", []byte(`{"session_id":"sh_1","text":"echo fixture","submit":true}`)); blocked {
		t.Fatal("plain typed text was no longer answered by the task request")
	}
	// Monitor is refused with the interceptor attached too, and nothing is asked.
	if blocked, _, _ := g.Hooks().PreToolUse(ctx, "Monitor", []byte(`{"command":"echo fixture-monitor"}`)); !blocked {
		t.Fatal("Monitor ran past the gate")
	}
}
