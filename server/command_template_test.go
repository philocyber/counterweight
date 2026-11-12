package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func TestCommandTemplateQuoteContextsRoundTrip(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if runtime.GOOS == "windows" {
		bash = `C:\Program Files\Git\bin\bash.exe`
		_, err = os.Stat(bash)
	}
	if err != nil {
		t.Skip("requires actual Bash")
	}
	for _, template := range []string{`printf '%s' {value}`, `printf '%s' '{value}'`, `printf '%s' "{value}"`, `printf '%s' $'{value}'`, `printf '%s' "$(printf '%s' {value})"`} {
		for _, value := range []string{`normal`, `a'b"c\d;$HOME $(printf INJECTED) ` + "`printf INJECTED`", "line one\nline two", `{second}`, `Unicode café ñ`} {
			got, err := renderBashCommandTemplate(template, map[string]any{"value": value, "second": "changed"})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bash, "--noprofile", "--norc", "-c", got)
			hideTemplateParserWindow(cmd)
			output, err := cmd.CombinedOutput()
			if err != nil || string(output) != value {
				t.Fatalf("template %q failed literal roundtrip: %q %v", template, output, err)
			}
		}
	}
	got, err := renderBashCommandTemplate(`printf '%s' "prefix{value}suffix"`, map[string]any{"value": []any{"a'b", "$(printf INJECTED)"}})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "--noprofile", "--norc", "-c", got)
	hideTemplateParserWindow(cmd)
	output, err := cmd.CombinedOutput()
	want, _ := json.Marshal([]any{"a'b", "$(printf INJECTED)"})
	if err != nil || string(output) != "prefix"+string(want)+"suffix" {
		t.Fatalf("JSON/prefix preservation failed: %q %v", output, err)
	}
}

func TestCommandTemplateUsesActualSDKExecutor(t *testing.T) {
	value := `a'b"$HOME $(printf INJECTED);{second}`
	for _, quote := range []string{"'", `"`} {
		program := "printf '%s' " + quote + "{value}" + quote
		if runtime.GOOS == "windows" {
			program = "Write-Output " + quote + "{value}" + quote
		}
		spec, _ := json.Marshal(map[string]string{"command": program})
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value, "second": "changed"}, nil)
		if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != value {
			t.Fatalf("native SDK literal execution failed: %q %v", result.Flatten(), err)
		}
	}
}

func TestCommandTemplateNativeExternalArguments(t *testing.T) {
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	application := shellQuote(python)
	if runtime.GOOS == "windows" {
		application = "& " + quotePowerShellLiteral(python)
	}
	for _, quote := range []string{"", "'", `"`} {
		for n, value := range []string{`a'b"c $HOME; $(printf INJECTED)`, "", "a b", `path space\`, `a\"b c`, `a""b`, "line one\nline two", `café ñ ‘ ’ “ ” {second}`} {
			t.Run(quote+"/"+fmt.Sprint(n), func(t *testing.T) {
				program := application + ` -c 'import sys,json;print(json.dumps(sys.argv[1:]))' ` + quote + "{value}" + quote + " " + quote + "pre{value}{second}post" + quote
				spec, _ := json.Marshal(map[string]string{"command": program})
				result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value, "second": "tail"}, nil)
				var args []string
				if err != nil || result.IsError || json.Unmarshal([]byte(strings.TrimSpace(result.Flatten())), &args) != nil || len(args) != 2 || args[0] != value || args[1] != "pre"+value+"tailpost" {
					t.Fatalf("native external argument changed: %q %v", result.Flatten(), err)
				}
			})
		}
	}
}

func TestCommandTemplateNativeTaskEnvironment(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native binding")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	dir := t.TempDir()
	relative, err := filepath.Rel(dir, python)
	if err != nil {
		t.Fatal(err)
	}
	value := `a\"b c`
	for _, application := range []string{filepath.Base(python), "& " + quotePowerShellLiteral(python), "& " + quotePowerShellLiteral(relative)} {
		program := application + ` -c 'import sys,json,os;print(json.dumps([sys.argv[1],os.environ.get(chr(83)+chr(87)+chr(84)),os.getcwd()]))' "{value}" | Set-Content result.json; Get-Content result.json`
		spec, _ := json.Marshal(map[string]string{"command": program})
		tc := &actool.ToolContext{WorkingDir: dir, Env: []string{"PATH=" + filepath.Dir(python) + ";" + os.Getenv("PATH"), "SWT=fixture"}}
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value}, tc)
		var received []string
		if err != nil || result.IsError || json.Unmarshal([]byte(strings.TrimSpace(result.Flatten())), &received) != nil || len(received) != 3 || received[0] != value || received[1] != "fixture" || !strings.EqualFold(received[2], dir) {
			t.Fatalf("task environment/pipeline changed: %q %v", result.Flatten(), err)
		}
	}
	if _, err := renderPowerShellCommandTemplate("& "+quotePowerShellLiteral(python)+" @'\n{value}\n'@", map[string]any{"value": value}); err == nil {
		t.Fatal("native here-string accepted")
	}
}

