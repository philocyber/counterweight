package intercept

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
)

// One call is read in the order the session receives it: text, Enter when
// submit is set, keys, raw bytes, pasted text. What follows the last line end
// stays pending, and input that is not text leaves the line unread until it
// ends or is discarded.
func TestShellSendIsReadInTerminalOrder(t *testing.T) {
	long := strings.Repeat("a", maxShellLineBytes)
	for _, c := range []struct {
		input   string
		program string
		unknown bool
		after   shellLineState
	}{
		{`{"text":"echo ok","submit":true}`, "echo ok\n", false, shellLineState{}},
		{`{"text":"echo ok"}`, "echo ok", false, shellLineState{text: "echo ok"}},
		{`{"text":"r","paste":"m -rf /fixture"}`, "rm -rf /fixture", false, shellLineState{text: "rm -rf /fixture"}},
		{`{"paste":"echo b","text":"echo a","keys":["enter"],"submit":true}`, "echo a\n\necho b", false, shellLineState{text: "echo b"}},
		{`{"text":"echo a\recho b\necho c"}`, "echo a\necho b\necho c", false, shellLineState{text: "echo c"}},
		{`{"paste":"echo a\n\techo b"}`, "echo a\n\techo b", false, shellLineState{text: " echo b"}},
		// A key that moves the cursor decides where the paste after it lands.
		{`{"text":"m -rf /fixture","keys":["home"],"paste":"r"}`, "m -rf /fixturer", true, shellLineState{unknown: true}},
		{`{"text":"m -rf /fixture","keys":["Home"," ENTER "],"paste":"echo next"}`, "m -rf /fixture\necho next", true, shellLineState{unknown: true}},
		{`{"keys":["left","home","end","right"],"paste":"echo b"}`, "echo b", false, shellLineState{text: "echo b"}},
		{`{"text":"echo a","keys":["enter","left"],"paste":"echo b"}`, "echo a\necho b", false, shellLineState{text: "echo b"}},
		// Raw bytes come after the keys and before the paste.
		{`{"text":"echo a","keys":["enter"],"hex":["0x72"],"paste":"m -rf /fixture\n"}`, "echo a\nm -rf /fixture\n", true, shellLineState{unknown: true}},
		{`{"text":"echo a","hex":["0x0d"]}`, "echo a", true, shellLineState{unknown: true}},
		{`{"text":"echo a","hex":"0x0d"}`, "echo a", true, shellLineState{unknown: true}},
		{`{"text":"echo a","keys":["up"]}`, "echo a", true, shellLineState{unknown: true}},
		{`{"text":"echo a","keys":["backspace"]}`, "echo a", true, shellLineState{unknown: true}},
		{`{"text":"echo a","keys":["delete"]}`, "echo a", true, shellLineState{unknown: true}},
		{`{"keys":["ctrl+r"]}`, "", true, shellLineState{unknown: true}},
		{`{"keys":["tab"]}`, "", true, shellLineState{unknown: true}},
		{`{"text":"/usr/bin/shre\t"}`, "/usr/bin/shre\t", true, shellLineState{unknown: true}},
		{`{"text":"echo a\u001b[D"}`, "echo a\x1b[D", true, shellLineState{unknown: true}},
		{`{"paste":"echo a\u001b[201~\recho b"}`, "echo a\x1b[201~\necho b", true, shellLineState{unknown: true}},
		// An interrupt discards the line; erasing it does so only while the
		// cursor is known to be at its end. A key the same call sent before
		// still needs a person.
		{`{"text":"echo a","keys":["ctrl+c"],"paste":"echo b"}`, "echo a\necho b", false, shellLineState{text: "echo b"}},
		{`{"text":"echo a","keys":["up","ctrl+c"],"paste":"echo b"}`, "echo a\necho b", true, shellLineState{text: "echo b"}},
		{`{"text":"echo a","keys":["ctrl+u"],"paste":"echo b"}`, "echo a\necho b", false, shellLineState{text: "echo b"}},
		{`{"text":"m -rf /fixture","keys":["home","ctrl+u"],"paste":"r"}`, "m -rf /fixturer", true, shellLineState{unknown: true}},
		// What was pending on the line comes before everything the call sends.
		{`{"line_before":"r","text":"m -rf /fixture","submit":true}`, "rm -rf /fixture\n", false, shellLineState{}},
		{`{"text":"m","line_before":"r\\\n"}`, "r\\\nm", false, shellLineState{text: "r\\\nm"}},
		{`{"line_before":"echo a","keys":["left"],"paste":"x"}`, "echo ax", true, shellLineState{unknown: true}},
		{`{"line_before":"echo a\t","submit":true}`, "echo a\t\n", true, shellLineState{unknown: true}},
		// The mark of a line that is not inspectable outlasts its end: only an
		// interrupt discards it.
		{`{"line_not_inspectable":true,"text":"echo a"}`, "echo a", true, shellLineState{unknown: true}},
		{`{"line_not_inspectable":true,"text":"echo a","submit":true}`, "echo a\n", true, shellLineState{unknown: true}},
		{`{"line_not_inspectable":true,"keys":["ctrl+c"],"paste":"echo b"}`, "\necho b", false, shellLineState{text: "echo b"}},
		{`{"line_not_inspectable":true,"keys":["ctrl+u"]}`, "", true, shellLineState{unknown: true}},
		// A line end leaves the text pending while the shell keeps reading the
		// same command.
		{`{"text":"r\\","submit":true}`, "r\\\n", false, shellLineState{text: "r\\\n"}},
		{`{"text":"echo 'a","keys":["enter"]}`, "echo 'a\n", false, shellLineState{text: "echo 'a\n"}},
		{`{"text":"cat <<EOF\rfixture","submit":true}`, "cat <<EOF\nfixture\n", false, shellLineState{text: "cat <<EOF\nfixture\n"}},
		{`{"text":"r\\\nm","keys":["ctrl+u"],"paste":"x"}`, "r\\\nm\nx", false, shellLineState{text: "r\\\nx"}},
		{`{"text":"r\\\nm","keys":["ctrl+c"],"paste":"x"}`, "r\\\nm\nx", false, shellLineState{text: "x"}},
		{`{"text":"r\\\nm -rf /fixture\necho next"}`, "r\\\nm -rf /fixture\necho next", false, shellLineState{text: "echo next"}},
		// The length of a pending line is bounded.
		{`{"text":"` + long + `"}`, long, false, shellLineState{text: long}},
		{`{"text":"` + long + `a"}`, long + "a", true, shellLineState{unknown: true}},
		{`{"text":"` + long + `a","submit":true,"paste":"echo b"}`, long + "a\necho b", true, shellLineState{unknown: true}},
	} {
		got := readShellSend([]byte(c.input))
		if got.program != c.program || got.unknown != c.unknown || got.after != c.after {
			name := c.input
			if len(name) > 120 {
				name = name[:120] + "..."
			}
			t.Errorf("%s: program=%.80q unknown=%v after={%.40q %v}, want program=%.80q unknown=%v after={%.40q %v}", name, got.program, got.unknown, got.after.text, got.after.unknown, c.program, c.unknown, c.after.text, c.after.unknown)
		}
	}
}

