package server

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestCommandTemplateRejectsParameterFedVariableNames(t *testing.T) {
	bash := parameterFlowBash(t)
	for _, program := range []string{
		`printf -v '{target}' '%s' fixed`,
		`printf '-v{target}' '%s' fixed`,
		`read -r '{target}' <<< fixed`,
		`array=(fixed); unset '{target}'`,
		`target={target}; printf -v "$target" '%s' fixed`,
		`target={target}; read -r "$target" <<< fixed`,
		`array=(fixed); target={target}; unset "$target"`,
		`run() { printf -v "$1" '%s' fixed; }; run {target}`,
		`command printf -v '{target}' '%s' fixed`,
		`command -p read -r '{target}' <<< fixed`,
		`array=(fixed); command -- unset '{target}'`,
		`run() { bash -c 'printf -v "$1" "%s" fixed' fixture "$1"; }; run {target}`,
		`run() { bash -c 'read -r "$1" <<< fixed' fixture "$1"; }; run {target}`,
		`run() { bash -c 'array=(fixed); unset "$1"' fixture "$1"; }; run {target}`,
	} {
		t.Run(program, func(t *testing.T) {
			rendered, err := renderBashCommandTemplate(program, map[string]any{"target": `array[$(printf SW_TARGET_OUTSIDE >&2)]`})
			if err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", rendered)
			hideTemplateParserWindow(cmd)
			output, runErr := cmd.CombinedOutput()
			t.Fatalf("parameter-fed variable name was accepted: output=%q execution_error=%v", output, runErr)
		})
	}
}

func TestCommandTemplateVariableNamesSDKBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix SDK uses Bash; Windows SDK uses PowerShell")
	}
	value := `quotes ' " $(printf SW_TARGET_OUTSIDE); {other}`
	for _, tc := range []struct {
		program string
		denied  bool
	}{
		{`printf -v '{target}' '%s' fixed`, true},
		{`read -r '{target}' <<< fixed`, true},
		{`array=(fixed); unset '{target}'`, true},
		{`printf -v 'array[0]' '%s' {value}; printf '%s' "${array[0]}"`, false},
		{`IFS= read -r data <<< '{value}'; printf '%s' "$data"`, false},
	} {
		spec, _ := json.Marshal(map[string]string{"command": tc.program})
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"target": `array[$(printf SW_TARGET_OUTSIDE >&2)]`, "value": value, "other": "changed"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.denied {
			if !result.IsError {
				t.Fatalf("SDK accepted parameter-fed variable target: %q", result.Flatten())
			}
		} else if result.IsError || result.Flatten() != value {
			t.Fatalf("SDK changed fixed-name data workflow: %q", result.Flatten())
		}
	}
}

func TestCommandTemplateFixedVariableNamesRemainData(t *testing.T) {
	bash := parameterFlowBash(t)
	value := `quotes ' " $(printf SW_TARGET_OUTSIDE); {other}`
	for _, program := range []string{
		`printf -v 'array[0]' '%s' {value}; printf '%s' "${array[0]}"`,
		`IFS= read -r data <<< '{value}'; printf '%s' "$data"`,
		`IFS= read -r 'array[0]' <<< '{value}'; printf '%s' "${array[0]}"`,
		`array=(fixed); unset 'array[0]'; printf '%s' {value}`,
		`printf -- '%s' {value}`,
		`command printf -v 'array[0]' '%s' {value}; printf '%s' "${array[0]}"`,
		`IFS= command -p read -r data <<< '{value}'; printf '%s' "$data"`,
		`IFS= read -r -p '{value}' data <<< '{value}'; printf '%s' "$data"`,
		`IFS= read -rp'{value}' data <<< '{value}'; printf '%s' "$data"`,
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
			if err != nil || string(output) != value {
				t.Fatalf("fixed-name data workflow changed: output=%q want=%q error=%v", output, value, err)
			}
		})
	}
}
