package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestCommandTemplateNestedShellSDKBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix SDK uses Bash; Windows SDK uses PowerShell")
	}
	value := `quotes ' " $(printf SW_OUTSIDE); {other}`
	for _, tc := range []struct {
		program string
		denied  bool
	}{
		{`bash -c 'echo {value}'`, true},
		{`eval 'echo {value}'`, true},
		{`program={value}; eval "$program"`, true},
		{`program={value}; bash -c "$program"`, true},
		{`program={value}; next=$program; printf '%s' "$next" | bash`, true},
		{`run() { eval "$1"; }; run {value}`, true},
		{`inner() { eval "$1"; }; outer() { inner "$1"; }; outer {value}`, true},
		{`run() { bash -c 'program=$1; eval "$program"' fixture "$1"; }; run {value}`, true},
		{`set -- {value}; eval "$1"`, true},
		{`printf -v program '%s' {value}; eval "$program"`, true},
		{`printf '%s' '{value}' | bash`, true},
		{`bash <<< '{value}'`, true},
		{`bash -c 'printf "%s" "$1"' fixture '{value}'`, false},
		{`printf '%s' '{value}' | bash -c 'cat'`, false},
		{`data={value}; bash -c 'printf "%s" "$1"' fixture "$data"`, false},
		{`run() { bash -c 'printf "%s" "$1"' fixture "$1"; }; run {value}`, false},
		{`set -- {value}; bash -c 'printf "%s" "$1"' fixture "$1"`, false},
		{`printf -v data '%s' {value}; printf '%s' "$data"`, false},
	} {
		spec, _ := json.Marshal(map[string]string{"command": tc.program})
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value, "other": "changed"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.denied {
			if !result.IsError || strings.Contains(result.Flatten(), "SW_OUTSIDE") {
				t.Fatalf("SDK executed interpolated program: %q", result.Flatten())
			}
		} else if result.IsError || result.Flatten() != value {
			t.Fatalf("SDK changed data: %q", result.Flatten())
		}
	}
}

func TestCommandTemplateShellStdinDataRemainsLiteral(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if runtime.GOOS == "windows" {
		bash = `C:\Program Files\Git\bin\bash.exe`
		_, err = os.Stat(bash)
	}
	if err != nil {
		t.Skip("requires actual Bash")
	}
	value := `printf SW_OUTSIDE`
	for _, tc := range []struct {
		program, output string
	}{
		{`printf '%s' '{value}' | cat`, value},
		{`bash -c 'cat' <<< '{value}'`, value + "\n"},
		{`printf '%s' '{value}' | bash -c 'cat'`, value},
		{`printf '%s' '{value}' | bash -n`, ""},
		{`printf '%s' '{value}' | bash <<< 'printf fixed'`, "fixed"},
		{`{ printf 'printf fixed' | bash; } <<< '{value}'`, "fixed"},
		{`bash 3<<< '{value}' 0<&3 <<< 'printf fixed'`, "fixed"},
		{`bash -c 'cat <&3' 3<<< '{value}'`, value + "\n"},
		{`{ printf 'printf fixed' | bash; } 3<<< '{value}'`, "fixed"},
		{`{ cat <<< 'printf fixed' | bash; } <<< '{value}'`, "fixed"},
		{`{ cat <&3 | bash; } 0<<< '{value}' 3<<< 'printf fixed'`, "fixed"},
		{`printf 'printf fixed' <<< '{value}' | bash`, "fixed"},
	} {
		rendered, err := renderBashCommandTemplate(tc.program, map[string]any{"value": value})
		if err != nil {
			t.Fatalf("data workflow refused: %s: %v", tc.program, err)
		}
		cmd := exec.Command(bash, "--noprofile", "--norc", "-c", rendered)
		hideTemplateParserWindow(cmd)
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			if !strings.EqualFold(key, "BASH_ENV") && !strings.EqualFold(key, "ENV") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != tc.output {
			t.Fatalf("stdin data changed: %s => %q %v; want %q", tc.program, output, err, tc.output)
		}
	}
}