func shellRun() context.Context {
	ctx, _ := WithTrace(context.Background(), "", nil)
	return ctx
}

// sent passes a call through the review and, as the tool does once it has
// run, reports it back.
func sent(i *Interceptor, ctx context.Context, input string) string {
	_, reviewed := i.ShellSendOnLine(ctx, []byte(input))
	i.ShellToolRan(ctx, "shell_send", []byte(input), nil)
	return string(reviewed)
}

// reviewedLine is the line a reviewed call shows up to and including its
// text, and whether the call is marked as continuing a line that was not read.
func reviewedLine(t *testing.T, reviewed []byte) (string, bool) {
	t.Helper()
	var call shellSendCall
	if err := json.Unmarshal(reviewed, &call); err != nil {
		t.Fatalf("reviewed input does not decode: %s: %v", reviewed, err)
	}
	return call.Before + call.Text, call.Unknown
}

// A line typed over several calls meets the rules as one line, whichever
// channel completes it, and only input that ran counts as typed.
func TestPendingShellLineIsReviewedWithTheCall(t *testing.T) {
	i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny"), semanticFixture(`\bfixturewipe\s`, "deny")}}
	names := []string{"shell_send"}
	denied := func(ctx context.Context, input string) bool {
		_, reviewed := i.ShellSendOnLine(ctx, []byte(input))
		dec, hit := i.MatchNames(names, reviewed)
		return hit && dec.Action == "deny"
	}
	for _, c := range []struct{ first, second string }{
		{`{"session_id":"sh_1","text":"r"}`, `{"session_id":"sh_1","text":"m -rf /fixture","submit":true}`},
		{`{"session_id":"sh_1","text":"r"}`, `{"session_id":"sh_1","paste":"m -rf /fixture\n"}`},
		{`{"session_id":"sh_1","text":"r\\","submit":true}`, `{"session_id":"sh_1","text":"m -rf /fixture","submit":true}`},
		{`{"session_id":"sh_1","text":"fixture"}`, `{"session_id":"sh_1","paste":"wipe -x /fixture"}`},
	} {
		ctx := shellRun()
		if denied(ctx, c.second) {
			t.Fatalf("the second call is denied on its own, so it shows nothing: %s", c.second)
		}
		if denied(ctx, c.first) {
			t.Errorf("the first call was denied before the line held the command: %s", c.first)
		}
		// A call that did not run has typed nothing.
		if denied(ctx, c.second) {
			t.Errorf("a call that never ran changed the pending line: %s", c.first)
		}
		sent(i, ctx, c.first)
		if !denied(ctx, c.second) {
			t.Errorf("the line completed by the second call escaped the rule: %s then %s", c.first, c.second)
		}
	}
	// One call that types and pastes forms the line as well.
	for _, input := range []string{
		`{"session_id":"sh_1","text":"r","paste":"m -rf /fixture"}`,
		`{"session_id":"sh_1","text":"fixture","paste":"wipe -x /fixture\n"}`,
		`{"session_id":"sh_1","text":"echo fixture","paste":"; /bin/r'm' -rf /fixture"}`,
	} {
		if !denied(shellRun(), input) {
			t.Errorf("text and paste of one call were not read as one line: %s", input)
		}
	}
	// A call that only presses Enter shows the line it submits, here one that
	// was typed before the rule existed.
	for _, first := range []string{
		`{"session_id":"sh_1","text":"r'm' -rf /fixture"}`,
		`{"session_id":"sh_1","paste":"r'm' -rf /fixture"}`,
	} {
		for _, enter := range []string{`{"session_id":"sh_1","keys":["enter"]}`, `{"session_id":"sh_1","submit":true}`, `{"session_id":"sh_1","text":"\r"}`} {
			ctx := shellRun()
			if denied(ctx, enter) {
				t.Fatalf("Enter on an empty line is denied: %s", enter)
			}
			sent(i, ctx, first)
			if !denied(ctx, enter) {
				t.Errorf("the line submitted by a later call escaped the rule: %s then %s", first, enter)
			}
		}
	}
	// The reviewed input keeps characters as rules written for them expect,
	// and holds each field once, with the value the tool reads. What it says
	// about the pending line comes from the gate alone.
	ctx := shellRun()
	sent(i, ctx, `{"session_id":"sh_1","text":"echo a > b && "}`)
	_, reviewed := i.ShellSendOnLine(ctx, []byte(`{"session_id":"sh_1","text":"x","TEXT":"echo <c>","SUBMIT":true,"wait":false,"LINE_BEFORE":"# ","line_not_inspectable":true}`))
	if string(reviewed) != `{"line_before":"echo a > b && ","session_id":"sh_1","submit":true,"text":"echo <c>","wait":false}` {
		t.Errorf("reviewed input is not the line the tool sends: %s", reviewed)
	}
	for _, claimed := range []string{
		`{"session_id":"sh_2","text":"r'm' -rf /fixture","submit":true,"line_before":"# "}`,
		`{"session_id":"sh_3","text":"r","Line_Before":"# "}`,
		`{"session_id":"sh_4","text":"r","line_not_inspectable":true}`,
	} {
		_, reviewed := i.ShellSendOnLine(ctx, []byte(claimed))
		if strings.Contains(strings.ToLower(string(reviewed)), "line_") {
			t.Errorf("a call told the gate what is pending on its line: %s", reviewed)
		}
		i.ShellToolRan(ctx, "shell_send", []byte(claimed), nil)
	}
	for _, session := range []string{"sh_3", "sh_4"} {
		if !denied(ctx, `{"session_id":"`+session+`","text":"m -rf /fixture","submit":true}`) {
			t.Errorf("what a call claimed about its line changed what is pending on %s", session)
		}
	}
}

