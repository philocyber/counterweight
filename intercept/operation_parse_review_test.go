package intercept

import (
	"encoding/json"
	"regexp"
	"testing"
)

func TestUnparsedShellInputRequiresHumanApproval(t *testing.T) {
	for _, command := range []string{
		`for fixture in one; do`, `echo "unterminated`,
		`r'm' -rf /fixture; echo "`,
		`bash -c 'for fixture in one; do'`,
		`eval 'echo "'`, `printf '%s' 'echo "' | bash`,
		"bash <<'EOF'\necho \"\nEOF", `done`,
		`bash -c '<dynamic>'`,
	} {
		for _, names := range [][]string{{"Bash"}, {"shell_open"}, {"shell_send"}, {"custom", "Bash"}} {
			input, _ := json.Marshal(map[string]string{"command": command, "text": command})
			i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
			if dec, hit := i.MatchNames(names, input); !hit || dec.Action != "ask" || dec.ModelFallback || !dec.TimeoutEnabled || dec.TimeoutAction != "deny" {
				t.Errorf("unparsed input passed silently: %q %v: %+v %v", command, names, dec, hit)
			}
			i.cached = []compiledRule{semanticFixture(regexp.QuoteMeta(string(input)), "allow"), semanticFixture(`\brm\s`, "deny")}
			if dec, hit := i.MatchNames(names, input); !hit || dec.Action != "allow" {
				t.Fatal("explicit allow priority changed")
			}
		}
	}
}

func TestUnparsedInputReviewPreservesDataAndPolicyControls(t *testing.T) {
	for _, command := range []string{`echo 'echo "'`, `printf '%s' 'for x; do'`, `bash -nc 'echo "'`, "", "# fixture comment\n"} {
		input, _ := json.Marshal(map[string]string{"command": command})
		i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
		assertUnconfinedShellReview(t, i, input)
	}
	input := []byte(`{"command":"for fixture in one; do"}`)
	for _, rules := range [][]compiledRule{{}, {semanticFixture("unmatched", "allow")}} {
		i := &Interceptor{cached: rules}
		if _, hit := i.Match("Bash", input); hit {
			t.Fatal("inactive restrictive policy gained a decision")
		}
	}
	i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
	if _, hit := i.Match("Write", input); hit {
		t.Fatal("file data parsed as a shell program")
	}
}
