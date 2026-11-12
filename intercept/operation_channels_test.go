package intercept

import (
	"encoding/json"
	"testing"
)

// A command word holding an unquoted pathname pattern is resolved by the shell
// against the file system, so restrictive rules must treat it as a program
// chosen at run time.
func TestGlobCommandWordIsRuntimeExecutable(t *testing.T) {
	deny := semanticFixture(`\brm\s`, "deny")
	for _, command := range []string{
		`/bin/r[m] -rf /fixture`,
		`/???/b??h -c 'id'`,
		`/usr/bin/ech* fixture`,
		`/bin/r["m"] -rf /fixture`,
		`env MODE=fixture /bin/r[m] -rf /fixture`,
		`sudo /bin/r? -rf /fixture`,
		`bash -c '/bin/r[m] -rf /fixture'`,
		`echo fixture; /bin/r[m] -rf /fixture`,
	} {
		for _, names := range [][]string{{"Bash"}, {"shell_open"}, {"shell_send"}, {"custom", "Bash"}} {
			input, _ := json.Marshal(map[string]string{"command": command, "text": command})
			if analysis := analyzeOperation(names, input); !analysis.runtimeExecutable {
				t.Errorf("pattern command word treated as a fixed program: %q %v", command, names)
			}
			i := &Interceptor{cached: []compiledRule{deny}}
			dec, hit := i.MatchNames(names, input)
			if !hit || dec.Action != "ask" || !dec.HumanRequired || dec.RuleName != "Runtime-selected shell executable" || dec.TimeoutAction != "deny" {
				t.Errorf("pattern command word did not require a person: %q %v: %+v %v", command, names, dec, hit)
			}
		}
	}
}

// Pattern characters in the arguments of a fixed program, quoted or escaped
// pattern characters, and the test command are not a runtime choice of program.
func TestGlobArgumentsKeepFixedProgram(t *testing.T) {
	deny := semanticFixture(`\brm\s`, "deny")
	for _, command := range []string{
		`ls *.txt`,
		`grep 'a[bc]' file`,
		`grep a[bc] file?`,
		`[ -f fixture ] && echo present`,
		`'r[m]' fixture`,
		`"ech*" fixture`,
		`ech\* fixture`,
		`env MODE=fixture ls *.txt`,
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		assertUnconfinedShellReview(t, &Interceptor{cached: []compiledRule{deny}}, input)
	}
	input := []byte(`{"command":"rm -rf *.txt"}`)
	if dec, hit := (&Interceptor{cached: []compiledRule{deny}}).Match("Bash", input); !hit || dec.Action != "deny" {
		t.Errorf("fixed program with a pattern argument lost its rule: %+v %v", dec, hit)
	}
}