// The pending text may have been read by a program before the call arrives,
// which leaves the call at the start of a line. Both readings meet the rules,
// and a rule that allows a call by its input does not answer for a piece.
func TestContinuedShellLineIsAlsoReadOnItsOwn(t *testing.T) {
	allow := semanticFixture(`"text":"(?:n|echo fixture)"`, "allow")
	i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny"), allow}}
	names := []string{"shell_send"}
	for _, pending := range []string{`# `, `echo '`, `: "`, `cat <<EOF` + "\n"} {
		ctx := shellRun()
		typed, _ := json.Marshal(map[string]string{"session_id": "sh_1", "text": pending})
		sent(i, ctx, string(typed))
		for _, input := range []string{
			`{"session_id":"sh_1","text":"r'm' -rf /fixture","submit":true}`,
			`{"session_id":"sh_1","paste":"r'm' -rf /fixture\n"}`,
			`{"session_id":"sh_1","text":"r'm' -rf","paste":" /fixture\n"}`,
		} {
			_, reviewed := i.ShellSendOnLine(ctx, []byte(input))
			if dec, hit := i.MatchNames(names, reviewed); !hit || dec.Action != "deny" {
				t.Errorf("pending text hid the command a call sends: %q then %s: %+v", pending, input, dec)
			}
		}
	}
	ctx := shellRun()
	whole := `{"session_id":"sh_1","text":"echo fixture","submit":true}`
	if dec, hit := i.MatchNames(names, []byte(whole)); !hit || dec.Action != "allow" {
		t.Fatalf("the allow rule does not cover the whole command: %+v", dec)
	}
	sent(i, ctx, `{"session_id":"sh_1","text":"pytho"}`)
	for _, input := range []string{`{"session_id":"sh_1","text":"n"}`, whole} {
		_, reviewed := i.ShellSendOnLine(ctx, []byte(input))
		if dec, hit := i.MatchNames(names, reviewed); !hit || dec.Action != "ask" || dec.RuleName != "Unconfined shell execution" {
			t.Errorf("a rule that allows a whole command answered for a piece of a line: %s: %+v", reviewed, dec)
		}
	}
}

