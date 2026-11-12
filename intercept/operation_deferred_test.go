package intercept

import (
	"encoding/json"
	"testing"
)

func TestLiteralDeferredProgramsRetainRestriction(t *testing.T) {
	for _, command := range []string{
		`trap "r'm' -rf /fixture" EXIT`,
		`trap -- "r'm' -rf /fixture" DEBUG`,
		`builtin trap "r'm' -rf /fixture" RETURN`,
		`command trap "r'm' -rf /fixture" ERR`,
		`alias cleanup="r'm' -rf /fixture"`,
		`alias -p safe=echo cleanup="r'm' -rf /fixture"`,
		`builtin alias cleanup="r'm' -rf /fixture"`,
		`cleanup() { r'm' -rf /fixture; }`,
	} {
		input, _ := json.Marshal(map[string]string{"command": command, "text": command})
		for _, tool := range []string{"Bash", "shell_send"} {
			i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
			if decision, hit := i.Match(tool, input); !hit || decision.Action != "deny" {
				t.Fatalf("deferred command escaped: %q %+v %v", command, decision, hit)
			}
		}
	}
}

func TestGeneratedDeferredProgramsAndAliasFragmentsRequireApproval(t *testing.T) {
	for _, command := range []string{
		`trap "$HANDLER" EXIT`, `trap $HANDLER EXIT`,
		`builtin trap "$HANDLER" DEBUG`,
		`alias "$DEFINITION"`, `alias cleanup="echo $VALUE"`,
		`alias method='curl -q -X '`, `alias -p method='curl -q -X '`,
		`alias method='echo '`, `trap 'echo "' EXIT`,
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
		if decision, hit := i.Match("Bash", input); !hit || decision.Action != "ask" || decision.ModelFallback || decision.TimeoutAction != "deny" {
			t.Fatalf("generated deferred program passed: %q %+v %v", command, decision, hit)
		}
		i.cached = []compiledRule{semanticFixture("alias|trap", "allow"), semanticFixture(`\brm\s`, "deny")}
		if decision, hit := i.Match("Bash", input); !hit || decision.Action != "allow" {
			t.Fatal("operator allow priority lost")
		}
	}
}

func TestDeferredListingAndResetPreserveData(t *testing.T) {
	for _, command := range []string{
		`trap`, `trap -p`, `trap -P EXIT`, `trap -l`, `trap -lp`,
		`trap -p "$SIGNALS"`, `trap -p "r'm' -rf /fixture" EXIT`,
		`trap - EXIT`, `trap '' EXIT`, `trap EXIT`, `trap -- - EXIT`,
		`alias`, `alias -p`, `alias cleanup`, `alias -- cleanup`,
		`echo 'trap "r'"'"'m'"'"' -rf /fixture" EXIT'`,
		`bash -nc 'trap "$HANDLER" EXIT'`,
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
		assertUnconfinedShellReview(t, i, input)
	}
}