// Every channel of shell_send that writes to the session is reviewed.
func TestShellSendChannelsAreReviewed(t *testing.T) {
	deny := semanticFixture(`\brm\s`, "deny")
	names := []string{"shell_send"}
	for _, input := range []string{
		`{"session_id":"sh_1","paste":"r'm' -rf /fixture\n"}`,
		`{"session_id":"sh_1","text":"echo fixture","paste":"/bin/rm -rf /fixture"}`,
		`{"session_id":"sh_1","text":"echo fixture\r/bin/rm -rf /fixture","submit":true}`,
	} {
		i := &Interceptor{cached: []compiledRule{deny}}
		if dec, hit := i.MatchNames(names, []byte(input)); !hit || dec.Action != "deny" {
			t.Errorf("command in a reviewed channel escaped the rule: %s: %+v %v", input, dec, hit)
		}
	}
	for _, input := range []string{
		`{"session_id":"sh_1","hex":["0x72","0x6d","0x20","0x2d","0x72","0x66","0x20","0x78","0x0d"]}`,
		`{"session_id":"sh_1","text":"echo fixture","hex":["0x0d"]}`,
		`{"session_id":"sh_1","keys":["up","enter"]}`,
		`{"session_id":"sh_1","keys":["down"]}`,
		`{"session_id":"sh_1","text":"/usr/bin/shre","keys":["tab"]}`,
		`{"session_id":"sh_1","text":"/usr/bin/shre\tfixture","submit":true}`,
		`{"session_id":"sh_1","text":"xfixture","keys":["backspace"]}`,
		`{"session_id":"sh_1","keys":["ctrl+r"]}`,
		`{"session_id":"sh_1","keys":["f13"]}`,
		`{"session_id":"sh_1","text":"\u001b[A","submit":true}`,
		`{"session_id":"sh_1","paste":"fixture\u001b[201~\u001b[A\r"}`,
		`{"session_id":"sh_1","text":"fixture\u0015echo other"}`,
		`{"session_id":"sh_1","text":"echo fixture","hex":"0x0d"}`,
		`{"session_id":"sh_1","command":5,"hex":["0x0d"]}`,
		`{"session_id":"sh_1","method":5,"text":"echo fixture"}`,
	} {
		if analysis := analyzeOperation(names, []byte(input)); !analysis.shellProgramUnknown {
			t.Errorf("bytes that cannot be read as a command were treated as inspected: %s", input)
		}
		i := &Interceptor{cached: []compiledRule{deny}}
		dec, hit := i.MatchNames(names, []byte(input))
		if !hit || dec.Action != "ask" || !dec.HumanRequired || dec.RuleName != "Unresolved shell program" || dec.TimeoutAction != "deny" {
			t.Errorf("uninspectable channel did not require a person: %s: %+v %v", input, dec, hit)
		}
	}
	for _, input := range []string{
		`{"session_id":"sh_1","text":"echo fixture","submit":true}`,
		`{"session_id":"sh_1","text":"echo fixture","keys":["enter"]}`,
		`{"session_id":"sh_1","keys":["ctrl+c"]}`,
		`{"session_id":"sh_1","keys":["Left","right","home","end","Return"]}`,
		`{"session_id":"sh_1","paste":"echo one\n\techo two\n"}`,
		`{"session_id":"sh_1","text":"echo one\r\necho two"}`,
	} {
		analysis := analyzeOperation(names, []byte(input))
		if analysis.runtimeExecutable || analysis.requestMethodUnknown || analysis.shellProgramUnknown {
			t.Errorf("plain input acquired an invented uncertainty: %s: %+v", input, analysis)
		}
		i := &Interceptor{cached: []compiledRule{deny}}
		if dec, hit := i.MatchNames(names, []byte(input)); !hit || dec.Action != "ask" || dec.HumanRequired || dec.RuleName != "Unconfined shell execution" {
			t.Errorf("plain input changed its review: %s: %+v %v", input, dec, hit)
		}
	}
}

// Input whose fields cannot be decoded is not an empty command for the tools
// that run a shell, and stays outside shell review for the others.
func TestUndecodableShellInputIsNotInspected(t *testing.T) {
	input := []byte(`{"command":"r'm' -rf /fixture","method":5}`)
	for _, name := range []string{"Bash", "shell_open"} {
		if analysis := analyzeOperation([]string{name}, input); !analysis.shellProgramUnknown {
			t.Errorf("%s input that failed to decode was treated as inspected", name)
		}
	}
	// The command that did decode still meets the rule that denies it.
	if dec, hit := (&Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}).Match("Bash", input); !hit || dec.Action != "deny" {
		t.Errorf("a field of the wrong type turned a denial into a request: %+v %v", dec, hit)
	}
	if analysis := analyzeOperation([]string{"WebFetch"}, []byte(`{"method":5}`)); analysis.shellProgramUnknown {
		t.Error("a tool that runs no shell gained a shell uncertainty")
	}
}

// A standing approval for one exact command covers only calls that send
// nothing but that text.
func TestCommandGrantDoesNotCoverOtherChannels(t *testing.T) {
	for _, input := range []string{
		`{"text":"echo fixture","paste":"rm -rf /fixture"}`,
		`{"text":"echo fixture","keys":["up","enter"]}`,
		`{"text":"echo fixture","hex":["0x0d"]}`,
		`{"text":"echo fixture","hex":"0x0d"}`,
	} {
		if got := CommandText("shell_send", []byte(input)); got != "" {
			t.Errorf("command text %q claimed for a call that sends more: %s", got, input)
		}
	}
	if got := CommandText("shell_send", []byte(`{"text":" echo fixture ","submit":true}`)); got != "echo fixture" {
		t.Errorf("text-only command lost: %q", got)
	}
}
