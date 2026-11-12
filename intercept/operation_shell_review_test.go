package intercept

import (
	"encoding/json"
	"testing"
)

func TestUnresolvedShellProgramsRequireHumanApproval(t *testing.T) {
	for _, command := range []string{
		`bash -c "$PROGRAM"`, `env -i sh -c "$PROGRAM"`,
		`bash -c "$(printf '%s' "$PROGRAM")"`,
		`bash -c *`, `bash "$OPTIONS" -c 'echo fixture'`,
		`bash fixture.sh`, `sh -- "$SCRIPT"`, `bash < fixture.sh`,
		`bash <<< "$PROGRAM"`, `printf '%s' "$PROGRAM" | bash`,
		"bash <<EOF\n$PROGRAM\nEOF", `bash`,
		`bash -n "$OPTIONS"`, `bash -ni -c "$PROGRAM"`,
		`bash -o "$OPTION" -c 'echo fixture'`,
	} {
		for _, tool := range []string{"Bash", "shell_open", "shell_send"} {
			input, _ := json.Marshal(map[string]string{"command": command, "text": command})
			i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
			if dec, hit := i.Match(tool, input); !hit || dec.Action != "ask" || dec.ModelFallback || !dec.TimeoutEnabled || dec.TimeoutAction != "deny" {
				t.Errorf("uninspected shell program escaped: %q: %+v %v", command, dec, hit)
			}
			i.cached = []compiledRule{semanticFixture(`bash|sh`, "allow"), semanticFixture(`\brm\s`, "deny")}
			if dec, hit := i.Match(tool, input); !hit || dec.Action != "allow" {
				t.Fatal("explicit allow precedence changed")
			}
		}
	}
}

func TestShellProgramReviewPreservesLiteralDataAndNoExec(t *testing.T) {
	for _, command := range []string{
		`bash -c 'echo "$1"' fixture "$DATA"`, `bash -c ''`,
		`bash -c '<dynamic>'`, `bash -nc "$PROGRAM"`,
		`bash -n fixture.sh`, `bash -n < fixture.sh`,
		`bash -o noexec -c "$PROGRAM"`, `echo 'bash fixture.sh'`,
		`printf '%s\n' 'echo fixture' | bash`, `bash <<< 'echo fixture'`,
		"bash <<'EOF'\necho fixture\nEOF", `bash -- -literal-script`,
	} {
		// A script filename still needs approval, including a dash-prefixed name
		// after --. It must not be interpreted as shell options or inline code.
		wantAsk := command == `bash -- -literal-script` || command == `bash -c '<dynamic>'`
		input, _ := json.Marshal(map[string]string{"command": command})
		i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
		dec, hit := i.Match("Bash", input)
		if subjects := operationSubjects([]string{"Bash"}, input); len(subjects) != 0 {
			t.Fatalf("data became an invented command: %q %v", command, subjects)
		}
		wantReason := "Unconfined shell execution"
		if wantAsk {
			wantReason = "Unresolved shell program"
		}
		if !hit || dec.Action != "ask" || dec.RuleName != wantReason {
			t.Errorf("shell argument or authorization role lost: %q: %+v %v", command, dec, hit)
		}
	}
}
