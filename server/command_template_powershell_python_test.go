package server

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestCommandTemplatePowerShellPythonSourceBoundary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicit cached Python")
	}
	command := "& " + quotePowerShellLiteral(python)
	value := `");print("SW_PS_PY_SOURCE_OUTSIDE");#`
	for _, tc := range []struct {
		name, program string
		denied        bool
	}{
		{"direct", command + ` -c 'print("{value}")'`, true},
		{"assigned", `$source='print("{value}")'; ` + command + ` -c $source`, true},
		{"bundled", command + ` -Ic 'print("{value}")'`, true},
		{"attached", command + ` '-cprint("{value}")'`, true},
		{"alias", `Set-Alias sw_python ` + quotePowerShellLiteral(python) + `; sw_python -c 'print("{value}")'`, true},
		{"dynamic", `$engine=` + quotePowerShellLiteral(python) + `; & $engine -c 'print("{value}")'`, true},
		{"dynamic_option", `$mode='-c'; ` + command + ` $mode 'print("{value}")'`, true},
		{"data", command + ` -c 'import sys;print(sys.argv[1])' '{value}'`, false},
		{"assigned_data", `$data='{value}'; ` + command + ` -Iu -c 'import sys;print(sys.argv[1])' $data`, false},
		{"alias_data", `Set-Alias sw_python ` + quotePowerShellLiteral(python) + `; sw_python -W ignore -c 'import sys;print(sys.argv[1])' '{value}'`, false},
		{"attached_options_data", command + ` -Wignore -Xutf8 -c 'import sys;print(sys.argv[1])' '{value}'`, false},
		{"same_named_function", `function python {param($c) $c}; python -c '{value}'`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, _ := json.Marshal(map[string]string{"command": tc.program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": value}, nil)
			if err != nil {
				t.Fatal(err)
			}
			output := strings.TrimSpace(result.Flatten())
			if tc.denied {
				if !result.IsError || strings.Contains(output, "\nSW_PS_PY_SOURCE_OUTSIDE") || output == "SW_PS_PY_SOURCE_OUTSIDE" {
					t.Fatalf("Python source executed: error=%v output=%q", result.IsError, output)
				}
			} else if result.IsError || output != value {
				t.Fatalf("Python data changed: error=%v output=%q want=%q", result.IsError, output, value)
			}
		})
	}
}

func TestCommandTemplatePowerShellPythonStdinBoundary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicit cached Python")
	}
	command := "& " + quotePowerShellLiteral(python)
	value := "# stdin fixture\nprint('SW_PS_PY_STDIN_OUTSIDE')"
	for _, tc := range []struct {
		name, program string
		denied        bool
	}{
		{"default", `Write-Output '{value}' | ` + command, true},
		{"dash", `Write-Output '{value}' | ` + command + ` -`, true},
		{"interactive_fixed", `Write-Output '{value}' | ` + command + ` -i -c 'pass'`, true},
		{"alias", `Set-Alias sw_python ` + quotePowerShellLiteral(python) + `; Write-Output '{value}' | sw_python`, true},
		{"fixed_data", `Write-Output '{value}' | ` + command + ` -c ` + quotePowerShellLiteral("import sys;print(sys.stdin.buffer.read().decode('utf-8-sig').strip())"), false},
		{"managed_data", `function python {process {$_}}; Write-Output '{value}' | python`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The fixed reader explicitly accepts Windows UTF-8 preambles.
			// The initial comment separates a preamble error from interactive
			// execution of the next supplied line in the regression control.
			spec, _ := json.Marshal(map[string]string{"command": tc.program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": value}, nil)
			if err != nil {
				t.Fatal(err)
			}
			output := strings.TrimSpace(strings.ReplaceAll(result.Flatten(), "\r\n", "\n"))
			if tc.denied {
				if !result.IsError || output == "SW_PS_PY_STDIN_OUTSIDE" || strings.Contains(output, "\nSW_PS_PY_STDIN_OUTSIDE") {
					t.Fatalf("Python stdin source executed: error=%v output=%q", result.IsError, output)
				}
			} else if result.IsError || output != value {
				t.Fatalf("Python stdin data changed: error=%v output=%q", result.IsError, output)
			}
		})
	}
}

func TestCommandTemplatePowerShellFixedPythonSourceQuotes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicit cached Python")
	}
	value := `quotes ' " \ trailing; {other}`
	source := `import sys,json;print(json.dumps([sys.argv[1], "", "fixed"]))`
	for _, head := range []string{
		"& " + quotePowerShellLiteral(python),
		`Set-Alias sw_python ` + quotePowerShellLiteral(python) + `; sw_python`,
		`$engine=` + quotePowerShellLiteral(python) + `; & $engine`,
	} {
		t.Run(head, func(t *testing.T) {
			for _, assigned := range []bool{false, true} {
				program := head + ` -c '` + source + `' '{value}'`
				if assigned {
					program = `$source='` + source + `'; ` + head + ` -c $source '{value}'`
				}
				rendered, renderErr := renderPowerShellCommandTemplate(program, map[string]any{"value": value, "other": "changed"})
				if renderErr != nil || !strings.Contains(rendered, `import sys,json;print(json.dumps([sys.argv[1], "", "fixed"]))`) {
					t.Fatalf("fixed source disappeared from review text: error=%v", renderErr)
				}
				spec, _ := json.Marshal(map[string]string{"command": program})
				result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": value, "other": "changed"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				if result.IsError || json.Unmarshal([]byte(strings.TrimSpace(result.Flatten())), &got) != nil || len(got) != 3 || got[0] != value || got[1] != "" || got[2] != "fixed" {
					t.Fatalf("fixed Python source or data corrupted: error=%v output=%q", result.IsError, result.Flatten())
				}
			}
		})
	}
}
