package intercept

import (
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// A wrapper is unwrapped only when its option semantics are known. Unknown
// flags and listing/help modes never turn their data arguments into commands.
func wrappedCommand(name string, args []string) ([]string, bool) {
	switch name {
	case "command", "exec", "env", "sudo", "nohup", "nice", "timeout", "builtin":
	default:
		return nil, false
	}
	index := 0
	for index < len(args) {
		arg := args[index]
		if arg == "--" {
			index++
			break
		}
		if (name == "env" || name == "sudo") && shellAssignment(arg) {
			index++
			continue
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" && name != "env" {
			break
		}
		valueOption, flag := false, false
		switch name {
		case "command":
			// -v/-V query command identity without executing it.
			if strings.ContainsAny(arg, "vV") {
				return nil, true
			}
			flag = arg == "-p"
		case "exec":
			flag = arg == "-c" || arg == "-l" || arg == "-cl" || arg == "-lc"
			valueOption = arg == "-a"
		case "builtin":
			// Only -- is accepted above; other options do not invoke a builtin.
		case "env":
			flag = arg == "-i" || arg == "--ignore-environment" || arg == "-" || strings.HasPrefix(arg, "--unset=") || strings.HasPrefix(arg, "--chdir=")
			valueOption = arg == "-u" || arg == "--unset" || arg == "-C" || arg == "--chdir"
			if nextValue, known := shortWrapperOptions(arg, "i", "uC"); known {
				valueOption, flag = nextValue, !nextValue
			}
		case "sudo":
			flag = arg == "-n" || arg == "--non-interactive" || arg == "-E" || arg == "--preserve-env" || strings.HasPrefix(arg, "--preserve-env=") || arg == "-H" || arg == "--set-home" || strings.HasPrefix(arg, "--user=") || strings.HasPrefix(arg, "--group=")
			valueOption = arg == "-u" || arg == "--user" || arg == "-g" || arg == "--group"
		case "nice":
			valueOption = arg == "-n" || arg == "--adjustment"
			_, err := strconv.Atoi(strings.TrimPrefix(arg, "-"))
			flag = err == nil || strings.HasPrefix(arg, "--adjustment=")
			if strings.HasPrefix(arg, "-n") && len(arg) > 2 {
				_, err := strconv.Atoi(arg[2:])
				flag = err == nil
			}
		case "timeout":
			flag = arg == "--foreground" || arg == "--preserve-status" || arg == "--verbose" || strings.HasPrefix(arg, "--signal=") || strings.HasPrefix(arg, "--kill-after=")
			valueOption = arg == "-s" || arg == "--signal" || arg == "-k" || arg == "--kill-after"
			if nextValue, known := shortWrapperOptions(arg, "v", "sk"); known {
				valueOption, flag = nextValue, !nextValue
			}
		}
		if valueOption {
			if index+1 >= len(args) {
				return nil, true
			}
			index += 2
		} else if flag {
			index++
		} else {
			return nil, true
		}
	}
	if name == "env" || name == "sudo" {
		for index < len(args) && shellAssignment(args[index]) {
			index++
		}
	}
	if name == "timeout" {
		if index >= len(args) {
			return nil, true
		}
		duration := strings.TrimRight(args[index], "smhd")
		value, err := strconv.ParseFloat(duration, 64)
		if err != nil || value < 0 {
			return nil, true
		}
		index++
	}
	if name == "builtin" && index < len(args) {
		// Do not project non-builtin names such as "builtin rm" as execution.
		// Other ordinary builtins have no nested executable view here.
		switch args[index] {
		case "eval", "source", ".", "command", "builtin", "exec", "kill", "trap", "alias", "<dynamic>":
		default:
			return nil, true
		}
	}
	return args[index:], true
}

func shellAssignment(arg string) bool {
	key, _, ok := strings.Cut(arg, "=")
	return ok && syntax.ValidName(key)
}

// Value-taking options consume the rest of the same token, or the next token
// when no suffix remains. Unknown short options leave the wrapper unresolved.
func shortWrapperOptions(arg, flags, values string) (nextValue, known bool) {
	if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
		return false, false
	}
	for n := 1; n < len(arg); n++ {
		if strings.ContainsRune(flags, rune(arg[n])) {
			continue
		}
		if strings.ContainsRune(values, rune(arg[n])) {
			return n+1 == len(arg), true
		}
		return false, false
	}
	return false, true
}