// After input that is not text, every call up to and including the one that
// ends the line needs a person, however harmless its own text looks. An
// interrupt, or erasing a line typed without such input, starts over.
func TestUnreadShellLineStaysUnreadUntilItEnds(t *testing.T) {
	i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
	names := []string{"shell_send"}
	needsPerson := func(ctx context.Context, input string) bool {
		_, reviewed := i.ShellSendOnLine(ctx, []byte(input))
		dec, hit := i.MatchNames(names, reviewed)
		return hit && dec.Action == "ask" && dec.HumanRequired && dec.RuleName == "Unresolved shell program"
	}
	for _, edit := range []string{
		`{"session_id":"sh_1","text":"m -rf /fixture","keys":["home"]}`,
		`{"session_id":"sh_1","text":"m -rf /fixture","keys":["left"]}`,
		`{"session_id":"sh_1","text":"xrm -rf /fixture","keys":["backspace"]}`,
		`{"session_id":"sh_1","keys":["up"]}`,
		`{"session_id":"sh_1","text":"/bin/r","keys":["tab"]}`,
		`{"session_id":"sh_1","hex":["0x72","0x6d"]}`,
		`{"session_id":"sh_1","text":"echo fixture\u0001"}`,
		`{"session_id":"sh_1","paste":"echo fixture\u001b[201~"}`,
	} {
		ctx := shellRun()
		if !needsPerson(ctx, edit) {
			t.Errorf("input that is not text did not need a person: %s", edit)
		}
		sent(i, ctx, edit)
		for _, next := range []string{
			`{"session_id":"sh_1","text":"echo fixture"}`,
			`{"session_id":"sh_1","keys":["ctrl+u"]}`,
			`{"session_id":"sh_1","keys":["enter"]}`,
			`{"session_id":"sh_1","text":"echo fixture","submit":true}`,
		} {
			if !needsPerson(ctx, next) {
				t.Errorf("a call on a line that was not read did not need a person: %s then %s", edit, next)
			}
		}
		if text, unknown := reviewedLine(t, []byte(sent(i, ctx, `{"session_id":"sh_1","text":"echo fixture"}`))); !unknown || text != "echo fixture" {
			t.Errorf("reviewed input does not mark the line: %q %v", text, unknown)
		}
		if needsPerson(ctx, `{"session_id":"sh_2","text":"echo fixture","submit":true}`) {
			t.Errorf("another session shared the line that was not read: %s", edit)
		}
		// Ending the line does not make the next one known: the line that ran
		// unread may have left a quote or another command open.
		sent(i, ctx, `{"session_id":"sh_1","keys":["enter"]}`)
		if !needsPerson(ctx, `{"session_id":"sh_1","text":"echo fixture","submit":true}`) {
			t.Errorf("the line after one that was not read was taken as a new line: %s", edit)
		}
		// An interrupt discards a line that was not read.
		sent(i, ctx, edit)
		sent(i, ctx, `{"session_id":"sh_1","keys":["ctrl+c"]}`)
		if needsPerson(ctx, `{"session_id":"sh_1","text":"echo fixture","submit":true}`) {
			t.Errorf("an interrupted line still needs a person: %s", edit)
		}
	}
	// Erasing a line that was only typed leaves nothing of it for the next call.
	ctx := shellRun()
	sent(i, ctx, `{"session_id":"sh_1","text":"r"}`)
	sent(i, ctx, `{"session_id":"sh_1","keys":["ctrl+u"]}`)
	if got := sent(i, ctx, `{"session_id":"sh_1","text":"m -rf /fixture"}`); got != `{"session_id":"sh_1","text":"m -rf /fixture"}` {
		t.Errorf("an erased line was still pending: %s", got)
	}
}

