package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
)

func renderCommandTemplate(program string, params map[string]any) (string, error) {
	return renderCommandTemplateContext(context.Background(), program, params)
}

type templateExecutionEnvironment struct {
	dir string
	env []string
}

func renderCommandTemplateContext(ctx context.Context, program string, params map[string]any, environment ...templateExecutionEnvironment) (string, error) {
	if runtime.GOOS != "windows" {
		return renderBashCommandTemplate(program, params)
	}
	return renderPowerShellCommandTemplateContext(ctx, program, params, environment...)
}

// Parse only an inert marker template. Parameters never enter the parser's
// command line or executable code; actual execution still uses the reviewed SDK.
func renderPowerShellCommandTemplate(program string, params map[string]any) (string, error) {
	return renderPowerShellCommandTemplateContext(context.Background(), program, params)
}

func renderPowerShellCommandTemplateContext(parent context.Context, program string, params map[string]any, environment ...templateExecutionEnvironment) (string, error) {
	type substitution struct {
		start, end    int
		marker, value string
	}
	var substitutions []substitution
	var template strings.Builder
	offset := 0
	nonce := rand.Text()
	for _, match := range commandPlaceholder.FindAllStringSubmatchIndex(program, -1) {
		value, ok := params[program[match[2]:match[3]]]
		if !ok {
			continue
		}
		text := valToStr(value)
		if strings.ContainsRune(text, 0) {
			return "", errors.New("command parameter contains a NUL byte")
		}
		marker := fmt.Sprintf("SWPARAM%sX%d", nonce, len(substitutions))
		substitutions = append(substitutions, substitution{match[0], match[1], marker, text})
		template.WriteString(program[offset:match[0]])
		template.WriteString(marker)
		offset = match[1]
	}
	if len(substitutions) == 0 {
		return program, nil
	}
	template.WriteString(program[offset:])
	markers := make([]string, len(substitutions))
	for n, item := range substitutions {
		markers[n] = item.marker
	}
	input, _ := json.Marshal(map[string]any{"program": template.String(), "markers": markers, "bindingPrefix": "SWCOMMAND" + nonce})
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", powerShellTemplateParser)
	// Resolve native applications in the same directory and environment as Bash.
	if len(environment) != 0 {
		cmd.Dir = environment[0].dir
		if len(environment[0].env) != 0 {
			cmd.Env = append(os.Environ(), environment[0].env...)
		}
	}
	hideTemplateParserWindow(cmd)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		return "", errors.New("PowerShell command template parsing failed")
	}
	var parsed struct {
		Modes                 map[string]string
		ScriptBlockParameters bool
		NativeArguments       []struct {
			Text, Value, Reference                    string
			Expanded, Head, Literal, Invoke, Pipeline bool
			PythonSource, OperatorArgument            bool
			HostSource, ShellSource, AnySource        bool
			Launcher, EnvironmentProgram              bool
			PipelineArguments                         []string
			Start, End                                int
		}
	}
	if json.Unmarshal(output, &parsed) != nil {
		return "", errors.New("PowerShell command template parsing failed")
	}
	if parsed.ScriptBlockParameters {
		return "", locale.NewError("Command parameters cannot be used to construct PowerShell script blocks; use fixed programs and separate data arguments")
	}
	pairs := make([]string, 0)
	valuePairs := make([]string, 0, len(substitutions)*2)
	for _, item := range substitutions {
		valuePairs = append(valuePairs, item.marker, item.value)
	}
	values := strings.NewReplacer(valuePairs...)
	for _, item := range substitutions {
		mode := parsed.Modes[item.marker]
		if mode == "" {
			return "", errors.New("command placeholder must be literal PowerShell argument data")
		}
		slashes := 0
		for n := item.start - 1; n >= 0 && program[n] == '`'; n-- {
			slashes++
		}
		if slashes%2 != 0 {
			return "", errors.New("command placeholder cannot follow an unmatched PowerShell escape")
		}
		text := item.value
		switch mode {
		case "NativeLiteral":
			text = quotePowerShellLiteral(text)
		case "SingleQuoted", "BareWord":
			text = powerShellSingleQuotes.Replace(text)
			if mode == "BareWord" {
				text = "'" + text + "'"
			}
		case "DoubleQuoted", "NativeExpanded":
			text = escapePowerShellDoubleQuoted(text)
		default:
			return "", errors.New("PowerShell here-string placeholder is unsupported")
		}
		pairs = append(pairs, item.marker, text)
	}
	// AST offsets are UTF-8 byte offsets, including when operator text contains
	// non-ASCII characters. Render outer arguments around their nested children
	// so each placeholder retains its own quoting context and each completed
	// native string is encoded once. Inserted values are never reparsed/replaced.
	source := template.String()
	for _, argument := range parsed.NativeArguments {
		if argument.Reference == "" || argument.Start < 0 || argument.End <= argument.Start || argument.End > len(source) || source[argument.Start:argument.End] != argument.Text {
			return "", errors.New("PowerShell command template parsing failed")
		}
	}
	sort.Slice(parsed.NativeArguments, func(i, j int) bool {
		a, b := parsed.NativeArguments[i], parsed.NativeArguments[j]
		if a.Start == b.Start {
			return a.End > b.End
		}
		return a.Start < b.Start
	})
	contextValues := strings.NewReplacer(pairs...)
	var renderRegion func(int, int, int) string
	renderRegion = func(start, end, excluded int) string {
		var rendered strings.Builder
		cursor := start
		for index, argument := range parsed.NativeArguments {
			if index == excluded || argument.Start < cursor || argument.End > end {
				continue
			}
			rendered.WriteString(contextValues.Replace(source[cursor:argument.Start]))
			encoded := base64.StdEncoding.EncodeToString([]byte(values.Replace(argument.Value)))
			expression := "([System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String('" + encoded + "')))"
			if argument.OperatorArgument && !argument.Expanded {
				// Keep fixed literals visible in reviewed text. Other operator
				// expressions retain evaluation in the original caller scope.
				expression = quotePowerShellLiteral(argument.Value)
			}
			if argument.Head {
				// Capture the command expression in caller scope once, before its
				// arguments can change variables or command-resolution state.
				if argument.Literal {
					// Keep the selected name visible in reviewed command text.
					expression = quotePowerShellLiteral(values.Replace(argument.Value))
				} else {
					expression = renderRegion(argument.Start, argument.End, index)
				}
				if argument.Invoke {
					rendered.WriteString("& ")
				}
				resolved := powerShellCommandResolver + " (" + expression + ")"
				if argument.PythonSource {
					denied := quotePowerShellLiteral(locale.Text(locale.FromContext(parent), "Command parameters cannot be interpolated into nested shell or Python programs or their execution options; pass separate data arguments"))
					resolved = powerShellPythonSourceGuard + " (" + resolved + ") " + denied
				}
				if argument.HostSource || argument.ShellSource || argument.AnySource {
					denied := quotePowerShellLiteral(locale.Text(locale.FromContext(parent), "Command parameters cannot be interpolated into nested shell or Python programs or their execution options; pass separate data arguments"))
					resolved = powerShellNestedShellGuard + " (" + resolved + ") " + denied + " " + powerShellBool(argument.HostSource) + " " + powerShellBool(argument.ShellSource) + " " + powerShellBool(argument.AnySource)
				}
				if argument.EnvironmentProgram {
					denied := quotePowerShellLiteral(locale.Text(locale.FromContext(parent), "Command parameters cannot be interpolated into CMD programs; use literal native arguments or a script data channel"))
					resolved = powerShellEnvironmentProgramGuard + " (" + resolved + ") " + denied
				}
				if argument.Pipeline {
					denied := quotePowerShellLiteral(locale.Text(locale.FromContext(parent), "Command parameters cannot be passed to expression-evaluating PowerShell commands; use fixed programs and separate data arguments"))
					pythonDenied := quotePowerShellLiteral(locale.Text(locale.FromContext(parent), "Command parameters cannot be used as interpreter stdin programs; use fixed programs that read separate data"))
					words := argument.PipelineArguments
					resolved = powerShellPipelineCommandGuard + " (" + resolved + ") " + denied + " " + powerShellBool(intercept.PythonTemplateReadsStdin(words)) + " " + pythonDenied +
						" " + powerShellBool(powerShellHostReadsStdin(words)) + " " + powerShellBool(intercept.ShellTemplateWordsInput(append([]string{"sh"}, words...)) != "") +
						" " + powerShellBool(powerShellCommandProcessorReadsStdin(words)) + " " + powerShellBool(wslRunsDefaultShell(words) || intercept.ShellTemplateWordsInput(append([]string{"wsl"}, words...)) != "")
				}
				rendered.WriteString("(" + argument.Reference + "=" + resolved + ")")
				cursor = argument.End
				continue
			}
			// Decode literal bytes as data rather than embedding quote characters
			// into another expandable string's subexpression source.
			if argument.Expanded {
				expression = renderRegion(argument.Start, argument.End, index)
			}
			denied := quotePowerShellLiteral(locale.Text(locale.FromContext(parent), "Command parameters cannot be interpolated into CMD programs; use literal native arguments or a script data channel"))
			evaluationDenied := quotePowerShellLiteral(locale.Text(locale.FromContext(parent), "Command parameters cannot be passed to expression-evaluating PowerShell commands; use fixed programs and separate data arguments"))
			rendered.WriteString("(" + powerShellNativeArgumentEncoder + " " + argument.Reference + " " + expression + " " + denied + " " + evaluationDenied + " " + powerShellBool(argument.OperatorArgument) + " " + powerShellBool(argument.Launcher) + ")")
			cursor = argument.End
		}
		rendered.WriteString(contextValues.Replace(source[cursor:end]))
		return rendered.String()
	}
	return renderRegion(0, len(source), -1), nil
}

