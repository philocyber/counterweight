package server

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/tool"
)

func TestCustomExecutorsMaskServerCredentials(t *testing.T) {
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "COUNTERWEIGHT_PG_DSN", "COUNTERWEIGHT_LLM_PROXY"} {
		t.Setenv(key, "SW_CUSTOM_ENV_FIXTURE")
	}
	for _, mode := range []string{"python", "command"} {
		t.Run(mode, func(t *testing.T) {
			code := `import os,json;print(json.dumps({k:os.getenv(k,'') for k in ['OPENAI_API_KEY','ANTHROPIC_API_KEY','COUNTERWEIGHT_PG_DSN','COUNTERWEIGHT_LLM_PROXY','SW_BENIGN','TOOL_DATA']}))`
			var output string
			var err error
			extra := []string{"OPENAI_API_KEY=SW_CUSTOM_ENV_OVERRIDE", "SW_BENIGN=SAFE_KEEP"}
			if mode == "python" {
				output, err = execPython(t.Context(), python, "env-fixture", code, map[string]any{"data": "DATA_KEEP"}, t.TempDir(), extra, 5*time.Second)
			} else {
				if runtime.GOOS != "windows" {
					t.Skip("Windows native argument fixture")
				}
				spec, _ := json.Marshal(map[string]any{"command": `@{OPENAI_API_KEY=$env:OPENAI_API_KEY;ANTHROPIC_API_KEY=$env:ANTHROPIC_API_KEY;COUNTERWEIGHT_PG_DSN=$env:COUNTERWEIGHT_PG_DSN;COUNTERWEIGHT_LLM_PROXY=$env:COUNTERWEIGHT_LLM_PROXY;SW_BENIGN=$env:SW_BENIGN} | ConvertTo-Json -Compress`})
				result, callErr := (&Server{}).runCommandTool(t.Context(), spec, nil, &tool.ToolContext{WorkingDir: t.TempDir(), Env: extra})
				err = callErr
				output = result.Flatten()
				if result.IsError {
					t.Fatalf("native command failed: %q", output)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			var values map[string]string
			if json.Unmarshal([]byte(strings.TrimSpace(output)), &values) != nil {
				t.Fatalf("native fixture output: %q", output)
			}
			for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "COUNTERWEIGHT_PG_DSN", "COUNTERWEIGHT_LLM_PROXY"} {
				if values[key] != "" {
					t.Errorf("%s leaked controlled server credential: %q", key, values[key])
				}
				if os.Getenv(key) != "SW_CUSTOM_ENV_FIXTURE" {
					t.Errorf("parent environment changed: %s", key)
				}
			}
			if values["SW_BENIGN"] != "SAFE_KEEP" || (mode == "python" && values["TOOL_DATA"] != "DATA_KEEP") {
				t.Fatalf("task/parameter environment changed: %v", values)
			}
		})
	}
}
