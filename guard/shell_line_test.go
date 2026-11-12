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

// shellLineFixture is a task the operator created with a request, and a model
// reviewer that allows whatever the rules leave to it. A call is therefore
// blocked only by a rule that denies it or by a person who does not approve.
type shellLineFixture struct {
	t       *testing.T
	d       *db.DB
	ic      *intercept.Interceptor
	g       *Guard
	taskID  string
	human   string // how a person answers the next request: allow, deny, or not at all
	pending int64  // the request the last call raised, if any
	reviews int    // calls answered by the model reviewer
}

func newShellLineFixture(t *testing.T) *shellLineFixture {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	task, err := d.CreateTaskWithOptions("shell line fixture", "fixture", db.TaskCreateOptions{OperatorRequest: "Assess https://api.example.test only; no production writes"})
	if err != nil {
		t.Fatal(err)
	}
	f := &shellLineFixture{t: t, d: d, ic: intercept.New(d), taskID: strconv.FormatInt(task.ID, 10)}
	t.Cleanup(func() {
		d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, f.taskID)
		d.DeleteTask(task.ID)
	})
	tools, err := f.ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	config := f.ic.GetJudgeConfig()
	t.Cleanup(func() { _ = f.ic.SetEnabledTools(tools); _ = f.ic.SetJudgeConfig(config) })
	if err := f.ic.SetEnabledTools([]string{"Bash", "shell_open", "shell_send"}); err != nil {
		t.Fatal(err)
	}
	if err := f.ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true, AskTimeoutSeconds: 1, AskTimeoutAction: "allow"}); err != nil {
		t.Fatal(err)
	}
	f.ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
		f.reviews++
		return intercept.Decision{Action: "allow"}, nil
	})
	f.g = NewWithInterceptor(f.ic)
	return f
}

