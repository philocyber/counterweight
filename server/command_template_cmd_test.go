package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/locale"

	actool "github.com/Autumn-27/norma/tool"
)

func TestCommandTemplateBatchArgumentCannotLaunchAnotherCommand(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows batch command parsing")
	}
	file := filepath.Join(t.TempDir(), "fixed fixture.cmd")
	// The batch file never reads its arguments. Any second marker must arise
	// from parsing the process command line, not from vulnerable batch content.
	if err := os.WriteFile(file, []byte("@echo off\r\necho BATCH_CALLED\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	spec, _ := json.Marshal(map[string]string{"command": "& " + quotePowerShellLiteral(file) + ` '{value}'`})
	for _, value := range []string{"fixture", `fixture" & echo SW_OUTSIDE & rem "`, `fixture\" & echo SW_OUTSIDE & rem "`, `%SW_BATCH_BREAK% & echo SW_OUTSIDE & rem "`, `%CMDCMDLINE:~-1% & echo SW_OUTSIDE & rem "`} {
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value}, &actool.ToolContext{Env: []string{`SW_BATCH_BREAK="`}})
		if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != "BATCH_CALLED" {
			t.Fatalf("batch argument escaped or compatibility failed: %q => %q %v", value, result.Flatten(), err)
		}
	}
}

func TestCommandTemplateBatchPercentDataRoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows batch command parsing")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	file := filepath.Join(t.TempDir(), "data fixture.cmd")
	// These fixtures contain no quotes in their data. The quoted SET statement
	// passes the batch parameter to Python through an environment data channel.
	body := "@echo off\r\nsetlocal DisableDelayedExpansion\r\nset \"SW_BATCH_DATA=%~1\"\r\n\"" + python + "\" -c \"import os,json;print(json.dumps(os.environ.get('SW_BATCH_DATA')))\"\r\n"
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	spec, _ := json.Marshal(map[string]string{"command": "& " + quotePowerShellLiteral(file) + ` '{value}'`})
	for _, value := range []string{`a%20b%22c`, `%SW_BATCH_BREAK%`, `%CMDCMDLINE:~-1%`, `100% complete`, `path space\`} {
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value}, &actool.ToolContext{Env: []string{`SW_BATCH_BREAK="`}})
		var received string
		if err != nil || result.IsError || json.Unmarshal([]byte(strings.TrimSpace(result.Flatten())), &received) != nil || received != value {
			t.Fatalf("batch literal data changed: %q => %q %v", value, result.Flatten(), err)
		}
	}
}

func TestCommandTemplateBatchLineBreakCannotLaunchAnotherCommand(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows batch command parsing")
	}
	file := filepath.Join(t.TempDir(), "line fixture.cmd")
	if err := os.WriteFile(file, []byte("@echo off\r\necho BATCH_CALLED\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	spec, _ := json.Marshal(map[string]string{"command": "& " + quotePowerShellLiteral(file) + ` '{value}'`})
	for _, value := range []string{"fixture\necho SW_OUTSIDE", "fixture\"\r\necho SW_OUTSIDE\r\nrem \""} {
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value}, &actool.ToolContext{})
		if strings.Contains(result.Flatten(), "SW_OUTSIDE") {
			t.Fatalf("batch line break activated parameter syntax: %q => %q", value, result.Flatten())
		}
		if err == nil && !result.IsError && strings.TrimSpace(result.Flatten()) != "BATCH_CALLED" {
			t.Fatalf("batch argument changed successful execution: %q => %q", value, result.Flatten())
		}
	}
}

func TestCommandTemplateCMDProgramInterpolationCannotExecuteParameter(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows CMD program parsing")
	}
	cmd := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	programs := []string{
		`& ` + quotePowerShellLiteral(cmd) + ` /d /s /c 'echo {value}'`,
		`& $env:ComSpec /d /s /c 'echo {value}'`,
		`& (Get-Command cmd.exe) /d /s /c 'echo {value}'`,
		`Set-Alias -Name sw-cmd -Value $env:ComSpec; sw-cmd /d /s /c 'echo {value}'`,
		`Set-Alias -Name sw-cmd -Value $env:ComSpec; sw-cmd /d /s /c "$(Set-Alias -Name sw-cmd -Value Write-Output)echo {value}"`,
		`& $env:ComSpec /d /s /c 'echo FIXED_ECHO' '{value}'`,
	}
	for _, program := range programs {
		spec, _ := json.Marshal(map[string]string{"command": program})
		result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": `FIXED_ECHO & echo SW_OUTSIDE`}, &actool.ToolContext{})
		if strings.Contains(result.Flatten(), "SW_OUTSIDE") {
			t.Fatalf("parameter became CMD program syntax: %s => %q %v", program, result.Flatten(), err)
		}
		if err == nil && !result.IsError {
			t.Fatalf("unsupported CMD program interpolation succeeded: %q", result.Flatten())
		}
	}
	program := `Write-Output '{value}'; & $env:ComSpec /d /s /c 'echo STATIC_PROGRAM'`
	spec, _ := json.Marshal(map[string]string{"command": program})
	result, err := (&Server{}).runCommandTool(t.Context(), spec, map[string]any{"value": "literal data"}, &actool.ToolContext{})
	if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != "literal data\r\nSTATIC_PROGRAM" {
		t.Fatalf("fixed CMD program or managed data was broken: %q %v", result.Flatten(), err)
	}
	for _, lang := range []locale.Lang{locale.En} {
		spec, _ := json.Marshal(map[string]string{"command": `& $env:ComSpec /d /s /c 'echo {value}'`})
		result, err := (&Server{}).runCommandTool(locale.WithLang(t.Context(), lang), spec, map[string]any{"value": "plain data"}, &actool.ToolContext{})
		want := locale.Text(lang, "Command parameters cannot be interpolated into CMD programs; use literal native arguments or a script data channel")
		if err != nil || !result.IsError || !strings.Contains(result.Flatten(), want) {
			t.Fatalf("localized CMD refusal missing: %s %q %v", lang, result.Flatten(), err)
		}
	}
}