// PowerShell ends a single-quoted string at any of five characters and a
// double-quoted one at any of four; each must be neutralised in a value.
var powerShellSingleQuotes = strings.NewReplacer("'", "''", "‘", "‘‘", "’", "’’", "‚", "‚‚", "‛", "‛‛")

func quotePowerShellLiteral(value string) string {
	return "'" + powerShellSingleQuotes.Replace(value) + "'"
}

func escapePowerShellDoubleQuoted(value string) string {
	return strings.NewReplacer("`", "``", "$", "`$", `"`, "`\"", "“", "`“", "”", "`”", "„", "`„").Replace(value)
}

func powerShellBool(value bool) string {
	if value {
		return "$true"
	}
	return "$false"
}

var (
	powerShellHostCommandOption = regexp.MustCompile(`(?i)^-(?:c|co|com|comm|comma|comman|command|f|fi|fil|file)$`)
	powerShellHostEncodedOption = regexp.MustCompile(`(?i)^-e(?:c|n|nc|nco\w*)?$`)
	// Host options whose next word is their value (-ExecutionPolicy Bypass,
	// -WindowStyle Hidden, ...), not the script or command.
	powerShellHostValueOption = regexp.MustCompile(`(?i)^-(?:ex\w*|ep|ea|encodeda\w*|w\w*|inp\w*|if|o\w*|config\w*|cus\w*|settings\w*|psc\w*|v\w*)$`)
)

