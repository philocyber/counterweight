package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runPowerShellTemplate executes a rendered template and reports whether any
// output line is exactly marker: a parameter that only travelled as data shows
// the marker inside a longer line, a parameter that ran prints it alone.
func runPowerShellTemplate(t *testing.T, program, value, marker string) (output string, failed, executed bool) {
	t.Helper()
	spec, _ := json.Marshal(map[string]string{"command": program})
	result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": value, "other": "changed"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// PowerShell prefixes text it pipes to a native program with a byte-order mark.
	output = strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(result.Flatten(), "\r\n", "\n")), "\ufeff")
	for _, line := range strings.Split(output, "\n") {
		executed = executed || strings.TrimSpace(line) == marker
	}
	return output, result.IsError, executed
}

func TestCommandTemplatePowerShellQuoteClassCharactersStayData(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	for _, program := range []string{
		`$data = '{value}'; Write-Output $data.Length`,
		`$data = "{value}"; Write-Output $data.Length`,
		`$table = @{ Uri = '{value}' }; Write-Output $table.Uri.Length`,
		`Write-Output ('' + '{value}').Length`,
		`$base = 'pre'; Write-Output "$base/{value}".Length`,
	} {
		for _, quote := range []string{"\u201a", "\u201b", "\u201e", "\u2018", "\u2019", "\u201c", "\u201d", "'", `"`} {
			t.Run(program+quote, func(t *testing.T) {
				output, failed, executed := runPowerShellTemplate(t, program, "A"+quote+"; Write-Output SW_QUOTE_OUTSIDE #", "SW_QUOTE_OUTSIDE")
				if failed || executed {
					t.Fatalf("quote character ended the string: error=%v output=%q", failed, output)
				}
			})
		}
	}
}

func TestCommandTemplatePowerShellNestedProgramCannotExecuteParameter(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	shell := `C:\Program Files\Git\bin\bash.exe`
	if _, err := os.Stat(shell); err != nil {
		t.Skip("requires a Unix shell for Windows")
	}
	bash := "& " + quotePowerShellLiteral(shell)
	for _, program := range []string{
		`powershell -NoProfile -Command "{value}"`,
		`powershell -NoProfile -Command 'Write-Output fixed' '{value}'`,
		`& powershell.exe -NoProfile "{value}"`,
		`$code = '{value}'; powershell -NoProfile -Command $code`,
		bash + ` -c '{value}'`,
		bash + ` -c "printf fixed; {value}"`,
		bash + ` '{value}' fixed`,
		`'{value}' | powershell -NoProfile -Command -`,
		`'{value}' | powershell -NoProfile`,
		`'{value}' | powershell -ExecutionPolicy Bypass -NoProfile`,
		`'{value}' | ` + bash,
		`'{value}' | cmd`,
		`Start-Process cmd -ArgumentList '/c', '{value}' -Wait -NoNewWindow`,
		`Start-Process powershell -ArgumentList "-NoProfile -Command {value}" -Wait -NoNewWindow`,
		`$program = 'cmd'; Start-Process $program -ArgumentList '/c', '{value}' -Wait -NoNewWindow`,
		`Add-Type -TypeDefinition '{value}'`,
	} {
		t.Run(program, func(t *testing.T) {
			output, failed, executed := runPowerShellTemplate(t, program, "echo SW_NESTED_OUTSIDE", "SW_NESTED_OUTSIDE")
			if !failed || executed {
				t.Fatalf("parameter became a nested program: error=%v output=%q", failed, output)
			}
		})
	}
}

// An option's value is not the host's program: with only options and their
// values the host, like wsl without a command, still reads the pipeline.
func TestNestedHostOptionValuesAreNotPrograms(t *testing.T) {
	for words, want := range map[string]bool{
		"-NoProfile":                              true,
		"-ExecutionPolicy Bypass -NoProfile":      true,
		"-WindowStyle Hidden -ep Bypass":          true,
		"-ExecutionPolicy Bypass -File a.ps1":     false,
		"-ExecutionPolicy Bypass a.ps1":           false,
		"-ExecutionPolicy Bypass -Command -":      true,
		"-NoProfile -EncodedCommand ZQBjAGgAbwA=": false,
	} {
		if got := powerShellHostReadsStdin(strings.Fields(words)); got != want {
			t.Errorf("powershell %s: reads stdin = %v, want %v", words, got, want)
		}
	}
	for words, want := range map[string]bool{
		"":                     true,
		"-d Ubuntu":            true,
		"-d Ubuntu -u root":    true,
		"-d Ubuntu -e":         true,
		"-e <dynamic>":         true,
		"-d Ubuntu cat":        false,
		"-d Ubuntu -e cat":     false,
		"-d Ubuntu -- sort -u": false,
	} {
		if got := wslRunsDefaultShell(strings.Fields(words)); got != want {
			t.Errorf("wsl %s: default shell = %v, want %v", words, got, want)
		}
	}
}

