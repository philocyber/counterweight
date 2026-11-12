package server

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestPowerShellCommandTemplateRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("requires PowerShell")
	}
	for _, template := range []string{`Write-Output {value}`, `Write-Output '{value}'`, `Write-Output "{value}"`} {
		for _, value := range []string{`normal`, `a'b"c;$HOME $(Write-Output INJECTED) ` + "`data", "line one\nline two", `café ñ ‘ ’ “ ” {second}`} {
			got, err := renderPowerShellCommandTemplate(template, map[string]any{"value": value, "second": "changed"})
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding; "+got)
			hideTemplateParserWindow(command)
			output, err := command.CombinedOutput()
			if err != nil || strings.TrimSuffix(strings.TrimSuffix(string(output), "\n"), "\r") != value {
				t.Fatalf("%s roundtrip failed: %q %v", template, output, err)
			}
		}
	}
	for _, template := range []string{`Write-Output ${value}`, `Write-Output "$({value})"`, "Write-Output @'\n{value}\n'@", "Write-Output `{value}"} {
		if _, err := renderPowerShellCommandTemplate(template, map[string]any{"value": "data"}); err == nil {
			t.Fatalf("non-data context accepted: %s", template)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := renderPowerShellCommandTemplateContext(ctx, `Write-Output "{value}"`, map[string]any{"value": "data"}); err == nil {
		t.Fatal("cancelled parser accepted")
	}
}