// powerShellHostReadsStdin reports whether a nested PowerShell host given
// these words takes its program from the pipeline. Words are original command
// line text; <dynamic> stands for one that is not fixed.
func powerShellHostReadsStdin(words []string) bool {
	for i := 0; i < len(words); i++ {
		word := words[i]
		switch {
		case word == "<dynamic>":
			return true
		case powerShellHostCommandOption.MatchString(word):
			return i+1 >= len(words) || words[i+1] == "-" || words[i+1] == "<dynamic>"
		case powerShellHostValueOption.MatchString(word):
			// The value is not a program; a missing or dynamic one decides nothing.
			i++
			if i < len(words) && words[i] == "<dynamic>" {
				return true
			}
		case powerShellHostEncodedOption.MatchString(word):
			return false
		case !strings.HasPrefix(word, "-"):
			return false
		}
	}
	return true
}

// wslRunsDefaultShell reports whether wsl given these words starts the
// distribution's shell with no command of its own, so that shell takes its
// program from the pipeline. An option this does not know is not assumed to
// supply a command.
func wslRunsDefaultShell(words []string) bool {
	for i := 0; i < len(words); i++ {
		switch word := strings.ToLower(words[i]); word {
		case "<dynamic>":
			return true
		case "-d", "--distribution", "-u", "--user", "--cd", "--shell-type":
			i++
		case "--system", "~", "-e", "--exec", "--":
		default:
			return strings.HasPrefix(word, "-")
		}
	}
	return true
}

// CMD runs piped lines as commands unless a fixed /c or /k program was given.
func powerShellCommandProcessorReadsStdin(words []string) bool {
	for _, word := range words {
		if word == "<dynamic>" {
			return true
		}
		if strings.EqualFold(word, "/c") || strings.EqualFold(word, "/k") {
			return false
		}
	}
	return true
}

// The fixed script block encodes a completed data string. CMD reinterprets its
// argument as program syntax, so it cannot inherit the native argv contract.
// Runtime resolution covers aliases and command expressions as well as paths.
// A process launcher that starts a command interpreter, and a cmdlet that
// compiles its argument, treat a parameter as program text too.
const powerShellNativeArgumentEncoder = `& {
 param($c,$v,[string]$denied,[string]$evaluationDenied,[bool]$operatorArgument,[bool]$launcher)
 if ($c -is [System.Management.Automation.AliasInfo]) {$c=$c.ResolvedCommand}
 if (!$operatorArgument -and $c -is [System.Management.Automation.CmdletInfo] -and ($c.ImplementingType.FullName -in @('Microsoft.PowerShell.Commands.InvokeExpressionCommand','Microsoft.PowerShell.Commands.AddTypeCommand') -or ($launcher -and $c.ImplementingType.FullName -eq 'Microsoft.PowerShell.Commands.StartProcessCommand'))) {throw $evaluationDenied}
 if ($c -is [System.Management.Automation.CommandInfo] -and $c.CommandType -eq [System.Management.Automation.CommandTypes]::Application) {
  if ($operatorArgument -and ($c.Path -match '\.(cmd|bat)$' -or [System.IO.Path]::GetFileName($c.Path) -ieq 'cmd.exe')) {return ,$v}
  if ([System.IO.Path]::GetFileName($c.Path) -ieq 'cmd.exe') {throw $denied}
  $q=[string][char]34
  foreach ($item in @($v)) {
   $text=[string]$item
   if ($c.Path -match '\.(cmd|bat)$') {$q+($text.Replace('%','%%cd:~,%') -replace ('(\\*)'+$q),('$1$1'+$q+$q))+$q} else {$q+(($text -replace ('(\\*)'+$q),('$1$1'+$q+$q)) -replace '(\\+)$','$1$1')+$q}
  }
 } else {,$v}
}`