// Sessions are told apart by run and by the id the tool uses, a session that
// is closed or opened starts empty, and a failed or unknown call types nothing.
func TestShellLinesBelongToOneSessionOfOneRun(t *testing.T) {
	i := &Interceptor{}
	first, second := shellRun(), shellRun()
	sent(i, first, `{"session_id":"sh_1","text":"r"}`)
	for _, c := range []struct {
		ctx   context.Context
		input string
	}{
		{second, `{"session_id":"sh_1","text":"m"}`},
		{first, `{"session_id":"sh_2","text":"m"}`},
		{first, `{"session_id":"","text":"m"}`},
		{first, `{"text":"m"}`},
		{first, `{"session_id":"SH_1","text":"m"}`},
	} {
		if _, reviewed := i.ShellSendOnLine(c.ctx, []byte(c.input)); string(reviewed) != c.input {
			t.Errorf("a line was shared with another run or session: %s became %s", c.input, reviewed)
		}
	}
	if text, _ := reviewedLine(t, []byte(sent(i, first, `{"SESSION_ID":"sh_1","text":"m"}`))); text != "rm" {
		t.Errorf("the session the tool writes to lost its line: %q", text)
	}
	// Calls outside a traced run are kept apart by task, agent and conversation.
	plain := context.Background()
	task := WithTaskContext(plain, "7", "fixture", nil)
	sent(i, task, `{"session_id":"sh_1","text":"r"}`)
	for _, ctx := range []context.Context{plain, WithTaskContext(plain, "8", "fixture", nil), WithTaskContext(plain, "7", "other", nil), WithConvID(task, 3), second} {
		if _, reviewed := i.ShellSendOnLine(ctx, []byte(`{"session_id":"sh_1","text":"m"}`)); string(reviewed) != `{"session_id":"sh_1","text":"m"}` {
			t.Errorf("a line was shared across tasks, agents or conversations: %s", reviewed)
		}
	}
	if text, _ := reviewedLine(t, []byte(sent(i, task, `{"session_id":"sh_1","text":"m"}`))); text != "rm" {
		t.Errorf("the task's own session lost its line: %q", text)
	}
	// Only a call that ran changes the line: other tools and failed calls do not.
	i.ShellToolRan(first, "Bash", []byte(`{"session_id":"sh_1","text":"x"}`), nil)
	i.ShellToolRan(first, "shell_read", []byte(`{"session_id":"sh_1","text":"x"}`), nil)
	if text, _ := reviewedLine(t, []byte(sent(i, first, `{"session_id":"sh_1","text":"x"}`))); text != "rmx" {
		t.Errorf("a call of another tool changed the line: %q", text)
	}
	// Closing a session, or opening one that gets the same id, starts empty.
	i.ShellToolRan(first, "shell_close", []byte(`{"session_id":"sh_1"}`), []byte(`"{\"closed\":\"sh_1\"}"`))
	if got := sent(i, first, `{"session_id":"sh_1","text":"m"}`); got != `{"session_id":"sh_1","text":"m"}` {
		t.Errorf("a closed session kept its line: %s", got)
	}
	i.ShellToolRan(first, "shell_open", []byte(`{"command":"sh"}`), []byte(`"{\"session_id\":\"sh_1\",\"state\":\"running\"}"`))
	if got := sent(i, first, `{"session_id":"sh_1","text":"m"}`); got != `{"session_id":"sh_1","text":"m"}` {
		t.Errorf("a newly opened session inherited a line: %s", got)
	}
}

