package server

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestCommandTemplatePowerShellVariableEvaluationRefused(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	for _, program := range []string{
		`$program='{value}'; Invoke-Expression $program`,
		`$program='{value}'; $copy=$program; iex $copy`,
		`$program=(''+'{value}'); Invoke-Expression $program`,
		`$script:program='{value}'; Invoke-Expression $program`,
		`$env:SW_TEMPLATE_PROGRAM='{value}'; iex $env:SW_TEMPLATE_PROGRAM`,
		`$program='{value}'; & ([scriptblock]::Create($program))`,
		`$program='{value}'; $copy=$program; & ([scriptblock]::Create($copy))`,
		`$program='{value}'; if ($false) {$program='Write-Output fixed'}; iex $program`,
		`$program='{value}'; Invoke-Expression -Command:$program`,
	} {
		t.Run(program, func(t *testing.T) {
			spec, _ := json.Marshal(map[string]string{"command": program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": `Write-Output SW_PS_FLOW_OUTSIDE`}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || strings.Contains(result.Flatten(), "SW_PS_FLOW_OUTSIDE") {
				t.Fatalf("parameter-derived PowerShell program was accepted: error=%v output=%q", result.IsError, result.Flatten())
			}
		})
	}
}

func TestCommandTemplatePowerShellVariableDataPreserved(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	value := `quotes ' " $(Write-Output SW_PS_FLOW_OUTSIDE); {other}`
	for _, program := range []string{
		`$data='{value}'; Write-Output $data`,
		`$data='{value}'; $copy=$data; Write-Output $copy`,
		`$script:data='{value}'; Write-Output $data`,
		`$env:SW_TEMPLATE_DATA='{value}'; Write-Output $env:SW_TEMPLATE_DATA`,
		`$data='{value}'; $block=[scriptblock]::Create('param($data) Write-Output $data'); & $block $data`,
		`$data=@('{value}','FIXED'); Write-Output $data`,
		`$data='{value}'; Write-Output -InputObject:$data`,
	} {
		t.Run(program, func(t *testing.T) {
			spec, _ := json.Marshal(map[string]string{"command": program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": value, "other": "changed"}, nil)
			want := value
			if strings.Contains(program, "@(") {
				want += "\r\nFIXED"
			}
			if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != want {
				t.Fatalf("PowerShell variable data changed: output=%q want=%q error=%v", result.Flatten(), want, err)
			}
		})
	}
}

func TestCommandTemplatePowerShellVariableNativeArguments(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell executor")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	for _, value := range []string{`a'b"c\d $(Write-Output SW_PS_FLOW_OUTSIDE); {other}`, "", "line one\nline two", `café ñ trailing space\`} {
		for _, array := range []bool{false, true} {
			program := `$data='{value}'; `
			want := []string{value}
			if array {
				program = `$data=@('{value}','FIXED'); `
				want = append(want, "FIXED")
			}
			program += "& " + quotePowerShellLiteral(python) + ` -c 'import sys,json;print(json.dumps(sys.argv[1:]))' $data`
			spec, _ := json.Marshal(map[string]string{"command": program})
			result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": value, "other": "changed"}, nil)
			var got []string
			if err != nil || result.IsError || json.Unmarshal([]byte(strings.TrimSpace(result.Flatten())), &got) != nil || len(got) != len(want) {
				t.Fatalf("native variable arguments changed: output=%q want=%q error=%v", result.Flatten(), want, err)
			}
			for n := range want {
				if got[n] != want[n] {
					t.Fatalf("native variable argument %d changed: got=%q want=%q", n, got[n], want[n])
				}
			}
		}
	}
}
