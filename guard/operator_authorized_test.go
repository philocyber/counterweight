package guard

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
)

// In a task the operator created with a request, a shell call the gate cannot
// inspect must still reach a person, whether the model reviewer is off or
// would allow it. Only the generic prompt is answered by the task request.
func TestUninspectableShellRequiresHumanInOperatorAuthorizedTask(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("operator authorized fixture", "fixture", db.TaskCreateOptions{OperatorRequest: "Assess https://api.example.test only; no production writes"})
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
	rule, err := d.CreateInterceptRule("operator authorized fixture", "tool_input", "string", "absent fixture restriction", "deny", "", 1000000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteInterceptRule(rule.ID)
	g := NewWithInterceptor(ic)
	authorization, err := ic.TaskOperatorAuthorization(intercept.WithTaskContext(t.Context(), taskID, "fixture", func(db.Activity) {}))
	if err != nil || !authorization.Approved() {
		t.Fatalf("fixture task is not operator-authorized: %v", err)
	}
	for _, reviewer := range []string{"off", "allow"} {
		reviews := 0
		ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
			reviews++
			return intercept.Decision{Action: "allow"}, nil
		})
		if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: reviewer == "allow", AskTimeoutSeconds: 1, AskTimeoutAction: "allow"}); err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{
			`"$FIXTURE_PROGRAM" harmless-data`,
			`curl -X "$FIXTURE_METHOD" https://fixture.invalid/resource`,
			`bash -c "$FIXTURE_PROGRAM"`,
			`printf '%s' "$FIXTURE_PROGRAM" | bash`,
		} {
			for _, response := range []string{"allow", "deny", "no-decision"} {
				t.Run(reviewer+"/"+command+"/"+response, func(t *testing.T) {
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
					input, _ := json.Marshal(map[string]string{"command": command})
					blocked, _, _ := g.Hooks().PreToolUse(ctx, "Bash", input)
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
			t.Fatal("model reviewer was consulted for a call that needs a person")
		}
	}
	// The generic prompt keeps being answered by the task request.
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{}); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"command": "echo fixture"})
	if blocked, _, _ := g.Hooks().PreToolUse(intercept.WithTaskContext(t.Context(), taskID, "fixture", func(db.Activity) {}), "Bash", input); blocked {
		t.Fatal("generic shell prompt was no longer answered by the task request")
	}
}