func shellCommandString(shell string, args []string) (string, bool) {
	program, _ := shellProgramInput(shell, args)
	return program, program != ""
}

// Distinguish command strings, stdin programs, script files and syntax checks.
// The second result means stdin is executable, not that its bytes are known.
func shellProgramInput(shell string, args []string) (string, bool) {
	source := shellProgramSource(shell, args)
	if source.kind == "command" {
		return args[source.index], false
	}
	return "", source.kind == "stdin"
}

type shellSource struct {
	kind    string
	index   int
	command bool
	stopped bool
}

// Keep the source role and option boundary even when its bytes are unresolved.
// Callers can require approval without treating a script filename as code.
func shellProgramSource(shell string, args []string) shellSource {
	command, noExec, interactive, stdin := false, false, false, false
	finish := func(index int, stopped bool) shellSource {
		source := shellSource{index: index, command: command, stopped: stopped}
		if noExec && !interactive {
			source.kind = "none"
			return source
		}
		if command {
			if index < len(args) {
				source.kind = "command"
				return source
			}
			source.kind = "none" // Missing -c argument cannot execute a program.
			return source
		}
		source.kind = "file"
		if stdin || index == len(args) {
			source.kind = "stdin"
		}
		return source
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			return finish(index+1, true)
		}
		if arg == "-" && !command {
			return finish(index+1, true)
		}
		if !strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "+") || arg == "-" || arg == "+" {
			return finish(index, false)
		}
		if arg == "--noprofile" || arg == "--norc" || arg == "--posix" {
			continue
		}
		if strings.HasPrefix(arg, "--") {
			return shellSource{kind: "unknown", index: index}
		}
		enable := arg[0] == '-'
		for _, flag := range arg[1:] {
			switch flag {
			case 'c':
				if !enable {
					return shellSource{kind: "unknown", index: index}
				}
				command = true
			case 'n':
				noExec = enable
			case 'i':
				interactive = enable
			case 's':
				stdin = enable
			case 'o', 'O':
				// Bash consumes a separate option-name argument, including when
				// -o/-O occurs inside a short-option cluster. Keep option data
				// separate from the first non-option -c program.
				if index+1 >= len(args) || !knownShellOption(shell, flag, args[index+1]) {
					return shellSource{kind: "unknown", index: index}
				}
				index++
				if flag == 'o' && args[index] == "noexec" {
					noExec = enable
				}
			case 'l', 'e', 'u', 'x', 'v', 'f':
			default:
				return shellSource{kind: "unknown", index: index}
			}
		}
	}
	return finish(len(args), false)
}

// Unknown names stay unresolved: invalid invocation options do not execute their
// following strings. No host shell or environment is consulted during policy.
func knownShellOption(shell string, flag rune, name string) bool {
	if shell != "bash" {
		return false
	}
	options := "allexport braceexpand emacs errexit errtrace functrace hashall histexpand history igncr ignoreeof interactive-comments keyword monitor noclobber noexec noglob nolog notify nounset onecmd physical pipefail posix privileged verbose vi xtrace"
	if flag == 'O' {
		options = "autocd assoc_expand_once cdable_vars cdspell checkhash checkjobs checkwinsize cmdhist compat31 compat32 compat40 compat41 compat42 compat43 compat44 completion_strip_exe complete_fullquote direxpand dirspell dotglob execfail expand_aliases extdebug extglob extquote failglob force_fignore globasciiranges globskipdots globstar gnu_errfmt histappend histreedit histverify hostcomplete huponexit inherit_errexit interactive_comments lastpipe lithist localvar_inherit localvar_unset login_shell mailwarn no_empty_cmd_completion nocaseglob nocasematch noexpand_translation nullglob patsub_replacement progcomp progcomp_alias promptvars restricted_shell shift_verbose sourcepath varredir_close xpg_echo"
	}
	for _, option := range strings.Fields(options) {
		if name == option {
			return true
		}
	}
	return false
}