// Resolve aliases explicitly: ResolvedCommand can be null before module autoload.
// Capture the final target before argument evaluation can rebind the alias.
const powerShellCommandResolver = `& {param($v) if ($v -is [scriptblock] -or $v -is [System.Management.Automation.CommandInfo]) {$c=$v} else {$c=$ExecutionContext.InvokeCommand.GetCommand([string]$v,[System.Management.Automation.CommandTypes]::All)}; for ($i=0;$c -is [System.Management.Automation.AliasInfo];$i++) {if ($i -ge 16) {throw 'PowerShell command alias resolution exceeded its safe bound'}; $c=$ExecutionContext.InvokeCommand.GetCommand($c.Definition,[System.Management.Automation.CommandTypes]::All)}; ,$c}`

// Pipeline input can reach an evaluation cmdlet without any argument AST. Check
// its resolved implementation before invocation, retaining normal data commands.
const powerShellPipelineCommandGuard = `& {param($c,[string]$denied,[bool]$pythonStdin,[string]$pythonDenied,[bool]$hostStdin,[bool]$shellStdin,[bool]$cmdStdin,[bool]$wslStdin) if ($c -is [System.Management.Automation.CmdletInfo] -and $c.ImplementingType.FullName -eq 'Microsoft.PowerShell.Commands.InvokeExpressionCommand') {throw $denied}; if ($c -is [System.Management.Automation.ApplicationInfo]) {$n=[System.IO.Path]::GetFileNameWithoutExtension($c.Path); if (($pythonStdin -and [System.IO.Path]::GetFileName($c.Path) -match '^pythonw?(?:[23](?:\.[0-9]+)*)?(?:\.exe)?$') -or ($hostStdin -and $n -match '^(powershell|pwsh)$') -or ($shellStdin -and $n -match '^(bash|sh|dash|ksh|zsh)$') -or ($cmdStdin -and $n -ieq 'cmd') -or ($wslStdin -and $n -ieq 'wsl')) {throw $pythonDenied}}; ,$c}`

// A nested PowerShell host joins the words after its command into program
// text, a Unix shell parses its -c string or script, and wsl hands its whole
// command line to a shell. The role of each word is classified from the
// template; which application the head resolves to is only known at run time.
const powerShellNestedShellGuard = `& {param($c,[string]$denied,[bool]$hostSource,[bool]$shellSource,[bool]$anySource) if ($c -is [System.Management.Automation.ApplicationInfo]) {$n=[System.IO.Path]::GetFileNameWithoutExtension($c.Path); if (($hostSource -and $n -match '^(powershell|pwsh)$') -or ($shellSource -and $n -match '^(bash|sh|dash|ksh|zsh)$') -or ($anySource -and $n -ieq 'wsl')) {throw $denied}}; ,$c}`

// A fixed CMD program still expands environment variables as program syntax,
// so it cannot run once the template has put parameter data into one.
const powerShellEnvironmentProgramGuard = `& {param($c,[string]$denied) if ($c -is [System.Management.Automation.ApplicationInfo] -and [System.IO.Path]::GetFileName($c.Path) -ieq 'cmd.exe') {throw $denied}; ,$c}`

// Apply source-role checks to the actual captured application, after alias
// resolution. A same-named managed function still receives ordinary data.
const powerShellPythonSourceGuard = `& {param($c,[string]$denied) if ($c -is [System.Management.Automation.ApplicationInfo] -and [System.IO.Path]::GetFileName($c.Path) -match '^pythonw?(?:[23](?:\.[0-9]+)*)?(?:\.exe)?$') {throw $denied}; ,$c}`