func TestCommandTemplateNativeRuntimeExpansion(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native binding")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	prefix := `pre\" quote `
	suffix := ` suffix\`
	for _, expression := range []string{`${env:SWT}`, `${prefix}`, `$(++$global:SWExpansionCount; Write-Output $prefix)`} {
		for n, value := range []string{`a'b"c $HOME; $(printf INJECTED)`, "", "a b", `path space\`, `a\"b c`, `a""b`, "line one\nline two", `café ñ ‘ ’ “ ” {second}`} {
			t.Run(expression+"/"+fmt.Sprint(n), func(t *testing.T) {
				program := `$prefix=$env:SWT; $global:SWExpansionCount=0; & ` + quotePowerShellLiteral(python) + ` -c 'import sys,json;print(json.dumps(sys.argv[1:]))' "` + expression + `{value}${env:SWSuffix}"; Write-Output $global:SWExpansionCount`
				spec, _ := json.Marshal(map[string]string{"command": program})
				result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value, "second": "changed"}, &actool.ToolContext{Env: []string{"SWT=" + prefix, "SWSuffix=" + suffix}})
				lines := strings.Split(strings.TrimSpace(result.Flatten()), "\n")
				var args []string
				count := "0"
				if strings.HasPrefix(expression, "$(") {
					count = "1"
				}
				if err != nil || result.IsError || len(lines) != 2 || json.Unmarshal([]byte(strings.TrimSpace(lines[0])), &args) != nil || len(args) != 1 || args[0] != prefix+value+suffix || strings.TrimSpace(lines[1]) != count {
					t.Fatalf("runtime native argument or expression count changed: %q %v", result.Flatten(), err)
				}
			})
		}
	}
}

func TestCommandTemplateNativeAliasBecomesManaged(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native binding")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	value := `a'b"c $HOME; $(printf INJECTED)`
	for _, argument := range []string{`"{value}"`, `"${env:SWT}{value}"`} {
		program := "Set-Alias -Name " + quotePowerShellLiteral(filepath.Base(python)) + " -Value Write-Output; " + filepath.Base(python) + " " + argument
		spec, _ := json.Marshal(map[string]string{"command": program})
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value}, &actool.ToolContext{Env: []string{"PATH=" + filepath.Dir(python) + ";" + os.Getenv("PATH"), "SWT=prefix"}})
		want := value
		if strings.Contains(argument, "SWT") {
			want = "prefix" + value
		}
		if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != want {
			t.Fatalf("template alias no longer preserves managed argument: %q %v", result.Flatten(), err)
		}
	}
}

func TestCommandTemplateNativeAliasBecomesApplication(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows native binding")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	for _, name := range []string{"Write-Output", "sw-native-fixture"} {
		for _, argument := range []string{`{value}`, `'{value}'`, `"${env:SWT}{value}"`} {
			for _, value := range []string{`a\"b c`, "", `café ñ $HOME; $(Write-Output INJECTED)`} {
				program := "Set-Alias -Force -Name " + quotePowerShellLiteral(name) + " -Value " + quotePowerShellLiteral(python) + "; " + name + ` -c 'import sys,json;print(json.dumps(sys.argv[1:]))' ` + argument
				spec, _ := json.Marshal(map[string]string{"command": program})
				result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value}, &actool.ToolContext{Env: []string{"SWT=prefix"}})
				want := value
				if strings.Contains(argument, "SWT") {
					want = "prefix" + value
				}
				var args []string
				if err != nil || result.IsError || json.Unmarshal([]byte(strings.TrimSpace(result.Flatten())), &args) != nil || len(args) != 1 || args[0] != want {
					t.Fatalf("managed/unknown alias became native and changed argv: %s %s %q %v", name, argument, result.Flatten(), err)
				}
			}
		}
	}
}

func TestCommandTemplateDeclaredManagedFunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell function binding")
	}
	for _, argument := range []string{`'{value}'`, `"${env:SWT}{value}"`} {
		value := `a\"b c $HOME; $(Write-Output INJECTED)`
		program := `function sw-managed-fixture {param([string]$v) Write-Output $v}; sw-managed-fixture ` + argument
		spec, _ := json.Marshal(map[string]string{"command": program})
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value}, &actool.ToolContext{Env: []string{"SWT=prefix"}})
		want := value
		if strings.Contains(argument, "SWT") {
			want = "prefix" + value
		}
		if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != want {
			t.Fatalf("declared managed function argument changed: %q %v", result.Flatten(), err)
		}
	}
}