// The number of remembered sessions is bounded, and a session that was dropped
// is treated as holding a line that was not read, never as an empty one.
func TestForgottenShellSessionIsNotAnEmptyLine(t *testing.T) {
	i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
	ctx := shellRun()
	sent(i, ctx, `{"session_id":"sh_0","text":"r"}`)
	for n := 1; n <= maxShellSessions; n++ {
		sent(i, ctx, fmt.Sprintf(`{"session_id":"sh_%d","text":"echo fixture"}`, n))
	}
	trace, _ := ctx.Value(traceKey{}).(*Trace)
	if len(trace.shell.lines) != maxShellSessions {
		t.Fatalf("remembered sessions: %d", len(trace.shell.lines))
	}
	for _, session := range []string{"sh_0", "sh_new"} {
		input := `{"session_id":"` + session + `","text":"m -rf /fixture","submit":true}`
		_, reviewed := i.ShellSendOnLine(ctx, []byte(input))
		dec, hit := i.MatchNames([]string{"shell_send"}, reviewed)
		if _, unknown := reviewedLine(t, reviewed); !unknown || !hit || dec.Action != "ask" || !dec.HumanRequired {
			t.Errorf("a session that may have been dropped was read as an empty line: %s: %+v", reviewed, dec)
		}
		// Ending that line does not make the session known; an interrupt does.
		i.ShellToolRan(ctx, "shell_send", []byte(input), nil)
		if _, reviewed := i.ShellSendOnLine(ctx, []byte(input)); string(reviewed) == input {
			t.Errorf("a session that may have been dropped became known at a line end: %s", reviewed)
		}
		sent(i, ctx, `{"session_id":"`+session+`","keys":["ctrl+c"]}`)
		if _, reviewed := i.ShellSendOnLine(ctx, []byte(input)); string(reviewed) != input {
			t.Errorf("a line after an interrupt is still not read: %s", reviewed)
		}
	}
	if len(trace.shell.lines) > maxShellSessions {
		t.Errorf("remembered sessions grew past the limit: %d", len(trace.shell.lines))
	}
	_, reviewed := i.ShellSendOnLine(ctx, []byte(`{"session_id":"sh_9","text":"; id"}`))
	if line, unknown := reviewedLine(t, reviewed); line != "echo fixture; id" || unknown {
		t.Errorf("a session within the limit lost its line: %s", reviewed)
	}
}

