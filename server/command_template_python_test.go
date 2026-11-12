package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCommandTemplatePythonProgramBoundary(t *testing.T) {
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires an explicit local Python fixture")
	}
	bash, err := exec.LookPath("bash")
	if runtime.GOOS == "windows" {
		bash = `C:\Program Files\Git\bin\bash.exe`
		_, err = os.Stat(bash)
	}
	if err != nil {
		t.Fatal(err)
	}
	python = shellQuote(filepath.ToSlash(python))
	value := `");print("SW_PY_PROGRAM_OUTSIDE");#`
	for _, tc := range []struct {
		name, program string
		denied        bool
	}{
		{"direct", python + ` -c 'print("{value}")'`, true},
		{"assigned", `source='print("{value}")'; ` + python + ` -c "$source"`, true},
		{"bundled", python + ` -Ic 'print("{value}")'`, true},
		{"attached_source", python + ` -c'print("{value}")'`, true},
		{"wrapped_source", `env ` + python + ` -Wignore -X utf8 -c 'print("{value}")'`, true},
		{"data", python + ` -c 'import sys;print(sys.argv[1],end="")' '{value}'`, false},
		{"assigned_data", `data='{value}'; ` + python + ` -Iu -c 'import sys;print(sys.argv[1],end="")' "$data"`, false},
		{"wrapped_data", `env ` + python + ` -W ignore -c 'import sys;print(sys.argv[1],end="")' '{value}'`, false},
		{"attached_options_data", python + ` -Wignore -Xutf8 -c 'import sys;print(sys.argv[1],end="")' '{value}'`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rendered, renderErr := renderBashCommandTemplate(tc.program, map[string]any{"value": value})
			if tc.denied && renderErr != nil {
				return
			}
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			cmd := exec.Command(bash, "--noprofile", "--norc", "-c", rendered)
			hideTemplateParserWindow(cmd)
			cmd.Env = append(os.Environ(), "BASH_ENV=", "ENV=")
			output, execErr := cmd.CombinedOutput()
			if tc.denied {
				t.Fatalf("parameter-bearing Python program accepted: output=%q error=%v markerExecuted=%v", output, execErr, strings.Contains(strings.ReplaceAll(string(output), "\r\n", "\n"), "\nSW_PY_PROGRAM_OUTSIDE\n"))
			}
			if execErr != nil || string(output) != value {
				t.Fatalf("Python data changed: output=%q error=%v want=%q", output, execErr, value)
			}
		})
	}
}

func TestCommandTemplatePythonStdinBoundary(t *testing.T) {
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicit cached Python")
	}
	bash, err := exec.LookPath("bash")
	if runtime.GOOS == "windows" {
		bash = `C:\Program Files\Git\bin\bash.exe`
		_, err = os.Stat(bash)
	}
	if err != nil {
		t.Fatal(err)
	}
	python = shellQuote(filepath.ToSlash(python))
	value := `print('SW_PY_STDIN_OUTSIDE')`
	for _, tc := range []struct {
		name, program string
		denied        bool
	}{
		{"pipe_default", `printf '%s' '{value}' | ` + python, true},
		{"pipe_dash", `printf '%s' '{value}' | ` + python + ` -`, true},
		{"here_string", python + ` - <<< '{value}'`, true},
		{"interactive_fixed", `printf '%s\n' '{value}' | ` + python + ` -i -c 'pass'`, true},
		{"fixed_stdin_data", `printf '%s' '{value}' | ` + python + ` -c 'import sys;print(sys.stdin.read().strip())'`, false},
		{"fixed_here_string_data", python + ` -c 'import sys;print(sys.stdin.read().strip())' <<< '{value}'`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rendered, renderErr := renderBashCommandTemplate(tc.program, map[string]any{"value": value})
			if tc.denied && renderErr != nil {
				return
			}
			if renderErr != nil {
				t.Fatal(renderErr)
			}
			cmd := exec.Command(bash, "--noprofile", "--norc", "-c", rendered)
			hideTemplateParserWindow(cmd)
			cmd.Env = append(os.Environ(), "BASH_ENV=", "ENV=", "PYTHONINSPECT=")
			output, execErr := cmd.CombinedOutput()
			if tc.denied {
				t.Fatalf("Python stdin source accepted: output=%q error=%v", output, execErr)
			}
			if execErr != nil || strings.TrimSpace(string(output)) != value {
				t.Fatalf("Python stdin data changed: output=%q error=%v", output, execErr)
			}
		})
	}
}