// deny adds a rule that denies input matching the pattern until t ends.
func (f *shellLineFixture) deny(t *testing.T, pattern string) {
	rule, err := f.d.CreateInterceptRule("shell line fixture", "tool_input", "regex", pattern, "deny", "", 1000000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	f.ic.Invalidate()
	t.Cleanup(func() { f.d.DeleteInterceptRule(rule.ID); f.ic.Invalidate() })
}

// run starts a new agent run of the fixture task.
func (f *shellLineFixture) run() context.Context {
	ctx := intercept.WithTaskContext(f.t.Context(), f.taskID, "fixture", func(activity db.Activity) {
		if activity.Kind != "intercept_request" {
			return
		}
		var detail struct {
			ID int64 `json:"pending_id"`
		}
		if err := json.Unmarshal([]byte(activity.Detail), &detail); err != nil {
			f.t.Error(err)
			return
		}
		f.pending = detail.ID
		if f.human == "allow" || f.human == "deny" {
			if err := f.ic.Decide(f.pending, f.human == "allow"); err != nil {
				f.t.Error(err)
			}
		}
	})
	ctx, _ = intercept.WithTrace(ctx, "", nil)
	return ctx
}

// send passes one shell_send call through the gate the way the agent loop
// does: the hook before the call and, when the call is let through, the hook
// after it ran. human says how a person answers if one is asked: "allow",
// "deny", or "silent" for no answer. With "" no person is expected.
func (f *shellLineFixture) send(run context.Context, input, human string) (blocked bool, message string) {
	f.human, f.pending = human, 0
	timeout := 30 * time.Second
	if human == "silent" {
		timeout = 300 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(run, timeout)
	defer cancel()
	blocked, message, _ = f.g.Hooks().PreToolUse(ctx, "shell_send", []byte(input))
	if !blocked {
		f.g.Hooks().PostToolUse(ctx, "shell_send", []byte(input), []byte(`"{}"`), false)
	}
	return blocked, message
}

// command is the command text the gate recorded for the last call.
func (f *shellLineFixture) command() string {
	audit := f.g.Audit()
	for n := len(audit) - 1; n >= 0; n-- {
		if audit[n].Action == "allow" {
			return audit[n].Command
		}
	}
	return ""
}

// request returns the rule and the input shown to the person asked last.
func (f *shellLineFixture) request(t *testing.T) (rule string, input map[string]any) {
	t.Helper()
	row, err := f.d.GetInterceptDetail(f.pending)
	if err != nil || row == nil || row.Audit == nil {
		t.Fatalf("approval request missing: %v", err)
	}
	if err := json.Unmarshal(row.ToolInput, &input); err != nil {
		t.Fatal(err)
	}
	return row.Audit.RuleName, input
}

func deniedByRule(blocked bool, message string) bool {
	return blocked && strings.Contains(message, "shell line fixture")
}

// A command built up over several calls, or over two channels of one call,
// meets the rule that denies it: at the call that puts its last piece on the
// line, and at the call that submits a line already there.
func TestSplitShellLineMeetsTheRuleThatDeniesIt(t *testing.T) {
	f := newShellLineFixture(t)
	t.Run("text then text with submit", func(t *testing.T) {
		f.deny(t, `\brm\s`)
		run := f.run()
		if blocked, message := f.send(run, `{"session_id":"sh_1","text":"r"}`, ""); blocked {
			t.Fatalf("the first piece was blocked: %s", message)
		}
		if blocked, message := f.send(run, `{"session_id":"sh_1","text":"m -rf /fixture","submit":true}`, ""); !deniedByRule(blocked, message) {
			t.Fatalf("the call that completes the line was not denied: blocked=%v %s", blocked, message)
		}
		if got := f.command(); got != "rm -rf /fixture" {
			t.Errorf("recorded command is not the whole line: %q", got)
		}
	})
	t.Run("text and paste then Enter", func(t *testing.T) {
		// With the rule in place the command does not get onto the line.
		f.deny(t, `\brm\s`)
		if blocked, message := f.send(f.run(), `{"session_id":"sh_1","text":"r","paste":"m -rf /fixture"}`, ""); !deniedByRule(blocked, message) {
			t.Fatalf("typing and pasting the command in one call was not denied: blocked=%v %s", blocked, message)
		}
	})
	// A line that reached the terminal before the rule existed is denied by
	// the later call that only presses Enter.
	for name, typed := range map[string]string{
		"text and paste before the rule then Enter": `{"session_id":"sh_1","text":"fixture","paste":"tool -x /fixture"}`,
		"text before the rule then Enter":           `{"session_id":"sh_1","text":"fixturetool -x /fixture"}`,
	} {
		t.Run(name, func(t *testing.T) {
			run := f.run()
			if blocked, message := f.send(run, typed, ""); blocked {
				t.Fatalf("typing was blocked without the rule: %s", message)
			}
			f.deny(t, `\bfixturetool\s`)
			if blocked, message := f.send(run, `{"session_id":"sh_1","keys":["enter"]}`, ""); !deniedByRule(blocked, message) {
				t.Fatalf("Enter on the typed line was not denied: blocked=%v %s", blocked, message)
			}
			if got := f.command(); got != "fixturetool -x /fixture [keys: enter]" {
				t.Errorf("recorded command is not the whole line: %q", got)
			}
		})
	}
	t.Run("text then Enter", func(t *testing.T) {
		f.deny(t, `\brm\s`)
		run := f.run()
		if blocked, message := f.send(run, `{"session_id":"sh_1","text":"echo safe"}`, ""); blocked {
			t.Fatalf("harmless text was blocked: %s", message)
		}
		if blocked, message := f.send(run, `{"session_id":"sh_1","keys":["enter"]}`, ""); blocked {
			t.Fatalf("Enter on a harmless line was blocked: %s", message)
		}
		if got := f.command(); got != "echo safe [keys: enter]" {
			t.Errorf("the Enter call does not show the line it submits: %q", got)
		}
		// The line ended: the next Enter is on an empty line again.
		if blocked, _ := f.send(run, `{"session_id":"sh_1","keys":["enter"]}`, ""); blocked || f.command() != " [keys: enter]" {
			t.Errorf("Enter on an empty line changed: blocked=%v command=%q", blocked, f.command())
		}
		// The denied command is refused when typed, before any Enter.
		if blocked, message := f.send(run, `{"session_id":"sh_1","text":"rm -rf /fixture"}`, ""); !deniedByRule(blocked, message) {
			t.Fatalf("typing the denied command was not refused: blocked=%v %s", blocked, message)
		}
	})
	t.Run("text, cursor key and paste then Enter", func(t *testing.T) {
		f.deny(t, `\brm\s`)
		run := f.run()
		reviews := f.reviews
		// The key moves the cursor, so where the paste lands is not followed:
		// a person decides this call and every later one up to the line end.
		if blocked, message := f.send(run, `{"session_id":"sh_1","text":"m -rf /fixture","keys":["home"],"paste":"r"}`, "allow"); blocked {
			t.Fatalf("the approved call was blocked: %s", message)
		}
		if rule, _ := f.request(t); rule != "Unresolved shell program" {
			t.Fatalf("the edited line was not put to a person as not inspectable: %q", rule)
		}
		for _, answer := range []string{"silent", "deny"} {
			if blocked, _ := f.send(run, `{"session_id":"sh_1","keys":["enter"]}`, answer); !blocked || f.pending == 0 {
				t.Fatalf("Enter on the edited line ran without a person approving it: blocked=%v pending=%d answer=%s", blocked, f.pending, answer)
			}
		}
		if f.reviews != reviews {
			t.Fatal("the model reviewer answered for a line that needs a person")
		}
	})
}

// A denied call sends nothing, so the line stays as it was: pressing Enter
// afterwards submits only what was typed before, and the denied piece is
// denied again whenever it is retried.
func TestDeniedShellLineEndLeavesThePendingLine(t *testing.T) {
	f := newShellLineFixture(t)
	f.deny(t, `\brm\s`)
	run := f.run()
	last := `{"session_id":"sh_1","text":"m -rf /fixture","submit":true}`
	if blocked, message := f.send(run, `{"session_id":"sh_1","text":"r"}`, ""); blocked {
		t.Fatalf("the first piece was blocked: %s", message)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if blocked, message := f.send(run, last, ""); !deniedByRule(blocked, message) {
			t.Fatalf("the call that completes the line was not denied: blocked=%v %s", blocked, message)
		}
	}
	if blocked, message := f.send(run, `{"session_id":"sh_1","keys":["enter"]}`, ""); blocked {
		t.Fatalf("Enter on the line the denied call left behind was blocked: %s", message)
	}
	if got := f.command(); got != "r [keys: enter]" {
		t.Fatalf("the denied call changed the pending line: %q", got)
	}
	// That Enter ended the line, so the piece alone is a different command now.
	if blocked, message := f.send(run, last, ""); blocked || f.command() != "m -rf /fixture" {
		t.Fatalf("the piece on an empty line: blocked=%v command=%q %s", blocked, f.command(), message)
	}
}

// Once a key has edited the line, the gate no longer knows what is on it. The
// call that ends the line needs a person although every piece looks harmless,
// and the model reviewer does not answer in that person's place.
func TestEditedShellLineNeedsAPersonAtItsEnd(t *testing.T) {
	f := newShellLineFixture(t)
	f.deny(t, `\brm\s`)
	run := f.run()
	if blocked, message := f.send(run, `{"session_id":"sh_1","text":"fixture-data","keys":["left","left"]}`, "allow"); blocked {
		t.Fatalf("the approved call was blocked: %s", message)
	}
	reviews := f.reviews
	for _, harmless := range []string{
		`{"session_id":"sh_1","text":"echo "}`,
		`{"session_id":"sh_1","keys":["enter"]}`,
		`{"session_id":"sh_1","text":"echo fixture","submit":true}`,
	} {
		if blocked, _ := f.send(run, harmless, "silent"); !blocked || f.pending == 0 {
			t.Fatalf("a call on the edited line ran without a person: %s", harmless)
		}
		rule, input := f.request(t)
		if rule != "Unresolved shell program" || input["line_not_inspectable"] != true {
			t.Fatalf("the request does not say the line is not inspectable: %q %v", rule, input)
		}
		if !strings.HasPrefix(f.command(), "[line not inspectable] ") {
			t.Errorf("the recorded command does not say the line is not inspectable: %q", f.command())
		}
	}
	if f.reviews != reviews {
		t.Fatal("the model reviewer answered for a line that needs a person")
	}
	// Another session of the run is not affected.
	if blocked, message := f.send(run, `{"session_id":"sh_2","text":"echo fixture","submit":true}`, ""); blocked || f.pending != 0 {
		t.Fatalf("another session inherited the edited line: %s", message)
	}
	// Ending the edited line does not make the session known again: the line
	// that ran unread may have left a command open. An interrupt does.
	if blocked, message := f.send(run, `{"session_id":"sh_1","keys":["enter"]}`, "allow"); blocked {
		t.Fatalf("the approved line end was blocked: %s", message)
	}
	if blocked, _ := f.send(run, `{"session_id":"sh_1","text":"echo fixture","submit":true}`, "deny"); !blocked {
		t.Fatal("the line after the edited one was taken as a new line")
	}
	f.pending = 0
	if blocked, message := f.send(run, `{"session_id":"sh_1","keys":["ctrl+c"]}`, ""); blocked || f.pending != 0 {
		t.Fatalf("the interrupt needed a person: %s", message)
	}
	if blocked, message := f.send(run, `{"session_id":"sh_1","text":"echo fixture","submit":true}`, ""); blocked || f.pending != 0 {
		t.Fatalf("the line after the interrupt still needs a person: %s", message)
	}
}

// Two runs have their own shell sessions even when the ids are the same, so
// what one run typed is not on the other run's line.
func TestShellLinesOfTwoRunsStayApart(t *testing.T) {
	f := newShellLineFixture(t)
	f.deny(t, `\brm\s`)
	first, second := f.run(), f.run()
	last := `{"session_id":"sh_1","text":"m -rf /fixture","submit":true}`
	if blocked, message := f.send(first, `{"session_id":"sh_1","text":"r"}`, ""); blocked {
		t.Fatalf("the first piece was blocked: %s", message)
	}
	if blocked, message := f.send(second, last, ""); blocked || f.command() != "m -rf /fixture" {
		t.Fatalf("the second run saw the first run's line: blocked=%v command=%q %s", blocked, f.command(), message)
	}
	if blocked, message := f.send(first, last, ""); !deniedByRule(blocked, message) {
		t.Fatalf("the first run lost its line: blocked=%v %s", blocked, message)
	}
}

// A call that paused its task for approval has sent nothing. The request shows
// the whole line, and when the approved call is repeated it runs once, its
// text is on the line once, and the approval is used up.
func TestApprovedShellReplayCountsOnce(t *testing.T) {
	f := newShellLineFixture(t)
	f.deny(t, `\brm\s`)
	id, _ := strconv.ParseInt(f.taskID, 10, 64)
	f.ic.SetTaskApprovalHandler(func(_ string, pendingID int64, expires *time.Time, action string) (bool, error) {
		return f.d.BeginTaskApprovalHold(id, pendingID, expires, action)
	})
	run := f.run()
	typed := `{"session_id":"sh_1","text":"echo fixture; "}`
	if blocked, message := f.send(run, typed, ""); blocked {
		t.Fatalf("harmless text was blocked: %s", message)
	}
	// The program is chosen at run time, so a person decides and the task waits.
	held := `{"session_id":"sh_1","text":"\"$FIXTURE_PROGRAM\" data"}`
	blocked, message := f.send(run, held, "silent")
	if !blocked || !strings.Contains(message, intercept.ApprovalDeferredMarker) || f.pending == 0 {
		t.Fatalf("the call did not wait for approval: blocked=%v pending=%d %s", blocked, f.pending, message)
	}
	approval := f.pending
	if _, input := f.request(t); input["line_before"] != "echo fixture; " || input["text"] != `"$FIXTURE_PROGRAM" data` {
		t.Fatalf("the request does not show the whole line: %v", input)
	}
	if err := f.ic.Decide(approval, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Exec(`UPDATE task_approval_holds SET ended_at=now(),outcome='resolved' WHERE task_id=$1 AND ended_at IS NULL`, id); err != nil {
		t.Fatal(err)
	}
	f.ic.SetTaskApprovalHandler(nil)
	if blocked, message := f.send(run, held, "silent"); blocked || f.pending != 0 {
		t.Fatalf("the approved call did not run on its repeat: blocked=%v pending=%d %s", blocked, f.pending, message)
	}
	// The line holds the approved text once.
	if blocked, _ := f.send(run, `{"session_id":"sh_1","keys":["enter"]}`, "deny"); !blocked || f.pending == 0 {
		t.Fatalf("Enter on a line that needs a person ran: blocked=%v", blocked)
	}
	if got := f.command(); got != `echo fixture; "$FIXTURE_PROGRAM" data [keys: enter]` {
		t.Fatalf("the repeated call is not on the line exactly once: %q", got)
	}
	// Back on the same line, the same call needs a new approval.
	for _, again := range []string{`{"session_id":"sh_1","keys":["ctrl+c"]}`, typed} {
		if blocked, message := f.send(run, again, ""); blocked {
			t.Fatalf("clearing and retyping the line was blocked: %s: %s", again, message)
		}
	}
	if blocked, _ := f.send(run, held, "silent"); !blocked || f.pending == 0 || f.pending == approval {
		t.Fatalf("one approval let the call run twice: blocked=%v pending=%d", blocked, f.pending)
	}
	if _, input := f.request(t); input["line_before"] != "echo fixture; " || input["text"] != `"$FIXTURE_PROGRAM" data` {
		t.Fatalf("the second request is for a different line: %v", input)
	}
}

// A command sent whole in one call is decided and recorded as before.
func TestWholeShellCommandInOneCallIsUnchanged(t *testing.T) {
	f := newShellLineFixture(t)
	f.deny(t, `\brm\s`)
	run := f.run()
	input := `{"session_id":"sh_1","text":"echo ok","submit":true}`
	for attempt := 0; attempt < 2; attempt++ {
		reviews := f.reviews
		if blocked, message := f.send(run, input, ""); blocked || f.pending != 0 || f.reviews != reviews+1 {
			t.Fatalf("the command was not answered by the task request and the reviewer: blocked=%v pending=%d %s", blocked, f.pending, message)
		}
		if got := f.command(); got != "echo ok" {
			t.Fatalf("recorded command changed: %q", got)
		}
		var recorded, status string
		if err := f.d.QueryRow(`SELECT tool_input::text, status FROM intercept_pending WHERE task_id=$1 ORDER BY id DESC LIMIT 1`, f.taskID).Scan(&recorded, &status); err != nil {
			t.Fatal(err)
		}
		if !intercept.SameArguments([]byte(recorded), []byte(input)) || status != "allowed" {
			t.Fatalf("recorded decision changed: %s %s", status, recorded)
		}
	}
	if blocked, message := f.send(run, `{"session_id":"sh_1","text":"rm -rf /fixture","submit":true}`, ""); !deniedByRule(blocked, message) {
		t.Fatalf("the denied command sent whole was not denied: blocked=%v %s", blocked, message)
	}
}
