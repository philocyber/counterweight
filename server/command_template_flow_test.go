package server

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func parameterFlowBash(t *testing.T) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if runtime.GOOS == "windows" {
		bash = `C:\Program Files\Git\bin\bash.exe`
		_, err = os.Stat(bash)
	}
	if err != nil {
		t.Skip("requires actual Bash")
	}
	return bash
}

func TestCommandTemplateRejectsVariableFedShellPrograms(t *testing.T) {
	bash := parameterFlowBash(t)
	for _, program := range []string{
		`program={value}; eval "$program"`,
		`program={value}; eval '$program'`,
		`program={value}; eval "\$program"`,
		`export program={value}; bash -c 'eval "$program"'`,
		`program='{value}'; bash -c "$program"`,
		`program="{value}"; sh -c "$program"`,
		`program={value}; next=$program; eval "$next"`,
		`program={value}; next=$(printf '%s' "$program"); eval "$next"`,
		`program={value}; fixed=''; next=$(printf '%s' "$program" '$fixed'); eval "$next"`,
		`program=('{value}'); eval "${program[0]}"`,
		`export program={value}; eval "$program"`,
		`for program in '{value}'; do eval "$program"; done`,
		`program={value}; printf '%s' "$program" | bash`,
		`program={value}; bash <<< "$program"`,
		`program={value}; { cat | bash; } <<< "$program"`,
		`program={value}; selector=program; eval "${!selector}"`,
		`program={value}; if false; then program='printf fixed'; fi; eval "$program"`,
		`run() { eval "$1"; }; run {value}`,
		`run() { eval '$1'; }; run {value}`,
		`run() { local program=$1; eval "$program"; }; run {value}`,
		`inner() { eval "$1"; }; outer() { inner "$1"; }; outer {value}`,
		`run() { printf '%s' "$@" | bash; }; run {value}`,
		`run() { eval "$2"; }; run fixed {value}`,
		`run() { bash -c 'eval "$1"' fixture "$1"; }; run {value}`,
		`run() { bash -c 'program=$1; eval "$program"' fixture "$1"; }; run {value}`,
		`run() { command bash -c 'eval "$1"' fixture "$1"; }; run {value}`,
		`set -- {value}; eval "$1"`,
		`set -- {value}; eval '$1'`,
		`set -- fixed {value}; shift; eval "$1"`,
		`set -- {value}; program=$1; eval "$program"`,
		`run() { set -- {value}; eval "$1"; }; run`,
		`printf -v program '%s' {value}; eval "$program"`,
		`printf -vprogram '%s' {value}; eval "$program"`,
		`builtin printf -v program '%s' {value}; eval "$program"`,
		`printf -v 'program[0]' '%s' {value}; eval "${program[0]}"`,
		`data={value}; printf -v program '%s' "$data"; eval "$program"`,
	} {
		t.Run(program, func(t *testing.T) {
			rendered, err := renderBashCommandTemplate(program, map[string]any{"value": "printf SW_VARIABLE_OUTSIDE"})
			if err != nil {
				return
			}
			// A missing refusal runs only an inert local printf, making the
			// control prove that accepted literal data is reparsed as code.
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", rendered)
			hideTemplateParserWindow(cmd)
			output, runErr := cmd.CombinedOutput()
			t.Fatalf("variable-fed program was accepted: output=%q execution_error=%v", output, runErr)
		})
	}
}

func TestCommandTemplateVariableDataWorkflowsRemainLiteral(t *testing.T) {
	bash := parameterFlowBash(t)
	value := `quotes ' " $(printf SW_VARIABLE_OUTSIDE); {other}`
	for _, program := range []string{
		`data={value}; printf '%s' "$data"`,
		`data='{value}'; next=$data; printf '%s' "$next"`,
		`data="{value}"; printf '%s' "$data" | bash -c 'cat'`,
		`data={value}; bash -c 'printf "%s" "$1"' fixture "$data"`,
		`data=('{value}'); printf '%s' "${data[0]}"`,
		`for data in '{value}'; do printf '%s' "$data"; done`,
		`program='printf fixed'; data={value}; eval "$program"; printf '%s' "$data"`,
		`run() { printf '%s' "$1"; }; run {value}`,
		`run() { local data=$1; printf '%s' "$data"; }; run {value}`,
		`run() { bash -c 'printf "%s" "$1"' fixture "$1"; }; run {value}`,
		`run() { command bash -c 'printf "%s" "$1"' fixture "$1"; }; run {value}`,
		`set -- {value}; printf '%s' "$1"`,
		`set -- fixed {value}; shift; printf '%s' "$1"`,
		`set -- {value}; bash -c 'printf "%s" "$1"' fixture "$1"`,
		`run() { set -- {value}; printf '%s' "$1"; }; run`,
		`printf -v data '%s' {value}; printf '%s' "$data"`,
		`printf -vdata '%s' {value}; printf '%s' "$data"`,
		`printf -v 'data[0]' '%s' {value}; printf '%s' "${data[0]}"`,
	} {
		t.Run(program, func(t *testing.T) {
			rendered, err := renderBashCommandTemplate(program, map[string]any{"value": value, "other": "changed"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", rendered)
			hideTemplateParserWindow(cmd)
			output, err := cmd.CombinedOutput()
			want := value
			if strings.HasPrefix(program, "program=") {
				want = "fixed" + value
			}
			if err != nil || string(output) != want {
				t.Fatalf("literal variable workflow changed: output=%q want=%q error=%v", output, want, err)
			}
		})
	}
}
