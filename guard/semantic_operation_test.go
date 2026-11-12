package guard

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
)

func TestSeededRulesBlockEquivalentOperations(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ic := intercept.New(d)
	prior, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ic.SetEnabledTools(prior) })
	if err := ic.SetEnabledTools([]string{"Bash", "shell_send"}); err != nil {
		t.Fatal(err)
	}
	g := NewWithInterceptor(ic)
	commands := []string{`curl --request=DELETE http://fixture.invalid/resource`, `c'u'rl -sXDELETE http://fixture.invalid/resource`, `r'm' -rf /fixture`, `m'k'fs.ext4 /fixture`, `sudo -n r'm' -rf /fixture`, `env MODE=fixture c'u'rl --request=DELETE http://fixture.invalid/resource`, `bash -lc "r'm' -rf /fixture"`}
	commands = append(commands, `env -uHOME r'm' -rf /fixture`, `env -C/fixture c'u'rl --request=DELETE http://fixture.invalid/resource`, `nice -n5 r'm' -rf /fixture`, `timeout -sTERM -k1s 2s c'u'rl --request=DELETE http://fixture.invalid/resource`)
	commands = append(commands, `env -iuHOME r'm' -rf /fixture`, `env -iu HOME r'm' -rf /fixture`, `env -iC/fixture c'u'rl --request=DELETE http://fixture.invalid/resource`, `timeout -vsTERM -vk1s 2s c'u'rl --request=DELETE http://fixture.invalid/resource`)
	commands = append(commands, `bash -o pipefail -c "r'm' -rf /fixture"`, `bash -eo pipefail -c "c'u'rl --request=DELETE http://fixture.invalid/resource"`, `bash -O extglob -c "r'm' -rf /fixture"`, `bash -c -o pipefail "c'u'rl --request=DELETE http://fixture.invalid/resource"`, `bash +o noexec -c "r'm' -rf /fixture"`, `bash -n +n -c "r'm' -rf /fixture"`, `bash -co pipefail "r'm' -rf /fixture"`, `bash -eoc pipefail "c'u'rl --request=DELETE http://fixture.invalid/resource"`, `bash -c -- "r'm' -rf /fixture"`)
	for _, base := range []string{`r'm' -rf /fixture`, `c'u'rl -'X'DE'LE'TE http://fixture.invalid/resource`} {
		commands = append(commands, "printf '%s\\n' "+strconv.Quote(base)+" | bash", "printf '%s\\n' "+strconv.Quote(base)+" | cat | env -i sh -s", "printf '%s\\n' "+strconv.Quote(base)+" | { (bash); }")
		commands = append(commands, "{ bash; } <<'EOF'\n"+base+"\nEOF", "(bash) <<'EOF'\n"+base+"\nEOF", "{ (env -i bash -s); } <<'EOF'\n"+base+"\nEOF", "{ bash & } <<'EOF'\n"+base+"\nEOF")
		commands = append(commands, "bash <<'EOF'\n"+base+"\nEOF", "env -i bash -s -- positional <<'EOF'\n"+base+"\nEOF", "bash <<<"+strconv.Quote(base))
		command := base
		for n := 0; n < 12; n++ {
			command = "bash -c " + strconv.Quote(command)
		}
		commands = append(commands, command)
	}
	for _, command := range commands {
		input, _ := json.Marshal(map[string]string{"command": command})
		blocked, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", input)
		if !blocked {
			t.Fatalf("production guard allowed %s", command)
		}
		interactive, _ := json.Marshal(map[string]string{"text": command})
		if blocked, _, _ := g.Hooks().PreToolUse(context.Background(), "shell_send", interactive); !blocked {
			t.Fatal("production guard allowed the equivalent interactive command")
		}
	}
	// A deny rule wins over an allow rule whatever their priorities, including when
	// the deny matches only through the parsed view: an allow for a target must not
	// permit one spelling of an operation that another spelling is denied.
	rule, err := d.CreateInterceptRule("semantic fixture operator override", "tool_input", "string", "fixture.invalid/resource", "allow", "", 1000000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteInterceptRule(rule.ID) })
	ic.Invalidate()
	input := []byte(`{"command":"curl --request=DELETE http://fixture.invalid/resource"}`)
	if blocked, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", input); !blocked {
		t.Fatal("an allow rule overrode a deny rule")
	}
	// The allow rule still does its job for an operation no deny rule covers.
	read := []byte(`{"command":"curl http://fixture.invalid/resource"}`)
	if blocked, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", read); blocked {
		t.Fatal("explicit operator allow lost")
	}
	// Permitting the denied operation takes an explicit change to the deny rules themselves
	// (here: every built-in rule about the HTTP DELETE method).
	rules, err := d.ListInterceptRules()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.Action != "deny" || !r.Enabled || !strings.Contains(r.Name, "DELETE") {
			continue
		}
		if _, err := d.UpdateInterceptRule(r.ID, r.Name, r.MatchTarget, r.MatchType, r.Pattern, r.Action, r.Message, r.Priority, false, r.TimeoutEnabled, r.TimeoutSeconds, r.TimeoutAction); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = d.UpdateInterceptRule(r.ID, r.Name, r.MatchTarget, r.MatchType, r.Pattern, r.Action, r.Message, r.Priority, true, r.TimeoutEnabled, r.TimeoutSeconds, r.TimeoutAction)
			ic.Invalidate()
		})
	}
	ic.Invalidate()
	if blocked, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", input); blocked {
		t.Fatal("disabling the deny rule did not let the operator allow apply")
	}
	if err := d.DeleteInterceptRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetEnabledTools([]string{}); err != nil {
		t.Fatal(err)
	}
	if blocked, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", input); blocked {
		t.Fatal("deliberately disabled interception no longer honored")
	}
}
