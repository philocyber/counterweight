package intercept

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func parseTestWord(t *testing.T, source string) *syntax.Word {
	t.Helper()
	file, err := syntax.NewParser().Parse(strings.NewReader("echo "+source), "")
	if err != nil {
		t.Fatal(err)
	}
	call := file.Stmts[0].Cmd.(*syntax.CallExpr)
	return call.Args[1]
}

func TestStaticWordBraceExpansionIsNotStatic(t *testing.T) {
	for _, source := range []string{
		`{1..99999999999}`, `{1..2}{1..2}{1..2}{1..2}{1..2}{1..2}{1..2}{1..2}{1..2}{1..2}{1..2}{1..2}`,
		`{a,b}`, `x{a,b}y`, `{a,{b,c}}`, `{a..e}`, `{1..10..2}`, `{a,"b"}`, `{a"x",b}`,
	} {
		start := time.Now()
		if value, ok := staticWord(parseTestWord(t, source)); ok {
			t.Fatalf("%s resolved statically to %q", source, value)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("%s took %s", source, time.Since(start))
		}
	}
}

func TestStaticWordKeepsLiteralBraces(t *testing.T) {
	for source, want := range map[string]string{
		`{value}`: `{value}`, `'{value}'`: `{value}`, `"{value}"`: `{value}`, `a{value}b`: `a{value}b`,
		`{}`: `{}`, `'{a,b}'`: `{a,b}`, `"{1..3}"`: `{1..3}`, `\{a,b\}`: `{a,b}`, `{a},{b}`: `{a},{b}`,
		`a\ b`: `a b`, `"a b"'c'`: `a bc`, `{value`: `{value`, `a,b`: `a,b`, `x..y`: `x..y`,
	} {
		got, ok := staticWord(parseTestWord(t, source))
		if !ok || got != want {
			t.Fatalf("%s resolved to %q %v, want %q", source, got, ok, want)
		}
	}
}

func TestBraceRangeCommandRequiresReview(t *testing.T) {
	for _, command := range []string{`echo {1..99999999999} {a,b}{c,d}`, `{1..99999999999} fixture`, `curl -X {GET,DELETE} https://fixture.invalid/`} {
		input, _ := json.Marshal(map[string]string{"command": command})
		start := time.Now()
		analysis := analyzeOperation([]string{"Bash"}, input)
		if time.Since(start) > time.Second {
			t.Fatalf("%q took %s", command, time.Since(start))
		}
		if strings.HasPrefix(command, "echo") {
			continue // Arguments stay data; only the elapsed time matters.
		}
		if !analysis.runtimeExecutable && !analysis.requestMethodUnknown {
			t.Fatalf("%q was treated as fully resolved: %+v", command, analysis)
		}
	}
}

func singleQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func TestNestedPrintfPipelinesAreBounded(t *testing.T) {
	command := "echo fixture"
	for len(command) < 4000 {
		quoted := singleQuote(command)
		command = "printf '%s;%s;%s;%s\n' " + quoted + " " + quoted + " " + quoted + " " + quoted + " | sh"
	}
	if len(command) > 200000 {
		t.Fatalf("fixture too large: %d", len(command))
	}
	input, _ := json.Marshal(map[string]string{"command": command})
	i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
	start := time.Now()
	dec, hit := i.Match("Bash", input)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("analysis took %s", time.Since(start))
	}
	if !hit || dec.Action != "ask" || !dec.TimeoutEnabled || dec.TimeoutAction != "deny" {
		t.Fatalf("nested pipelines were not held for review: %+v %v", dec, hit)
	}
}

func TestAnalysisBudgetFailsClosed(t *testing.T) {
	// A program fanning out into more derived programs than the budget allows
	// must be reported as not inspectable.
	var b strings.Builder
	for n := 0; n < maxAnalysisPrograms+10; n++ {
		b.WriteString("bash -c " + strconv.Quote("echo "+strconv.Itoa(n)) + "\n")
	}
	if analysis := analyzeShell(b.String()); !analysis.shellProgramUnknown {
		t.Fatal("program count bound did not fail closed")
	}
	big := "echo " + strings.Repeat("a", maxAnalysisBytes+1)
	if analysis := analyzeShell(big); !analysis.shellProgramUnknown {
		t.Fatal("byte bound did not fail closed")
	}
	i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
	input, _ := json.Marshal(map[string]string{"command": b.String()})
	dec, hit := i.Match("Bash", input)
	if !hit || dec.Action != "ask" || !dec.HumanRequired {
		t.Fatalf("exceeded budget did not require a person: %+v %v", dec, hit)
	}
}

func TestUncertainShellDecisionsRequireHuman(t *testing.T) {
	i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
	for command, want := range map[string]bool{
		`"$PROGRAM" data`: true,
		`curl -X "$METHOD" https://fixture.invalid/`:         true,
		`bash -c "$PROGRAM"`:                                 true,
		`curl -K fixture.cfg https://fixture.invalid/`:       true,
		`wget --config=fixture.cfg https://fixture.invalid/`: true,
		`echo fixture`: false,
		// Only the client's own default startup file leaves these open; the
		// task request answers that generic prompt.
		`curl https://fixture.invalid/`:    false,
		`curl -q https://fixture.invalid/`: false,
		`wget https://fixture.invalid/`:    false,
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		dec, hit := i.Match("Bash", input)
		if !hit || dec.Action != "ask" || dec.HumanRequired != want {
			t.Fatalf("%q: %+v %v", command, dec, hit)
		}
	}
}
