package server

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestCommandTemplatePowerShellExpressionEvaluationCannotExecuteParameter(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	for _, program := range []string{
		`Invoke-Expression '{value}'`,
		`iex "{value}"`,
		`& (Get-Command Invoke-Expression) '{value}'`,
		`Set-Alias -Name sw-eval -Value Invoke-Expression; sw-eval '{value}'`,
		`Microsoft.PowerShell.Utility\Invoke-Expression '{value}'`,
		`Invoke-Expression -Command '{value}'`,
		`Invoke-Expression "$(Write-Output '{value}')"`,
		`Set-Alias -Name sw-eval -Value Invoke-Expression; sw-eval "$(Set-Alias -Name sw-eval -Value Write-Output){value}"`,
	} {
		t.Run(program, func(t *testing.T) {
			spec, _ := json.Marshal(map[string]string{"command": program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": `Write-Output SW_EVALUATION_OUTSIDE`}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || strings.Contains(result.Flatten(), "SW_EVALUATION_OUTSIDE") {
				t.Fatalf("PowerShell expression parameter reached execution: error=%v output=%q", result.IsError, result.Flatten())
			}
		})
	}
}

func TestCommandTemplatePowerShellFixedEvaluationAndDataRemainSupported(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	value := `quotes ' " $(Write-Output SW_EVALUATION_OUTSIDE); {other}`
	for _, program := range []string{
		`Write-Output '{value}'`,
		`Set-Alias -Name sw-data -Value Write-Output; sw-data '{value}'`,
		`function Invoke-Expression { param([string]$Command) Write-Output $Command }; Invoke-Expression '{value}'`,
		`Invoke-Expression 'Write-Output FIXED'; Write-Output '{value}'`,
	} {
		t.Run(program, func(t *testing.T) {
			spec, _ := json.Marshal(map[string]string{"command": program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": value, "other": "changed"}, nil)
			want := value
			if strings.HasPrefix(program, "Invoke-Expression 'Write-Output FIXED'") {
				want = "FIXED\r\n" + value
			}
			if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != want {
				t.Fatalf("fixed program/data workflow changed: output=%q want=%q error=%v", result.Flatten(), want, err)
			}
		})
	}
}

func TestCommandTemplatePowerShellScriptBlockParameterCannotExecute(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	for _, program := range []string{
		`& ([scriptblock]::Create('{value}'))`,
		`& ([System.Management.Automation.ScriptBlock]::Create("{value}"))`,
		`& ([scriptblock]::Create(('' + '{value}')))`,
		`& ([scriptblock]::Create([string]('{value}')))`,
		`& ([scriptblock]'{value}')`,
		`& ([System.Management.Automation.ScriptBlock]"{value}")`,
		`& ([scriptblock](('{value}')))`,
		`. ([scriptblock]::Create('{value}'))`,
	} {
		t.Run(program, func(t *testing.T) {
			spec, _ := json.Marshal(map[string]string{"command": program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": `Write-Output SW_SCRIPTBLOCK_OUTSIDE`}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || strings.Contains(result.Flatten(), "SW_SCRIPTBLOCK_OUTSIDE") {
				t.Fatalf("PowerShell script-block parameter reached execution: error=%v output=%q", result.IsError, result.Flatten())
			}
		})
	}
}

func TestCommandTemplatePowerShellFixedScriptBlocksRemainData(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	value := `quotes ' " $(Write-Output SW_SCRIPTBLOCK_OUTSIDE); {other}`
	for _, program := range []string{
		`& ([scriptblock]::Create('param([string]$data) Write-Output $data')) '{value}'`,
		`& ([System.Management.Automation.ScriptBlock]::Create('param([string]$data) Write-Output $data')) "{value}"`,
		`& ([scriptblock]{ param([string]$data) Write-Output $data }) '{value}'`,
		`& ([scriptblock]{ Write-Output '{value}' })`,
		`& ([scriptblock](({ Write-Output '{value}' })))`,
		`& { param([string]$data) Write-Output $data } '{value}'`,
		`[string]'{value}'`,
		`& ([scriptblock]::Create('Write-Output FIXED')); Write-Output '{value}'`,
	} {
		t.Run(program, func(t *testing.T) {
			spec, _ := json.Marshal(map[string]string{"command": program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": value, "other": "changed"}, nil)
			want := value
			if strings.Contains(program, "Create('Write-Output FIXED')") {
				want = "FIXED\r\n" + value
			}
			if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != want {
				t.Fatalf("fixed script-block data workflow changed: output=%q want=%q error=%v", result.Flatten(), want, err)
			}
		})
	}
}
