package intercept

import (
	"encoding/json"
	"testing"

	"github.com/Philocyber/counterweight/locale"
)

func TestRuntimeExecutableRequiresHumanApproval(t *testing.T) {
	for _, command := range []string{
		`"$PROGRAM" -rf /fixture`,
		`${PROGRAM} --request=DELETE https://fixture.invalid/resource`,
		`"$(printf '%s' unknown-program)" /fixture`,
		`env MODE=fixture "$PROGRAM" /fixture`,
		`command "$PROGRAM" /fixture`,
		`exec -a fixture "$PROGRAM" /fixture`,
		`bash -c '"$PROGRAM" /fixture'`,
		`printf '%s\n' '"$PROGRAM" /fixture' | bash`,
	} {
		for _, names := range [][]string{{"Bash"}, {"shell_open"}, {"shell_send"}, {"custom", "Bash"}} {
			input, _ := json.Marshal(map[string]string{"command": command, "text": command})
			i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s|method: 'DELETE'`, "deny")}}
			dec, hit := i.MatchNames(names, input)
			if !hit || dec.Action != "ask" || dec.ModelFallback || dec.TimeoutAction != "deny" || !dec.TimeoutEnabled {
				t.Errorf("runtime executable silently allowed: %q %v: %+v %v", command, names, dec, hit)
			}
			i.cached = []compiledRule{semanticFixture(`/fixture|fixture.invalid`, "allow"), semanticFixture(`\brm\s|method: 'DELETE'`, "deny")}
			if dec, hit := i.MatchNames(names, input); !hit || dec.Action != "allow" {
				t.Error("explicit operator override lost")
			}
		}
	}
}

func TestRuntimeExecutableApprovalPreservesStaticDataAndControls(t *testing.T) {
	deny := semanticFixture(`\brm\s|method: 'DELETE'`, "deny")
	for _, command := range []string{`echo "$PROGRAM"`, `printf '%s' "$PROGRAM"`, `nmap "$TARGET"`, `bash -c 'echo "$1"' fixture "$PROGRAM"`, `command -v "$PROGRAM"`, `env NAME="$PROGRAM" echo fixture`, `command '<dynamic>' /fixture`} {
		i := &Interceptor{cached: []compiledRule{deny}}
		input, _ := json.Marshal(map[string]string{"command": command})
		assertUnconfinedShellReview(t, i, input)
	}
	input := []byte(`{"command":"\"$PROGRAM\" /fixture"}`)
	for _, rules := range [][]compiledRule{{}, {semanticFixture(`unmatched`, "allow")}} {
		i := &Interceptor{cached: rules}
		if dec, hit := i.Match("Bash", input); hit {
			t.Errorf("disabled restrictive policy gained a new decision: %+v", dec)
		}
	}
	i := &Interceptor{cached: []compiledRule{deny}}
	if dec, hit := i.Match("Write", input); hit {
		t.Errorf("non-execution tool entered shell approval: %+v", dec)
	}
	if dec, hit := i.Match("Bash", input, locale.En); !hit || dec.Message != locale.Text(locale.En, "The command selects its executable at runtime; human approval is required") {
		t.Errorf("approval reason missing or altered: %+v", dec)
	}
}