const powerShellTemplateParser = `$ErrorActionPreference='Stop'
[Console]::InputEncoding=New-Object System.Text.UTF8Encoding
[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding
$inputData=ConvertFrom-Json ([Console]::In.ReadToEnd())
$tokens=$null; $parseErrors=$null
$ast=[System.Management.Automation.Language.Parser]::ParseInput($inputData.program,[ref]$tokens,[ref]$parseErrors)
if ($parseErrors.Count) { exit 2 }
$strings=$ast.FindAll({param($n) $n -is [System.Management.Automation.Language.StringConstantExpressionAst] -or $n -is [System.Management.Automation.Language.ExpandableStringExpressionAst]},$true)
$variables=$ast.FindAll({param($n) $n -is [System.Management.Automation.Language.VariableExpressionAst]},$true)
$taintedVariables=@{}
$assignedVariables=@{}
$environmentWritten=$false
$fixedVariables=@('true','false','null','PWD','HOME','PSScriptRoot','PSCommandPath','PID','PSHOME','PSVersionTable','PSCulture','PSUICulture','ExecutionContext','Host','LASTEXITCODE','?')
function Get-TemplateVariableName($variable) { $variable.VariablePath.UserPath -replace '^(global|script|local|private):','' }
# A variable is independent of the parameters only when every way the template
# gives it a value is an assignment statement of uninfluenced data. Loop,
# pipeline and parameter variables, and variables a command writes by name,
# have no such statement.
function Test-TemplateVariableTainted($variable) {
 $name=Get-TemplateVariableName $variable
 if ($taintedVariables[$name]) {return $true}
 if ($name -match '^env:') {return $environmentWritten}
 return !($assignedVariables[$name] -or $fixedVariables -contains $name)
}
function Test-TemplateInfluenced($node) {
 if ($null -eq $node) {return $false}
 foreach ($marker in $inputData.markers) {if ($node.Extent.Text.Contains($marker)) {return $true}}
 foreach ($variable in $node.FindAll({param($n) $n -is [System.Management.Automation.Language.VariableExpressionAst]},$true)) {
  if (Test-TemplateVariableTainted $variable) {return $true}
 }
 return $false
}
# A nested PowerShell host joins every word after its command into program
# text; only the arguments of a fixed script file are data.
function Test-HostTemplateSourceInfluenced($command) {
 $elements=$command.CommandElements
 for ($i=1;$i -lt $elements.Count;$i++) {
  $node=$elements[$i]
  if (Test-TemplateInfluenced $node) {return $true}
  if ($node -is [System.Management.Automation.Language.CommandParameterAst] -and $null -eq $node.Argument -and $node.ParameterName -match '^f(i(le?)?)?$' -and $i+1 -lt $elements.Count -and !(Test-TemplateInfluenced $elements[$i+1])) {return $false}
 }
 return $false
}
# A Unix shell takes its options and the first other word (the -c string or
# the script) as program; the words after that are positional data.
function Test-ShellTemplateSourceInfluenced($command) {
 $elements=$command.CommandElements
 for ($i=1;$i -lt $elements.Count;$i++) {
  $node=$elements[$i]
  if (Test-TemplateInfluenced $node) {return $true}
  if ($node -is [System.Management.Automation.Language.StringConstantExpressionAst]) {$word=$node.Value}
  elseif ($node -is [System.Management.Automation.Language.CommandParameterAst] -and $null -eq $node.Argument) {$word='-'+$node.ParameterName}
  else {
   # A word produced at run time can be an option that makes a later word the program.
   for ($j=$i+1;$j -lt $elements.Count;$j++) {if (Test-TemplateInfluenced $elements[$j]) {return $true}}
   return $false
  }
  if ($word -eq '--') {return ($i+1 -lt $elements.Count -and (Test-TemplateInfluenced $elements[$i+1]))}
  if (!$word.StartsWith('-') -and !$word.StartsWith('+')) {return $false}
  if ($word -cmatch '^[-+][A-Za-z]*[oO]$') {$i++; if ($i -lt $elements.Count -and (Test-TemplateInfluenced $elements[$i])) {return $true}}
 }
 return $false
}
function Test-AnyTemplateElementInfluenced($command) {
 foreach ($node in $command.CommandElements | Select-Object -Skip 1) {if (Test-TemplateInfluenced $node) {return $true}}
 return $false
}
# A process launcher is data-only when its program is a fixed name other than
# a command interpreter. Its first positional word is that program.
function Test-TemplateLauncherShell($command) {
 $elements=$command.CommandElements
 $program=$null
 for ($i=1;$i -lt $elements.Count -and $null -eq $program;$i++) {
  $node=$elements[$i]
  if ($node -isnot [System.Management.Automation.Language.CommandParameterAst]) {$program=$node; break}
  if ($node.ParameterName -match '^f') {if ($null -ne $node.Argument) {$program=$node.Argument} elseif ($i+1 -lt $elements.Count) {$program=$elements[$i+1]}; break}
  if ($null -eq $node.Argument -and $node.ParameterName -notmatch '^(l|n|p|u|wai|wh|co)') {$i++}
 }
 if ($program -isnot [System.Management.Automation.Language.StringConstantExpressionAst] -or (Test-TemplateInfluenced $program)) {return $true}
 return [System.IO.Path]::GetFileNameWithoutExtension($program.Value) -match '^(cmd|powershell|pwsh|bash|sh|dash|ksh|zsh|wsl)$'
}
# Classify only the original argument AST. A dynamic pre-source word could be
# an execution option; do not assume it establishes the data argv boundary.
function Test-PythonTemplateSourceInfluenced($command) {
 $elements=$command.CommandElements
 for ($i=1;$i -lt $elements.Count;$i++) {
  $node=$elements[$i]
  if (Test-TemplateInfluenced $node) {return $true}
  if ($node -is [System.Management.Automation.Language.StringConstantExpressionAst]) {$word=$node.Value}
  elseif ($node -is [System.Management.Automation.Language.CommandParameterAst] -and $null -eq $node.Argument) {$word='-'+$node.ParameterName}
  else {return $true}
  if ($word -eq '--') {
   if ($i+1 -ge $elements.Count) {return $false}
   return (Test-TemplateInfluenced $elements[$i+1])
  }
  if ($word -eq '-' -or !$word.StartsWith('-')) {return $false}
  if ($word.StartsWith('--')) {
   if ($word -ne '--check-hash-based-pycs' -or $i+1 -ge $elements.Count) {return $true}
   $i++; if (Test-TemplateInfluenced $elements[$i]) {return $true}
   if ($elements[$i] -isnot [System.Management.Automation.Language.StringConstantExpressionAst]) {return $true}
   continue
  }
  for ($j=1;$j -lt $word.Length;$j++) {
   $flag=$word[$j]
   if ($flag -ceq 'c' -or $flag -ceq 'm') {
    if ($j+1 -lt $word.Length) {return $false}
    if ($i+1 -ge $elements.Count) {return $true}
    return (Test-TemplateInfluenced $elements[$i+1])
   }
   if ($flag -ceq 'W' -or $flag -ceq 'X') {
    if ($j+1 -eq $word.Length) {
     $i++; if ($i -ge $elements.Count -or (Test-TemplateInfluenced $elements[$i])) {return $true}
     if ($elements[$i] -isnot [System.Management.Automation.Language.StringConstantExpressionAst]) {return $true}
    }
    break
   }
   if ('bBdEiIOPqsSuvx'.IndexOf($flag) -lt 0) {return $true}
  }
 }
 return $false
}
# Merge assignments across branches/scopes; never assume a later overwrite is
# taken. Only original AST markers seed this finite variable-definition flow.
$assignments=$ast.FindAll({param($n) $n -is [System.Management.Automation.Language.AssignmentStatementAst]},$true)
foreach ($assignment in $assignments) {
 foreach ($variable in $assignment.Left.FindAll({param($n) $n -is [System.Management.Automation.Language.VariableExpressionAst]},$true)) {$assignedVariables[(Get-TemplateVariableName $variable)]=$true}
}
foreach ($name in @('_','PSItem','input','args','PSBoundParameters','Matches','foreach','switch','Error','MyInvocation')) {$taintedVariables[$name]=$true}
foreach ($parameter in $ast.FindAll({param($n) $n -is [System.Management.Automation.Language.ParameterAst]},$true)) {$taintedVariables[(Get-TemplateVariableName $parameter.Name)]=$true}
foreach ($loop in $ast.FindAll({param($n) $n -is [System.Management.Automation.Language.ForEachStatementAst]},$true)) {$taintedVariables[(Get-TemplateVariableName $loop.Variable)]=$true}
if ($ast.Find({param($n) $n -is [System.Management.Automation.Language.InvokeMemberExpressionAst] -and $n.Member -is [System.Management.Automation.Language.StringConstantExpressionAst] -and $n.Member.Value -ieq 'SetEnvironmentVariable'},$true)) {$environmentWritten=$true}
$commands=$ast.FindAll({param($n) $n -is [System.Management.Automation.Language.CommandAst]},$true)
foreach ($command in $commands) {
 # A fixed word of a command can be the name of a variable that command writes.
 foreach ($element in $command.CommandElements | Select-Object -Skip 1) {
  foreach ($word in $element.FindAll({param($n) $n -is [System.Management.Automation.Language.StringConstantExpressionAst]},$true)) {
   $name=$word.Value -replace '^(variable|global|script|local|private):',''
   if ($name -match '^env:') {$environmentWritten=$true} elseif ($name) {$taintedVariables[$name]=$true}
  }
 }
}
$changed=$true
while ($changed) {
 $changed=$false
 foreach ($assignment in $assignments) {
  if (!(Test-TemplateInfluenced $assignment.Right)) {continue}
  foreach ($variable in $assignment.Left.FindAll({param($n) $n -is [System.Management.Automation.Language.VariableExpressionAst]},$true)) {
   $name=Get-TemplateVariableName $variable
   if (!$taintedVariables[$name]) {$taintedVariables[$name]=$true; $changed=$true}
  }
 }
}
$result=@{}
$native=@{}
$scriptBlockParameters=$false
foreach ($node in @($strings)+@($variables)) {
 if (!(Test-TemplateInfluenced $node)) {continue}
  # Examine the original inert AST only. Constructor arguments may be wrapped
  # in parentheses, concatenations or casts, so inspect all ancestors.
  for ($ancestor=$node.Parent;$null -ne $ancestor;$ancestor=$ancestor.Parent) {
   $typeName=''
   if ($ancestor -is [System.Management.Automation.Language.ConvertExpressionAst]) {
    $convertedChild=$ancestor.Child
    while ($convertedChild -is [System.Management.Automation.Language.ParenExpressionAst] -and $convertedChild.Pipeline.PipelineElements.Count -eq 1 -and $convertedChild.Pipeline.PipelineElements[0] -is [System.Management.Automation.Language.CommandExpressionAst]) {
     $convertedChild=$convertedChild.Pipeline.PipelineElements[0].Expression
    }
    # A literal script block already has a trusted parsed body. Its string data
    # placeholders are not source passed through a string-to-code conversion.
    if ($convertedChild -isnot [System.Management.Automation.Language.ScriptBlockExpressionAst]) {$typeName=$ancestor.Type.TypeName.FullName}
   } elseif ($ancestor -is [System.Management.Automation.Language.InvokeMemberExpressionAst] -and $ancestor.Static -and $ancestor.Member -is [System.Management.Automation.Language.StringConstantExpressionAst] -and $ancestor.Member.Value -ieq 'Create' -and $ancestor.Expression -is [System.Management.Automation.Language.TypeExpressionAst]) {
    $typeName=$ancestor.Expression.TypeName.FullName
   }
   if ($typeName -in @('scriptblock','System.Management.Automation.ScriptBlock')) {$scriptBlockParameters=$true}
  }
}
foreach ($marker in $inputData.markers) {
 $candidates=@($strings | Where-Object {$_.Extent.Text.Contains($marker)} | Sort-Object {$_.Extent.Text.Length})
 foreach ($node in $candidates) {
  $nested=$false
  if ($node.StringConstantType.ToString() -eq 'BareWord' -and $node.Parent -is [System.Management.Automation.Language.CommandAst] -and $node.Parent.CommandElements[0] -eq $node -and $node.Parent.InvocationOperator.ToString() -ne 'Ampersand') { continue }
  if ($node -is [System.Management.Automation.Language.ExpandableStringExpressionAst]) {
   foreach ($expression in $node.NestedExpressions) { if ($expression.Extent.Text.Contains($marker)) {$nested=$true} }
  }
  if (!$nested) {
   $mode=$node.StringConstantType.ToString()
   if ($node.Parent -is [System.Management.Automation.Language.CommandAst] -and $node.Parent.CommandElements[0] -ne $node) {
    $expanded=$node -is [System.Management.Automation.Language.ExpandableStringExpressionAst] -and $node.NestedExpressions.Count
    # Runtime command resolution decides whether native encoding is needed.
    # An earlier operator statement can create/rebind aliases or functions.
    if ($mode -in @('BareWord','SingleQuoted','DoubleQuoted')) {
     $mode='NativeLiteral'
     if ($expanded) {$mode='NativeExpanded'}
    }
   }
   $result[$marker]=$mode; break
  }
 }
}
$argumentNodes=$ast.FindAll({param($n) $n -is [System.Management.Automation.Language.ExpressionAst] -and (($n.Parent -is [System.Management.Automation.Language.CommandAst] -and $n.Parent.CommandElements[0] -ne $n) -or ($n.Parent -is [System.Management.Automation.Language.CommandParameterAst] -and $n.Parent.Argument -eq $n))},$true)
foreach ($node in $argumentNodes) {
 $command=$node.Parent
 if ($command -is [System.Management.Automation.Language.CommandParameterAst]) {$command=$command.Parent}
 if ($command -isnot [System.Management.Automation.Language.CommandAst] -or $command.CommandElements[0] -eq $node) {continue}
 $stringNode=$node -is [System.Management.Automation.Language.StringConstantExpressionAst] -or $node -is [System.Management.Automation.Language.ExpandableStringExpressionAst]
 if ($stringNode -and $node.StringConstantType.ToString() -notin @('BareWord','SingleQuoted','DoubleQuoted')) {continue}
 $influenced=Test-TemplateInfluenced $node
 # This describes independence from known template-parameter flow only;
 # it does not prove an arbitrary runtime expression or environment trusted.
 $operatorArgument=!$influenced -and (Test-TemplateInfluenced $command)
 if (!$influenced -and !$operatorArgument) {continue}
 # Also bind an outer argument whose placeholders occur only in a subexpression.
 $expanded=!$stringNode -or ($node -is [System.Management.Automation.Language.ExpandableStringExpressionAst] -and $node.NestedExpressions.Count)
 $start=[System.Text.Encoding]::UTF8.GetByteCount($inputData.program.Substring(0,$node.Extent.StartOffset))
 $end=$start+[System.Text.Encoding]::UTF8.GetByteCount($node.Extent.Text)
 $head=$command.CommandElements[0]
 $literal=$head -is [System.Management.Automation.Language.StringConstantExpressionAst]
 $reference='$'+$inputData.bindingPrefix+'X'+$head.Extent.StartOffset
 $headStart=[System.Text.Encoding]::UTF8.GetByteCount($inputData.program.Substring(0,$head.Extent.StartOffset))
 $headEnd=$headStart+[System.Text.Encoding]::UTF8.GetByteCount($head.Extent.Text)
 $value=''
 if ($literal) {$value=$head.Value}
 $invoke=$command.InvocationOperator.ToString() -notin @('Ampersand','Dot')
 $native[$head.Extent.StartOffset]=@{Text=$head.Extent.Text;Value=$value;Head=$true;Literal=$literal;Invoke=$invoke;Reference=$reference;Start=$headStart;End=$headEnd;PythonSource=[bool](Test-PythonTemplateSourceInfluenced $command);HostSource=[bool](Test-HostTemplateSourceInfluenced $command);ShellSource=[bool](Test-ShellTemplateSourceInfluenced $command);AnySource=[bool](Test-AnyTemplateElementInfluenced $command)}
 $argumentValue=''
 if ($stringNode) {$argumentValue=$node.Value}
 $native[$node.Extent.StartOffset]=@{Text=$node.Extent.Text;Value=$argumentValue;Reference=$reference;Expanded=[bool]$expanded;Start=$start;End=$end;OperatorArgument=[bool]$operatorArgument;Launcher=[bool](Test-TemplateLauncherShell $command)}
}
# Pipeline consumers may have no explicit argument, so bind their heads when an
# earlier element contains original markers or assignment-influenced variables.
# Retain influence through intermediates rather than trusting their transforms.
function Get-TemplateHeadEntry($element) {
 $head=$element.CommandElements[0]
 $entry=$native[$head.Extent.StartOffset]
 if ($null -eq $entry) {
  $literal=$head -is [System.Management.Automation.Language.StringConstantExpressionAst]
  $value=''; if ($literal) {$value=$head.Value}
  $start=[System.Text.Encoding]::UTF8.GetByteCount($inputData.program.Substring(0,$head.Extent.StartOffset))
  $end=$start+[System.Text.Encoding]::UTF8.GetByteCount($head.Extent.Text)
  $reference='$'+$inputData.bindingPrefix+'X'+$head.Extent.StartOffset
  $invoke=$element.InvocationOperator.ToString() -notin @('Ampersand','Dot')
  $entry=@{Text=$head.Extent.Text;Value=$value;Head=$true;Literal=$literal;Invoke=$invoke;Reference=$reference;Start=$start;End=$end}
  $native[$head.Extent.StartOffset]=$entry
 }
 return $entry
}
$pipelines=$ast.FindAll({param($n) $n -is [System.Management.Automation.Language.PipelineAst]},$true)
foreach ($pipeline in $pipelines) {
 $incoming=$false
 foreach ($element in $pipeline.PipelineElements) {
  if ($incoming -and $element -is [System.Management.Automation.Language.CommandAst]) {
   $entry=Get-TemplateHeadEntry $element
   $entry.Pipeline=$true
   $entry.PipelineArguments=@(foreach ($argument in $element.CommandElements | Select-Object -Skip 1) {
    if ($argument -is [System.Management.Automation.Language.StringConstantExpressionAst]) {$argument.Value}
    elseif ($argument -is [System.Management.Automation.Language.CommandParameterAst] -and $null -eq $argument.Argument) {'-'+$argument.ParameterName}
    else {'<dynamic>'}
   })
  }
  if (Test-TemplateInfluenced $element) {$incoming=$true}
 }
}
# A fixed CMD program expands environment variables as program syntax.
foreach ($name in @($taintedVariables.Keys)) {if ($name -match '^env:') {$environmentWritten=$true}}
if ($environmentWritten) {
 foreach ($command in $commands) {(Get-TemplateHeadEntry $command).EnvironmentProgram=$true}
}
ConvertTo-Json -Compress -Depth 5 @{Modes=$result;NativeArguments=@($native.Values);ScriptBlockParameters=$scriptBlockParameters}`
