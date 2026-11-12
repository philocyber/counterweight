package intercept

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
)

func TestDeepLiteralShellsCannotHideRestrictedOperations(t *testing.T) {
	for _, fixture := range []struct{ command, pattern string }{
		{`r'm' -rf /fixture`, `\brm\s`},
		{`c'u'rl -'X'DE'LE'TE https://fixture.invalid/resource`, `method: 'DELETE'`},
	} {
		for _, depth := range []int{9, 12} {
			command := fixture.command
			for n := 0; n < depth; n++ {
				command = "bash -c " + strconv.Quote(command)
			}
			input, _ := json.Marshal(map[string]string{"command": command})
			i := &Interceptor{cached: []compiledRule{semanticFixture(fixture.pattern, "deny")}}
			dec, hit := i.Match("Bash", input)
			if !hit || dec.Action != "deny" {
				t.Fatalf("depth %d restricted operation escaped: %+v %v", depth, dec, hit)
			}
			i.cached = []compiledRule{semanticFixture(`/fixture`, "allow"), semanticFixture(fixture.pattern, "deny")}
			if dec, hit := i.Match("Bash", input); !hit || dec.Action != "allow" {
				t.Fatal("explicit raw operator override lost")
			}
			for _, dataCommand := range []string{"echo " + strconv.Quote(command), "bash -nc " + strconv.Quote(command)} {
				i.cached = []compiledRule{semanticFixture(fixture.pattern, "deny")}
				input, _ := json.Marshal(map[string]string{"command": dataCommand})
				assertUnconfinedShellReview(t, i, input)
			}
		}
	}
}

func semanticFixture(pattern, action string) compiledRule {
	return compiledRule{InterceptRule: db.InterceptRule{Enabled: true, MatchTarget: "tool_input", MatchType: "regex", Pattern: pattern, Action: action}, re: regexp.MustCompile(pattern)}
}

func TestLiteralShellStdinPolicy(t *testing.T) {
	for _, command := range []string{
		"bash <<'EOF'\nr'm' -rf /fixture\nEOF",
		"sh <<EOF\nr'm' -rf /fixture\nEOF",
		`bash <<<"r'm' -rf /fixture"`,
		"env -i bash -s -- positional <<'EOF'\nr'm' -rf /fixture\nEOF",
		"bash -o pipefail <<-'EOF'\n\tr'm' -rf /fixture\n\tEOF",
		"bash 0<<'EOF' 1>/dev/null\nr'm' -rf /fixture\nEOF",
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
		if dec, hit := i.Match("Bash", input); !hit || dec.Action != "deny" {
			t.Errorf("literal stdin escaped: %q: %+v %v", command, dec, hit)
		}
		i.cached = []compiledRule{semanticFixture(`/fixture`, "allow"), semanticFixture(`\brm\s`, "deny")}
		if dec, hit := i.Match("Bash", input); !hit || dec.Action != "allow" {
			t.Error("explicit raw allow lost")
		}
	}
	for _, prefix := range []string{"cat", "bash -n", "bash -o noexec", "bash script.sh", "bash -c 'echo fixture'", "bash 3", "bash < /dev/null"} {
		command := prefix + " <<'EOF'\nr'm' -rf /fixture\nEOF"
		// A later input redirect replaces the here-document; an earlier one does not.
		if prefix == "bash < /dev/null" {
			command = "bash <<'EOF' < /dev/null\nr'm' -rf /fixture\nEOF"
		}
		if subjects := shellSubjects(command); len(subjects) != 0 {
			t.Errorf("data/syntax-only stdin projected: %q: %v", command, subjects)
		}
	}
	for _, command := range []string{
		"bash 3<<'EOF'\nr'm' -rf /fixture\nEOF",
		"bash <<'EOF' <&-\nr'm' -rf /fixture\nEOF",
		"bash <<'EOF' <<< 'echo fixture'\nr'm' -rf /fixture\nEOF",
		"bash <<EOF\n$PROGRAM\nEOF",
	} {
		if subjects := shellSubjects(command); len(subjects) != 0 {
			t.Errorf("unknown or replaced stdin projected: %q: %v", command, subjects)
		}
	}
	for _, action := range []string{"deny", "ask"} {
		i := &Interceptor{cached: []compiledRule{semanticFixture(`method: 'DELETE'`, action)}}
		command := "bash <<'EOF'\nc'u'rl --request=DELETE https://fixture.invalid/resource\nEOF"
		for _, tool := range []string{"Bash", "shell_send"} {
			input, _ := json.Marshal(map[string]string{"command": command, "text": command})
			if dec, hit := i.Match(tool, input); !hit || dec.Action != action {
				t.Errorf("%s stdin method rule lost: %+v %v", tool, dec, hit)
			}
		}
	}
}