func TestCommandTemplatePowerShellNestedProgramDataRemainsSupported(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	shell := `C:\Program Files\Git\bin\bash.exe`
	if _, err := os.Stat(shell); err != nil {
		t.Skip("requires a Unix shell for Windows")
	}
	script := filepath.Join(t.TempDir(), "fixture.ps1")
	if err := os.WriteFile(script, []byte("param([string]$data) Write-Output $data\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value := `data & echo SW_NESTED_OUTSIDE; {other}`
	for _, program := range []string{
		"& " + quotePowerShellLiteral(shell) + ` -c 'printf "%s" "$1"' fixture '{value}'`,
		`powershell -NoProfile -File ` + quotePowerShellLiteral(script) + ` '{value}'`,
		`'{value}' | & ` + quotePowerShellLiteral(shell) + ` -c 'cat'`,
		`'{value}' | cmd /d /c 'findstr data'`,
		`Start-Process -FilePath where.exe -ArgumentList '{value}' -WindowStyle Hidden -Wait; Write-Output '{value}'`,
	} {
		t.Run(program, func(t *testing.T) {
			output, failed, executed := runPowerShellTemplate(t, program, value, "SW_NESTED_OUTSIDE")
			if failed || executed || output != value {
				t.Fatalf("fixed nested program lost its data: error=%v output=%q want=%q", failed, output, value)
			}
		})
	}
}

func TestCommandTemplatePowerShellUnassignedVariableCannotExecuteParameter(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	for _, tc := range []struct{ program, value string }{
		{`foreach ($item in @('{value}')) { Invoke-Expression $item }`, "Write-Output SW_FLOW_OUTSIDE"},
		{`@('{value}') | ForEach-Object { Invoke-Expression $_ }`, "Write-Output SW_FLOW_OUTSIDE"},
		{`@('{value}') | ForEach-Object { iex $PSItem }`, "Write-Output SW_FLOW_OUTSIDE"},
		{`function Invoke-Fixture($code) { Invoke-Expression $code }; Invoke-Fixture '{value}'`, "Write-Output SW_FLOW_OUTSIDE"},
		{`& { param($code) Invoke-Expression $code } '{value}'`, "Write-Output SW_FLOW_OUTSIDE"},
		{`& { Invoke-Expression $args[0] } '{value}'`, "Write-Output SW_FLOW_OUTSIDE"},
		{`Set-Variable -Name code -Value '{value}'; Invoke-Expression $code`, "Write-Output SW_FLOW_OUTSIDE"},
		{`New-Variable code '{value}'; Invoke-Expression $code`, "Write-Output SW_FLOW_OUTSIDE"},
		{`$code = 'Write-Output fixed'; Set-Variable code '{value}'; Invoke-Expression $code`, "Write-Output SW_FLOW_OUTSIDE"},
		{`Write-Output '{value}' -OutVariable code | Out-Null; Invoke-Expression $code[0]`, "Write-Output SW_FLOW_OUTSIDE"},
		{`Write-Output '{value}' | Tee-Object -Variable code | Out-Null; Invoke-Expression $code`, "Write-Output SW_FLOW_OUTSIDE"},
		{`foreach ($item in @('{value}')) { & ([scriptblock]::Create($item)) }`, "Write-Output SW_FLOW_OUTSIDE"},
		{`foreach ($item in @('{value}')) { & $env:ComSpec /d /s /c $item }`, "echo SW_FLOW_OUTSIDE"},
		{`$env:SW_TEMPLATE_PROGRAM = '{value}'; & $env:ComSpec /d /s /c 'echo %SW_TEMPLATE_PROGRAM%'`, "fixed & echo SW_FLOW_OUTSIDE"},
	} {
		t.Run(tc.program, func(t *testing.T) {
			output, failed, executed := runPowerShellTemplate(t, tc.program, tc.value, "SW_FLOW_OUTSIDE")
			if !failed || executed {
				t.Fatalf("unassigned variable carried a parameter into a program: error=%v output=%q", failed, output)
			}
		})
	}
}

func TestCommandTemplatePowerShellUnassignedVariableDataPreserved(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	value := `quotes ' " $(Write-Output SW_FLOW_OUTSIDE); {other}`
	for _, program := range []string{
		`foreach ($item in @('{value}')) { Write-Output $item }`,
		`@('{value}') | ForEach-Object { Write-Output $_ }`,
		`function Show-Fixture($data) { Write-Output $data }; Show-Fixture '{value}'`,
		`Set-Variable -Name data -Value '{value}'; Write-Output $data`,
		`$fixed = 'Write-Output ' + "'" + "'"; Invoke-Expression $fixed; Write-Output '{value}'`,
		`& $env:ComSpec /d /s /c 'rem fixed'; foreach ($item in @('{value}')) { Write-Output $item }`,
	} {
		t.Run(program, func(t *testing.T) {
			output, failed, executed := runPowerShellTemplate(t, program, value, "SW_FLOW_OUTSIDE")
			if failed || executed || output != value {
				t.Fatalf("data workflow changed: error=%v output=%q want=%q", failed, output, value)
			}
		})
	}
}
