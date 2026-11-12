package intercept

import (
	"reflect"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestShellTemplateProgramRoles(t *testing.T) {
	for _, tc := range []struct {
		command string
		words   []string
		unknown bool
	}{
		{`bash -c 'echo fixed' fixture data`, []string{"bash", "-c", "'echo fixed'"}, false},
		{`command bash -ec 'echo fixed' fixture data`, []string{"command", "bash", "-ec", "'echo fixed'"}, false},
		{`env -i sh -c 'echo fixed' fixture data`, []string{"env", "-i", "sh", "-c", "'echo fixed'"}, false},
		{`timeout 2 sh -c 'echo fixed' fixture data`, []string{"timeout", "2", "sh", "-c", "'echo fixed'"}, false},
		{`bash -o pipefail -c 'echo fixed' fixture data`, []string{"bash", "-o", "pipefail", "-c", "'echo fixed'"}, false},
		{`bash -- fixture.sh data`, []string{"bash", "--"}, false},
		{`bash fixture.sh data`, []string{"bash", "fixture.sh"}, false},
		{`bash -n -c 'echo fixed'`, []string{"bash", "-n", "-c"}, false},
		{`builtin eval 'echo' data`, []string{"builtin", "eval", "'echo'", "data"}, false},
		{`command -v bash`, nil, false},
		{`printf '%s' data`, nil, false},
		{`bash -o "$OPTION" -c 'echo fixed'`, nil, true},
		{`env "$OPTION" sh -c 'echo fixed'`, nil, true},
		{`bash --unsupported -c 'echo fixed'`, nil, true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			file, err := syntax.NewParser().Parse(strings.NewReader(tc.command), "")
			if err != nil {
				t.Fatal(err)
			}
			call := file.Stmts[0].Cmd.(*syntax.CallExpr)
			protected, unknown := ShellTemplateProgramWords(call)
			var got []string
			for _, word := range protected {
				got = append(got, tc.command[word.Pos().Offset():word.End().Offset()])
			}
			if unknown != tc.unknown || !reflect.DeepEqual(got, tc.words) {
				t.Fatalf("roles = %q, unknown=%v; want %q, unknown=%v", got, unknown, tc.words, tc.unknown)
			}
		})
	}
}