func TestCommandTemplateNativeNestedParameters(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell native binding")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	for en, expression := range []string{`$('pre{prefix}')`, `$(Write-Output 'pre{prefix}')`, `$(++$global:SWNestedCount; Write-Output 'pre{prefix}')`, `$(& ` + quotePowerShellLiteral(python) + ` -c 'import sys;print(sys.argv[1])' 'pre{prefix}')`} {
		for tn, trailing := range []string{"{value}", ""} {
			for vn, value := range []string{`a\"b c`, "", `café ñ ' $HOME; $(Write-Output INJECTED)`} {
				t.Run(fmt.Sprintf("%d/%d/%d", en, tn, vn), func(t *testing.T) {
					prefix := `x'"\$HOME $(Write-Output INJECTED)`
					program := `$global:SWNestedCount=0; $unused='café 😀'; & ` + quotePowerShellLiteral(python) + ` -c 'import sys,json;print(json.dumps(sys.argv[1:]))' "` + expression + trailing + `"; Write-Output $global:SWNestedCount`
					spec, _ := json.Marshal(map[string]string{"command": program})
					result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value, "prefix": prefix}, nil)
					lines := strings.Split(strings.TrimSpace(result.Flatten()), "\n")
					var args []string
					count := "0"
					if strings.Contains(expression, "++") {
						count = "1"
					}
					want := "pre" + prefix
					if trailing != "" {
						want += value
					}
					if err != nil || result.IsError || len(lines) != 2 || json.Unmarshal([]byte(strings.TrimSpace(lines[0])), &args) != nil || len(args) != 1 || args[0] != want || strings.TrimSpace(lines[1]) != count {
						t.Fatalf("nested parameter or evaluation count changed: %s %q %v", expression, result.Flatten(), err)
					}
				})
			}
		}
	}
}

func TestCommandTemplateParameterizedExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell command lookup")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	for _, head := range []string{`& {exe}`, `& '{exe}'`} {
		for _, argument := range []string{`'{value}'`, `"${env:SWT}{value}"`} {
			for _, value := range []string{`a\"b c`, "", `café ñ $HOME; $(Write-Output INJECTED) {exe}`} {
				program := head + ` -c 'import sys,json;print(json.dumps(sys.argv[1:]))' ` + argument
				spec, _ := json.Marshal(map[string]string{"command": program})
				result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"exe": python, "value": value}, &actool.ToolContext{Env: []string{"SWT=prefix"}})
				want := value
				if strings.Contains(argument, "SWT") {
					want = "prefix" + value
				}
				var args []string
				if err != nil || result.IsError || json.Unmarshal([]byte(strings.TrimSpace(result.Flatten())), &args) != nil || len(args) != 1 || args[0] != want {
					t.Fatalf("parameterized executable changed argument: %s %s %q %v", head, argument, result.Flatten(), err)
				}
			}
		}
		value := `a\"b c {exe}`
		spec, _ := json.Marshal(map[string]string{"command": head + ` '{value}'`})
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"exe": "Write-Output", "value": value}, nil)
		if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != value {
			t.Fatalf("parameterized managed command changed value: %q %v", result.Flatten(), err)
		}
	}
	if _, err := renderPowerShellCommandTemplate(`{exe} '{value}'`, map[string]any{"exe": python, "value": "fixture"}); err == nil {
		t.Fatal("command-code placeholder without call operator accepted")
	}
}

func TestCommandTemplateDynamicExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell command expressions")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	// Keep the operator variable distinct from the placeholder key: ${exe}
	// deliberately rejects substitution when exe is supplied as parameter data.
	for hn, head := range []string{`$application`, `$holder.executable`, `$(++$global:SWExecutableCount; Write-Output $application)`, `"${application}"`, `$info`, `('{exe}')`, `"$(++$global:SWExecutableCount | Out-Null){exe}"`} {
		for an, argument := range []string{`'{value}'`, `"$($application='Write-Output'; ++$global:SWArgumentCount | Out-Null){value}"`} {
			for vn, value := range []string{`a\"b c`, "", `café ñ $HOME; $(Write-Output INJECTED)`} {
				t.Run(fmt.Sprintf("%d/%d/%d", hn, an, vn), func(t *testing.T) {
					program := `$global:SWExecutableCount=0; $global:SWArgumentCount=0; $application=` + quotePowerShellLiteral(python) + `; $holder=@{executable=$application}; $info=Get-Command -Name $application; & ` + head + ` -c 'import sys,json;print(json.dumps(sys.argv[1:]))' ` + argument + `; Write-Output $global:SWExecutableCount; Write-Output $global:SWArgumentCount`
					spec, _ := json.Marshal(map[string]string{"command": program})
					result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value, "exe": python}, nil)
					lines := strings.Split(strings.TrimSpace(result.Flatten()), "\n")
					var args []string
					commandCount, argumentCount := "0", "0"
					if strings.Contains(head, "++") {
						commandCount = "1"
					}
					if an == 1 {
						argumentCount = "1"
					}
					if err != nil || result.IsError || len(lines) != 3 || json.Unmarshal([]byte(strings.TrimSpace(lines[0])), &args) != nil || len(args) != 1 || args[0] != value || strings.TrimSpace(lines[1]) != commandCount || strings.TrimSpace(lines[2]) != argumentCount {
						t.Fatalf("dynamic command/argument/count changed: %q %v", result.Flatten(), err)
					}
				})
			}
		}
	}
	for _, operator := range []string{"&", "."} {
		value := `a\"b c`
		program := `$SWLocal=0; ` + operator + ` {param([string]$v) $SWLocal++; Write-Output $v} '{value}'; Write-Output $SWLocal`
		spec, _ := json.Marshal(map[string]string{"command": program})
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value}, nil)
		want := value + "\r\n0"
		if operator == "." {
			want = value + "\r\n1"
		}
		if err != nil || result.IsError || strings.TrimSpace(result.Flatten()) != want {
			t.Fatalf("managed scriptblock/caller scope changed: %q %v", result.Flatten(), err)
		}
	}
	if _, err := renderPowerShellCommandTemplate(`& "${exe}" '{value}'`, map[string]any{"exe": python, "value": "fixture"}); err == nil {
		t.Fatal("placeholder in variable expansion accepted")
	}
}

func TestCommandTemplateDynamicAliasTargetPinned(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell alias resolution")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	for _, head := range []string{`$target`, `$command`} {
		value := `a\"b c`
		program := `Set-Alias -Name sw-dynamic-alias -Value ` + quotePowerShellLiteral(python) + `; $target='sw-dynamic-alias'; $command=Get-Command -Name $target; & ` + head + ` -c 'import sys,json;print(json.dumps(sys.argv[1:]))' "$(Set-Alias -Name sw-dynamic-alias -Value Write-Output){value}"`
		spec, _ := json.Marshal(map[string]string{"command": program})
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"value": value}, nil)
		var args []string
		if err != nil || result.IsError || json.Unmarshal([]byte(strings.TrimSpace(result.Flatten())), &args) != nil || len(args) != 1 || args[0] != value {
			t.Fatalf("selected alias target changed during argument evaluation: %s %q %v", head, result.Flatten(), err)
		}
	}
}

func TestCommandTemplateStaticAliasTargetPinned(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell native alias binding")
	}
	python := os.Getenv("COUNTERWEIGHT_TEST_PYTHON")
	if python == "" {
		t.Skip("requires explicitly selected Python")
	}
	for _, head := range []string{`sw-static-alias`, `& 'sw-static-alias'`} {
		value := `a\"b c`
		program := `Set-Alias -Name sw-static-alias -Value ` + quotePowerShellLiteral(python) + `; ` + head + ` -c 'import sys,json;print(json.dumps(sys.argv[1:]))' "$(Set-Alias -Name sw-static-alias -Value Write-Output){first}" '{second}'; sw-static-alias 'after{second}'`
		spec, _ := json.Marshal(map[string]string{"command": program})
		result, err := (&Server{}).runCommandTool(context.Background(), spec, map[string]any{"first": "", "second": value}, nil)
		var args []string
		lines := strings.Split(strings.TrimSpace(result.Flatten()), "\n")
		if err != nil || result.IsError || len(lines) != 2 || json.Unmarshal([]byte(strings.TrimSpace(lines[0])), &args) != nil || len(args) != 2 || args[0] != "" || args[1] != value || strings.TrimSpace(lines[1]) != "after"+value {
			t.Fatalf("static alias later argument changed: %s %q %v", head, result.Flatten(), err)
		}
	}
}

func TestCommandTemplateRejectsNonDataContexts(t *testing.T) {
	for _, program := range []string{`printf '%s' ${value}`, `printf '%s' "${OTHER:-{value}}"`, `echo $(({value}))`, "echo `printf '%s' {value}`", "cat <<EOF\n{value}\nEOF", `printf '%s' \{value}`, `printf '%s' "\{value}"`} {
		if got, err := renderBashCommandTemplate(program, map[string]any{"value": "data"}); err == nil {
			t.Fatalf("unsupported context accepted: %q => %q", program, got)
		}
	}
	if _, err := renderBashCommandTemplate(`echo {value}`, map[string]any{"value": "a\x00b"}); err == nil {
		t.Fatal("NUL accepted")
	}
	got, err := renderBashCommandTemplate(`printf '%s' '{unknown}'`, map[string]any{"value": "unused"})
	if err != nil || !strings.Contains(got, "{unknown}") {
		t.Fatal("unmapped literal changed")
	}
}
