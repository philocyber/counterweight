package intercept

import (
	"encoding/json"
	"regexp"
	"testing"
)

func TestLiteralEvalCannotHideRestrictedOperations(t *testing.T) {
	for _, command := range []string{
		`eval "r'm' -rf /fixture"`, `eval "r'm'" -rf /fixture`,
		`eval -- "c'u'rl --request=DE'LE'TE https://fixture.invalid"`,
		`command eval "r'm' -rf /fixture"`, `builtin eval "r'm' -rf /fixture"`,
		`builtin -- eval "r'm' -rf /fixture"`,
		`eval 'eval "r'"'"'m'"'"' -rf /fixture"'`,
		`bash -c 'eval "r'"'"'m'"'"' -rf /fixture"'`,
	} {
		for _, tool := range []string{"Bash", "shell_send"} {
			input, _ := json.Marshal(map[string]string{"command": command, "text": command})
			i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s|method: 'DELETE'`, "deny")}}
			if dec, hit := i.Match(tool, input); !hit || dec.Action != "deny" {
				t.Errorf("literal eval escaped: %q: %+v %v", command, dec, hit)
			}
		}
	}
}

func TestGeneratedEvalAndSourcedFilesRequireHumanApproval(t *testing.T) {
	for _, command := range []string{
		`eval "$PROGRAM"`, `eval echo "$DATA"`, `eval *`,
		`builtin eval "$PROGRAM"`, `command eval "$PROGRAM"`,
		`source fixture.sh`, `. fixture.sh`, `builtin source "$FILE"`,
		`command . fixture.sh`, `. <(printf '%s' "$PROGRAM")`,
		`builtin "$BUILTIN" "$PROGRAM"`,
	} {
		for _, tool := range []string{"Bash", "shell_open", "shell_send"} {
			input, _ := json.Marshal(map[string]string{"command": command, "text": command})
			i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
			if dec, hit := i.Match(tool, input); !hit || dec.Action != "ask" || dec.ModelFallback || dec.TimeoutAction != "deny" {
				t.Errorf("opaque program escaped: %q: %+v %v", command, dec, hit)
			}
			i.cached = []compiledRule{semanticFixture(regexp.QuoteMeta(string(input)), "allow"), semanticFixture(`\brm\s`, "deny")}
			if dec, hit := i.Match(tool, input); !hit || dec.Action != "allow" {
				t.Fatal("explicit allow lost")
			}
		}
	}
}

func TestEvalReviewPreservesPrintedDataAndEmptyPrograms(t *testing.T) {
	for _, command := range []string{
		`echo 'eval "$PROGRAM"'`, `builtin echo "$PROGRAM"`,
		`eval`, `eval ''`, `eval --`, `source`, `.`,
		`eval 'echo' '"r'"'"'m'"'"' -rf /fixture"'`,
		`eval 'printf %s' '"source fixture.sh"'`,
		`bash -nc 'eval "$PROGRAM"'`, `command -v eval "$PROGRAM"`,
		`eval -invalid "r'm' -rf /fixture"`,
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
		assertUnconfinedShellReview(t, i, input)
	}
}