// A standing approval for one exact command is compared with the whole line,
// never with the piece one call adds, and not with a line that was not read.
func TestCommandGrantCoversTheWholeShellLine(t *testing.T) {
	i := &Interceptor{}
	ctx := shellRun()
	grants := []db.TaskToolGrant{{Scope: "command", ToolName: "shell_send", Command: "echo fixture"}}
	whole := `{"session_id":"sh_1","text":"echo fixture","submit":true}`
	if _, reviewed := i.ShellSendOnLine(ctx, []byte(whole)); !GrantAllows(grants, []string{"shell_send"}, reviewed) {
		t.Fatal("the granted command typed in one call lost its approval")
	}
	sent(i, ctx, `{"session_id":"sh_1","text":"id; "}`)
	_, reviewed := i.ShellSendOnLine(ctx, []byte(whole))
	if got := CommandText("shell_send", reviewed); got != "id; echo fixture" {
		t.Errorf("command text of a continued line: %q", got)
	}
	if GrantAllows(grants, []string{"shell_send"}, reviewed) {
		t.Error("an approval for one command covered a longer line that ends with it")
	}
	if !GrantAllows([]db.TaskToolGrant{{Scope: "command", ToolName: "shell_send", Command: "id; echo fixture"}}, []string{"shell_send"}, reviewed) {
		t.Error("an approval for the whole line did not cover it")
	}
	// An approval for exact parameters is bound to the whole line as well.
	if GrantAllows([]db.TaskToolGrant{{Scope: "input", ToolName: "shell_send", Command: whole}}, []string{"shell_send"}, reviewed) {
		t.Error("an approval for exact parameters covered them on a different line")
	}
	sent(i, ctx, `{"session_id":"sh_1","keys":["ctrl+c","up"]}`)
	_, reviewed = i.ShellSendOnLine(ctx, []byte(whole))
	if got := CommandText("shell_send", reviewed); got != "" || GrantAllows(grants, []string{"shell_send"}, reviewed) {
		t.Errorf("an approval covered a line that was not read: %q", got)
	}
}

// The tool sends the fields that decode even when another has the wrong type,
// so such a call is still reviewed with its line, and stays not inspectable.
func TestPartlyDecodedShellSendKeepsItsLine(t *testing.T) {
	i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
	names := []string{"shell_send"}
	for _, second := range []string{
		`{"session_id":"sh_1","text":"m -rf /fixture","submit":true,"Paste":5}`,
		`{"session_id":"sh_1","text":"m -rf /fixture","submit":true,"HEX":"x"}`,
		`{"session_id":"sh_1","submit":true,"Paste":5,"line_before":"# "}`,
	} {
		ctx := shellRun()
		first := `{"session_id":"sh_1","text":"r"}`
		if strings.Contains(second, "line_before") {
			first = `{"session_id":"sh_1","text":"r'm' -rf /fixture"}`
		}
		sent(i, ctx, first)
		_, reviewed := i.ShellSendOnLine(ctx, []byte(second))
		if dec, hit := i.MatchNames(names, reviewed); !hit || dec.Action != "deny" {
			t.Errorf("a field of the wrong type turned a denial into a request: %s: %+v", reviewed, dec)
		}
		if !analyzeOperation(names, reviewed).shellProgramUnknown {
			t.Errorf("a call that did not decode was reviewed as one that did: %s", reviewed)
		}
		sent(i, ctx, second)
		if lines, session := i.shellLinesFor(ctx, "sh_1"); !lines.get(session).unknown {
			t.Errorf("the line is known after a call that did not decode: %s", second)
		}
	}
}
