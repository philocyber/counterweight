package server

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestCommandTemplatePowerShellPipelineEvaluationRefused(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	for _, program := range []string{
		`'{value}' | Invoke-Expression`,
		`Write-Output '{value}' | iex`,
		`$data='{value}'; $data | Invoke-Expression`,
		`$data='{value}'; $copy=$data; Write-Output $copy | iex`,
		`'{value}' | Write-Output | Invoke-Expression`,
		`Set-Alias sw-pipeline Invoke-Expression; '{value}' | sw-pipeline`,
		`'{value}' | & (Get-Command Invoke-Expression)`,
		`'{value}' | Microsoft.PowerShell.Utility\Invoke-Expression`,
	} {
		t.Run(program, func(t *testing.T) {
			spec, _ := json.Marshal(map[string]string{"command": program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": "Write-Output SW_PIPELINE_OUTSIDE"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || strings.Contains(result.Flatten(), "SW_PIPELINE_OUTSIDE") {
				t.Fatalf("pipeline data reached code evaluation: error=%v output=%q", result.IsError, result.Flatten())
			}
		})
	}
}

func TestCommandTemplatePowerShellPipelineDataPreserved(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	value := `quotes ' " $(Write-Output SW_PIPELINE_OUTSIDE); {other}`
	for _, program := range []string{
		`'{value}' | Write-Output`,
		`Write-Output '{value}' | Write-Output | Write-Output`,
		`$data='{value}'; $copy=$data; $copy | Write-Output`,
		`Set-Alias sw-data Write-Output; '{value}' | sw-data`,
		`function Invoke-Expression { process { Write-Output $_ } }; '{value}' | Invoke-Expression`,
		`'Write-Output FIXED' | Invoke-Expression; Write-Output '{value}'`,
	} {
		t.Run(program, func(t *testing.T) {
			spec, _ := json.Marshal(map[string]string{"command": program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": value, "other": "changed"}, nil)
			want := value
			if strings.HasPrefix(program, "'Write-Output FIXED'") {
				want = "FIXED\r\n" + value
			}
			if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != want {
				t.Fatalf("pipeline data changed: output=%q want=%q error=%v", result.Flatten(), want, err)
			}
		})
	}
}