func TestCommandTemplateShellStdinRedirectionBindings(t *testing.T) {
	for _, program := range []string{
		`{ printf 'printf fixed' | bash; } > '{value}'`,
		`{ printf 'printf fixed' | bash; } 0< '{value}' <<< 'unused fixed'`,
		`printf '%s' '{value}' | command -v bash`,
		`printf '%s' '{value}' | bash -c 'cat' 0<&0`,
	} {
		if _, err := renderBashCommandTemplate(program, map[string]any{"value": "output/input fixture"}); err != nil {
			t.Fatalf("data or overwritten binding refused: %s: %v", program, err)
		}
	}
}

func TestCommandTemplateRejectsShellProgramInterpolation(t *testing.T) {
	for _, program := range []string{
		`bash -c 'echo {value}'`,
		`sh -c 'echo {value}'`,
		`env -i bash --noprofile --norc -c 'echo {value}'`,
		`command bash -ec 'echo {value}'`,
		`timeout 2 sh -c 'echo {value}'`,
		`eval 'echo {value}'`,
		`builtin eval 'echo {value}'`,
		`bash -o pipefail -c 'echo {value}'`,
		`bash {value} 'echo SW_OUTSIDE'`,
		`bash -n {value} 'echo SW_OUTSIDE'`,
		`bash -o {value} -c 'echo fixed'`,
		`env {value} bash -c 'echo fixed'`,
		`bash --unsupported -c 'echo {value}'`,
	} {
		if rendered, err := renderBashCommandTemplate(program, map[string]any{"value": "FIXED_ECHO; printf SW_OUTSIDE"}); err == nil {
			t.Errorf("program interpolation accepted: %s => %s", program, rendered)
		}
	}
}

func TestCommandTemplateRejectsShellStdinProgramInterpolation(t *testing.T) {
	for _, program := range []string{
		`printf '%s' '{value}' | bash`,
		`printf '%s' '{value}' | cat | env -i PATH=/usr/bin:/bin sh -s`,
		`printf '%s' '{value}' | { bash; }`,
		`printf '%s' '{value}' | { :; bash; }`,
		`printf '%s' '{value}' | (bash)`,
		`bash <<< '{value}'`,
		`bash < '{value}'`,
		`{ bash; } <<< '{value}'`,
		`{ cat | bash; } <<< '{value}'`,
		`printf '%s' '{value}' | bash -ps`,
		`printf '%s' '{value}' | env -S 'bash -s'`,
		`bash <<< 'echo fixed' < '{value}'`,
		`printf '%s' '{value}' | bash 0<&0`,
		`bash 3<<< '{value}' 0<&3`,
		`bash 3<<< '{value}' 0>&3`,
		`{ cat <&3 | bash; } 3<<< '{value}'`,
	} {
		if rendered, err := renderBashCommandTemplate(program, map[string]any{"value": "printf SW_OUTSIDE"}); err == nil {
			t.Errorf("stdin program interpolation accepted: %s => %s", program, rendered)
		}
	}
}

func TestCommandTemplateNestedShellDataArgumentsRemainLiteral(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if runtime.GOOS == "windows" {
		bash = `C:\Program Files\Git\bin\bash.exe`
		_, err = os.Stat(bash)
	}
	if err != nil {
		t.Skip("requires actual Bash")
	}
	for _, program := range []string{
		`bash --noprofile --norc -c 'printf "%s" "$1"' fixture '{value}'`,
		`env -i PATH=/usr/bin:/bin bash --noprofile --norc -c 'printf "%s" "$1"' fixture '{value}'`,
		`command bash -c 'printf "%s" "$1"' fixture "{value}"`,
		`bash -o pipefail -c 'printf "%s" "$1"' fixture '{value}'`,
	} {
		value := `quotes ' " $(printf SW_OUTSIDE); {other}`
		rendered, err := renderBashCommandTemplate(program, map[string]any{"value": value, "other": "changed"})
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bash, "--noprofile", "--norc", "-c", rendered)
		hideTemplateParserWindow(cmd)
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			if !strings.EqualFold(key, "BASH_ENV") && !strings.EqualFold(key, "ENV") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != value {
			t.Fatalf("nested data argument changed: %s => %q %v", program, output, err)
		}
	}
}