func TestGroupedLiteralShellStdinPolicy(t *testing.T) {
	for _, command := range []string{
		"{ bash; } <<'EOF'\nr'm' -rf /fixture\nEOF",
		"(bash) <<'EOF'\nr'm' -rf /fixture\nEOF",
		"{ (env -i bash -s); } <<'EOF'\nc'u'rl --request=DELETE https://fixture.invalid\nEOF",
		"{ bash 1>/dev/null; } <<<\"r'm' -rf /fixture\"",
		"{ bash & } <<'EOF'\nr'm' -rf /fixture\nEOF",
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s|method: 'DELETE'`, "deny")}}
		if dec, hit := i.Match("Bash", input); !hit || dec.Action != "deny" {
			t.Errorf("grouped literal stdin escaped: %q: %+v %v", command, dec, hit)
		}
	}
	for _, command := range []string{
		"{ cat; } <<'EOF'\nr'm' -rf /fixture\nEOF",
		"(bash -n) <<'EOF'\nr'm' -rf /fixture\nEOF",
		"{ bash </dev/null; } <<'EOF'\nr'm' -rf /fixture\nEOF",
		"{ bash <<< 'echo fixture'; } <<'EOF'\nr'm' -rf /fixture\nEOF",
		"{ cat; bash; } <<'EOF'\nr'm' -rf /fixture\nEOF",
	} {
		if subjects := shellSubjects(command); len(subjects) != 0 {
			t.Errorf("data, consumed, or replaced input projected: %q: %v", command, subjects)
		}
	}
}

func TestRestrictiveRulesSeeStaticShellOperations(t *testing.T) {
	deleteRule := semanticFixture(`(?i)\bcurl\b[^|\n&;"]{0,300}(?:-X\s*DELETE|--request\s+DELETE|-XDELETE)|\bwget\b[^|\n&;"]{0,300}--method[=\s]+DELETE`, "deny")
	destructive := semanticFixture(`(?i)\b(rm\s+-rf\s+/|mkfs|shutdown|reboot)`, "deny")
	for _, command := range []string{
		`curl --request=DELETE https://fixture.invalid/resource`,
		`curl -X 'DE''LETE' https://fixture.invalid/resource`,
		`c'u'rl -X"DELETE" https://fixture.invalid/resource`,
		`curl -sXDELETE https://fixture.invalid/resource`,
		`curl -vsSX DELETE https://fixture.invalid/resource`,
		`curl -H '` + strings.Repeat("a", 400) + `' --request=DELETE https://fixture.invalid/resource`,
		`wget --method="DELETE" https://fixture.invalid/resource`,
		`wget --method 'DE''LETE' https://fixture.invalid/resource`,
		`r'm' -rf /fixture`, `r"m" -rf /fixture`, `r\m -rf /fixture`,
		`m'k'fs.ext4 /fixture`, `m"k"fs /fixture`, `shut'down' now`,
		`true && r'm' -rf /fixture`, `echo $(r'm' -rf /fixture)`,
		`sudo -n -u root r'm' -rf /fixture`,
		`sudo --user=root -- r'm' -rf /fixture`,
		`env -i -u HOME MODE=fixture c'u'rl --request=DELETE https://fixture.invalid/resource`,
		`command -p r'm' -rf /fixture`, `exec -a fixture r'm' -rf /fixture`,
		`nice -n 5 nohup r'm' -rf /fixture`, `timeout --signal TERM 2s r'm' -rf /fixture`,
		`bash -lc "r'm' -rf /fixture"`, `sh -c "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`sudo -n env MODE=fixture bash -c "r'm' -rf /fixture"`,
		`env PATH="$PATH" c'u'rl --request=DELETE https://fixture.invalid/resource`,
		`sudo -n MODE="$MODE" r'm' -rf /fixture`,
		strings.Repeat("env ", 20) + `r'm' -rf /fixture`,
		`env -uHOME r'm' -rf /fixture`,
		`env -C/fixture c'u'rl --request=DELETE https://fixture.invalid/resource`,
		`nice -n5 r'm' -rf /fixture`,
		`timeout -sTERM -k1s 2s c'u'rl --request=DELETE https://fixture.invalid/resource`,
		`env -iuHOME r'm' -rf /fixture`,
		`env -iu HOME r'm' -rf /fixture`,
		`env -iC/fixture c'u'rl --request=DELETE https://fixture.invalid/resource`,
		`timeout -vsTERM -vk1s 2s c'u'rl --request=DELETE https://fixture.invalid/resource`,
		`bash -o pipefail -c "r'm' -rf /fixture"`,
		`bash -eo pipefail -c "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash -O extglob -c "r'm' -rf /fixture"`,
		`bash -c -o pipefail "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash +o noexec -c "r'm' -rf /fixture"`,
		`bash -n +n -c "r'm' -rf /fixture"`,
		`bash -co pipefail "r'm' -rf /fixture"`,
		`bash -eoc pipefail "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash -c -- "r'm' -rf /fixture"`,
	} {
		t.Run(command, func(t *testing.T) {
			i := &Interceptor{cached: []compiledRule{destructive, deleteRule}}
			input, _ := json.Marshal(map[string]string{"command": command})
			dec, hit := i.Match("Bash", input)
			if !hit || dec.Action != "deny" {
				t.Fatalf("static operation escaped: %+v %v", dec, hit)
			}
		})
	}
}

