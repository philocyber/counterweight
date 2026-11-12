package intercept

import (
	"encoding/json"
	"testing"

	"github.com/Philocyber/counterweight/locale"
)

func TestUnconfinedShellExecutionRequiresHumanApproval(t *testing.T) {
	for _, command := range []string{
		`python -c 'print("fixture")'`,
		`node -e 'console.log("fixture")'`,
		`renamed-client fixture-input`,
		`nmap "$TARGET"`,
		`curl -q --request GET https://fixture.invalid/resource`,
		`echo fixture`,
		`fixture_function() { echo fixture; }; fixture_function`,
		`make fixture`,
		``,
	} {
		for _, names := range [][]string{{"Bash"}, {"shell_open"}, {"shell_send"}, {"custom_fixture", "Bash"}} {
			input, _ := json.Marshal(map[string]string{"command": command, "text": command})
			i := &Interceptor{cached: []compiledRule{semanticFixture(`never-matching-restriction`, "deny")}}
			for _, lang := range []locale.Lang{locale.En} {
				dec, hit := i.MatchNames(names, input, lang)
				if !hit || dec.Action != "ask" || dec.RuleName != "Unconfined shell execution" || dec.ModelFallback || !dec.TimeoutEnabled || dec.TimeoutAction != "deny" {
					t.Errorf("unconfined execution automatically authorized: %q %v: %+v %v", command, names, dec, hit)
				}
				if dec.Message != locale.Text(locale.En, "Shell execution is not confined by input rules; human approval is required") {
					t.Error("mandatory authorization message missing or altered")
				}
			}
		}
	}
}

func assertUnconfinedShellReview(t *testing.T, i *Interceptor, input []byte) {
	t.Helper()
	analysis := analyzeOperation([]string{"Bash"}, input)
	if analysis.runtimeExecutable || analysis.requestMethodUnknown || analysis.shellProgramUnknown {
		t.Fatalf("data acquired an invented uncertainty: %+v", analysis)
	}
	dec, hit := i.Match("Bash", input)
	if !hit || dec.Action != "ask" || dec.RuleName != "Unconfined shell execution" || dec.ModelFallback || !dec.TimeoutEnabled || dec.TimeoutAction != "deny" || dec.ConfigDigest == "" {
		t.Fatalf("known data lost mandatory authorization: %+v %v", dec, hit)
	}
}

func TestUnconfinedReviewPreservesExplicitPolicyControls(t *testing.T) {
	input := []byte(`{"command":"echo fixture"}`)
	deny := semanticFixture(`unmatched`, "deny")
	for _, tc := range []struct {
		name  string
		rules []compiledRule
		names []string
		want  string
	}{
		{"inactive policy", []compiledRule{}, []string{"Bash"}, ""},
		{"allow-only policy", []compiledRule{semanticFixture(`unmatched`, "allow")}, []string{"Bash"}, ""},
		{"non-shell tool", []compiledRule{deny}, []string{"Write"}, ""},
		{"native HTTP tool", []compiledRule{deny}, []string{"WebFetch"}, ""},
		{"matching operator allow", []compiledRule{semanticFixture(`fixture`, "allow"), deny}, []string{"Bash"}, "allow"},
		{"matching operator deny", []compiledRule{semanticFixture(`fixture`, "deny"), semanticFixture(`fixture`, "allow")}, []string{"Bash"}, "deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dec, hit := (&Interceptor{cached: tc.rules}).MatchNames(tc.names, input)
			if hit != (tc.want != "") || dec.Action != tc.want {
				t.Fatalf("explicit policy changed: %+v %v", dec, hit)
			}
		})
	}
}
