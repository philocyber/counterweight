package intercept

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLiteralPipelinesCannotHideRestrictedOperations(t *testing.T) {
	commands := []string{
		`printf '%s\n' "r'm' -rf /fixture" | bash`,
		`printf '%s' "c'u'rl --request=DE'LE'TE https://fixture.invalid/resource" | sh`,
		`printf '%s%s\n' "r'" "m' -rf /fixture" | env -i bash -s`,
		`echo "r'm' -rf /fixture" | bash`,
		`printf '%s\n' "r'm' -rf /fixture" | cat | bash`,
		`{ printf '%s\n' "r'm' -rf /fixture"; } | (bash)`,
		`printf '%s\n' "r'm' -rf /fixture" | { bash; }`,
		`printf '%s\n' "r'm' -rf /fixture" |& bash`,
		"cat <<'EOF' | bash\nr'm' -rf /fixture\nEOF",
		`printf '%s\n' 'echo fixture' "r'm' -rf /fixture" | bash`,
		`printf '%s\n' "r'm' -rf /fixture" | (bash | cat)`,
		"{ cat | cat; } <<'EOF' | bash\nr'm' -rf /fixture\nEOF",
		`{ printf '%s\n' "r'm' -rf /fixture" | bash; } >/dev/null`,
		`printf '%s\n' "r'm' -rf /fixture"` + strings.Repeat(" | cat", 512) + " | bash",
	}
	for _, command := range commands {
		for _, action := range []string{"deny", "ask"} {
			i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s|method: 'DELETE'`, action)}}
			for _, tool := range []string{"Bash", "shell_send"} {
				input, _ := json.Marshal(map[string]string{"command": command, "text": command})
				if dec, hit := i.Match(tool, input); !hit || dec.Action != action {
					t.Errorf("%s literal pipe escaped %s: %q: %+v %v", tool, action, command, dec, hit)
				}
				i.cached = []compiledRule{semanticFixture(`/fixture|fixture.invalid`, "allow"), semanticFixture(`\brm\s|method: 'DELETE'`, action)}
				if dec, hit := i.Match(tool, input); !hit || dec.Action != "allow" {
					t.Error("explicit raw operator allow lost")
				}
				i.cached = []compiledRule{semanticFixture(`\brm\s|method: 'DELETE'`, action)}
			}
		}
	}
}

func TestLiteralPipelinesPreserveDataAndStdinOverrides(t *testing.T) {
	for _, command := range []string{
		`printf '%s\n' "r'm' -rf /fixture" | cat`,
		`printf '%s\n' "r'm' -rf /fixture" | bash -n`,
		`printf '%s\n' "r'm' -rf /fixture" | bash script.sh`,
		`printf '%s\n' "r'm' -rf /fixture" | bash -c 'echo fixture'`,
		`printf '%s\n' "r'm' -rf /fixture" | bash </dev/null`,
		`printf '%s\n' "r'm' -rf /fixture" | { bash <<< 'echo fixture'; }`,
		`printf '%s\n' "r'm' -rf /fixture" >/dev/null | bash`,
		`printf '%s\n' "$PROGRAM" | bash`,
		`echo -e "r'm' -rf /fixture" | bash`,                                                    // Unsupported echo escape mode.
		`printf '%999999999s' "r'm' -rf /fixture" | bash`,                                       // Never allocate format widths.
		`printf '` + strings.Repeat("x", 2048) + `%s'` + strings.Repeat(` ''`, 600) + ` | bash`, // Bound repeated literal output too.
		`printf '%s\n' "r'm' -rf /fixture" | { cat; bash; }`,
		`{ printf '%s\n' "r'm' -rf /fixture" | cat; } >/dev/null | bash`,
	} {
		if subjects := shellSubjects(command); len(subjects) != 0 {
			t.Errorf("data, unknown or replaced pipe input projected: %q: %v", command, subjects)
		}
	}
	command := `printf '%s\n' 'echo fixture' | bash <<< "r'm' -rf /fixture"`
	if subjects := shellSubjects(command); len(subjects) != 1 || subjects[0] != "rm -rf /fixture" {
		t.Errorf("explicit executable stdin override lost: %v", subjects)
	}
}