func TestOperationSubjectsPreserveRuleControls(t *testing.T) {
	deny := semanticFixture(`curl -X DELETE`, "deny")
	input := []byte(`{"command":"curl --request=DELETE https://fixture.invalid/resource"}`)
	for _, fixture := range []struct {
		name  string
		rules []compiledRule
		want  string
	}{
		{"deny", []compiledRule{deny}, "deny"},
		{"empty", []compiledRule{}, ""},
		{"raw operator override", []compiledRule{semanticFixture(`fixture.invalid/resource`, "allow"), deny}, "allow"},
		{"no new allow", []compiledRule{semanticFixture(`curl -X DELETE`, "allow")}, ""},
		{"approval", []compiledRule{semanticFixture(`curl -X DELETE`, "ask")}, "ask"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			i := &Interceptor{cached: fixture.rules}
			dec, hit := i.Match("Bash", input)
			if dec.Action != fixture.want || hit != (fixture.want != "") {
				t.Fatalf("%+v matched=%v", dec, hit)
			}
		})
	}
	for _, command := range []string{
		`curl -q -H '-XDELETE' https://fixture.invalid/resource`,
		`curl -q --data '-XDELETE' https://fixture.invalid/resource`,
		`curl -q -- https://fixture.invalid/resource/-XDELETE`,
		`echo "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		"cat <<'FIXTURE'\nc'u'rl --request=DELETE https://fixture.invalid/resource\nFIXTURE",
		`command -v c'u'rl --request=DELETE https://fixture.invalid/resource`,
		`sudo --help c'u'rl --request=DELETE https://fixture.invalid/resource`,
		`sh -n -c "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash script.sh "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`sh -c 'echo "$1"' fixture "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`env MODE="c'u'rl --request=DELETE https://fixture.invalid/resource" echo fixture`,
		`env -u"r'm' -rf /fixture" echo fixture`,
		`env -iu "r'm' -rf /fixture" echo fixture`,
		`env -iC"r'm' -rf /fixture" echo fixture`,
		`env -iz r'm' -rf /fixture`,
		`env -iu`,
		`nice -ninvalid r'm' -rf /fixture`,
		`timeout -sTERM -k1s 2s echo "r'm' -rf /fixture"`,
		`timeout -vsTERM -vk1s 2s echo "r'm' -rf /fixture"`,
		`timeout -vz 2s r'm' -rf /fixture`,
		`timeout -vs`,
		`bash -o noexec -c "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash -c -n "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash -o pipefail -c 'echo "$1"' fixture "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash -O "c'u'rl --request=DELETE https://fixture.invalid/resource" -c 'echo fixture'`,
		`bash -o invalid_option -c "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash -o 'pipefail noexec' -c "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash -o "$OPTION" -c "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash -o pipefail script.sh "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
		`bash -c`,
		`bash -co`,
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		i := &Interceptor{cached: []compiledRule{deny}}
		// Arguments remain data even when the script source itself is opaque.
		if subjects := operationSubjects([]string{"Bash"}, input); len(subjects) != 0 {
			t.Fatalf("data projected as an executable request: %s %v", command, subjects)
		}
		wantAsk := false
		switch command {
		case `bash script.sh "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
			`bash -O "c'u'rl --request=DELETE https://fixture.invalid/resource" -c 'echo fixture'`,
			`bash -o invalid_option -c "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
			`bash -o 'pipefail noexec' -c "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
			`bash -o "$OPTION" -c "c'u'rl --request=DELETE https://fixture.invalid/resource"`,
			`bash -o pipefail script.sh "c'u'rl --request=DELETE https://fixture.invalid/resource"`, `bash -co`:
			wantAsk = true
		}
		dec, hit := i.Match("Bash", input)
		wantReason := "Unconfined shell execution"
		if wantAsk {
			wantReason = "Unresolved shell program"
		}
		if !hit || dec.Action != "ask" || dec.RuleName != wantReason {
			t.Fatalf("data or authorization role lost: %s %+v", command, dec)
		}
	}
	if subjects := operationSubjects([]string{"Write"}, input); len(subjects) != 0 {
		t.Fatal("nonexecuting file write parsed as shell")
	}
	if subjects := shellSubjects(`$UNTRUSTED_PROGRAM --request=DELETE`); len(subjects) != 0 {
		t.Fatal("dynamic program invented")
	}
}

func TestStaticOperationsCoverInteractiveAndResolvedExecutors(t *testing.T) {
	r := semanticFixture(`curl -X DELETE|method\s*[:=]\s*['"]DELETE['"]`, "deny")
	for _, fixture := range []struct {
		names []string
		input string
	}{
		{[]string{"shell_send"}, `{"text":"curl --request=DELETE https://fixture.invalid/resource"}`},
		{[]string{"shell_open"}, `{"command":"curl --request=DELETE https://fixture.invalid/resource"}`},
		{[]string{"custom", "Bash"}, `{"command":"curl --request=DELETE https://fixture.invalid/resource"}`},
		{[]string{"custom", "WebFetch"}, `{"method":"DELETE","url":"https://fixture.invalid/resource"}`},
	} {
		i := &Interceptor{cached: []compiledRule{r}}
		if dec, hit := i.MatchNames(fixture.names, []byte(fixture.input)); !hit || dec.Action != "deny" {
			t.Fatalf("executor escape: %v %+v %v", fixture.names, dec, hit)
		}
	}
}
