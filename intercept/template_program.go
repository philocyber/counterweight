package intercept

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ShellTemplateLiteralWord decodes a syntactically literal shell word without
// executing expansions or consulting runtime command/environment state.
func ShellTemplateLiteralWord(word *syntax.Word) (string, bool) { return staticWord(word) }

func templateShellName(name string) bool {
	return name == "bash" || name == "sh" || name == "dash" || name == "ksh"
}

// A script operand can name the interpreter's own input stream, and a word the
// template fills in at run time can be made to. Either makes stdin the program.
var templateStdinAlias = regexp.MustCompile(`^/(?:dev/(?:stdin|fd/[0-9]+)|proc/(?:self|thread-self|[0-9]+)/fd/[0-9]+)$|\{[^{}]+\}|^<dynamic>$`)

// templateOperandPath returns a script operand in the spelling templateStdinAlias
// reads: /dev//stdin and /dev/./stdin name the same stream as /dev/stdin.
func templateOperandPath(operand string) string {
	if strings.HasPrefix(operand, "/") {
		return path.Clean(operand)
	}
	return operand
}

// templateCommandString reports whether name is a recognized interpreter and the
// fixed words after it select its command-string option, so that a following
// word is program text. Behind a program that is not a listed wrapper only this
// form counts: a bare name is as likely data, as in "grep -i python".
func templateCommandString(name string, args []string, known []bool) bool {
	for i := range args {
		if !known[i] {
			args = args[:i]
			break
		}
	}
	if templateShellName(name) {
		return shellProgramSource(name, args).command
	}
	if !templatePythonName.MatchString(name) {
		return false
	}
	for i := 0; i < len(args); i++ {
		word := args[i]
		if word == "-" || !strings.HasPrefix(word, "-") || strings.HasPrefix(word, "--") {
			return false
		}
		for j := 1; j < len(word); j++ {
			switch word[j] {
			case 'c':
				return true
			case 'm':
				return false
			case 'W', 'X':
				if j+1 == len(word) {
					i++
				}
				j = len(word)
			}
		}
	}
	return false
}

// ShellTemplateProgramWords identifies words controlling a recognized nested
// shell or Python program. It performs no execution, filesystem or environment lookup.
// Unknown means the recognized invocation cannot be classified by this grammar;
// callers must not assume its placeholder arguments are ordinary data.
// This is not a classifier for arbitrary interpreters or runtime-defined commands.
func ShellTemplateProgramWords(call *syntax.CallExpr) (protected []*syntax.Word, unknown bool) {
	if len(call.Args) == 0 {
		return nil, false
	}
	words := make([]string, len(call.Args))
	known := make([]bool, len(words))
	for i, word := range call.Args {
		words[i], known[i] = staticWord(word)
	}
	offset := 0
	for known[offset] {
		name := path.Base(words[offset])
		tail, wrapper := wrappedCommand(name, words[offset+1:])
		if !wrapper {
			break
		}
		if len(tail) == 0 {
			// Identity queries never execute their argument.
			if name == "command" && len(words) > offset+1 && known[offset+1] && (words[offset+1] == "-v" || words[offset+1] == "-V") {
				return nil, false
			}
			return nil, true
		}
		next := len(words) - len(tail)
		for i := offset; i < next; i++ {
			if !known[i] || !singleRequestArgument(call.Args[i]) {
				return nil, true
			}
		}
		protected = append(protected, call.Args[offset:next]...)
		offset = next
	}
	if !known[offset] {
		// A command name selected at run time can be any interpreter.
		return nil, true
	}
	name := path.Base(words[offset])
	switch name {
	case "eval", "trap", "mapfile", "readarray":
		// eval joins every argument and parses the result again. A trap action
		// and a mapfile callback are command lists selected by fixed options.
		return append(protected, call.Args[offset:]...), false
	case "source", ".":
		// The sourced file is a program; later words are its positional data.
		end := offset + 2
		if end < len(words) && known[offset+1] && words[offset+1] == "--" {
			end++
		}
		if end > len(words) {
			end = len(words)
		}
		return append(protected, call.Args[offset:end]...), false
	case "compgen":
		// The -W word list is expanded with its command substitutions, and -C
		// and -F name a command to run. Only what follows a fixed -- is the
		// word to complete.
		end := len(words)
		for i := offset + 1; i < end; i++ {
			if known[i] && words[i] == "--" {
				end = i
			}
		}
		return append(protected, call.Args[offset:end]...), false
	}
	embedded := false
	if !templateShellName(name) && !templatePythonName.MatchString(name) {
		if len(protected) != 0 {
			// Changing a wrapped command identity can select an interpreter.
			protected = append(protected, call.Args[offset])
		}
		if name == "xargs" && templateXargsProgram(words[offset+1:], known[offset+1:]) {
			// Every argument and input line can reach the command string.
			return call.Args, false
		}
		// Launchers outside the wrapper list (xargs, find -exec, setsid, ...)
		// still start a recognized interpreter named later in their arguments.
		for i := offset + 1; i < len(words) && !embedded; i++ {
			if known[i] && templateCommandString(path.Base(words[i]), words[i+1:], known[i+1:]) {
				offset, name, embedded = i, path.Base(words[i]), true
			}
		}
		if !embedded {
			return protected, false
		}
	}
	// An embedded invocation whose options cannot be classified keeps every
	// following word fixed instead of refusing its launcher's own data.
	rest := append(append([]*syntax.Word{}, protected...), call.Args[offset:]...)
	if templatePythonName.MatchString(name) {
		roles, unknown := pythonTemplateProgramWords(call, offset, words, known, protected)
		if unknown && embedded {
			return rest, false
		}
		return roles, unknown
	}
	source := shellProgramSource(name, words[offset+1:])
	end := offset + 1 + source.index
	if end > len(words) {
		end = len(words)
	}
	unknown = source.kind == "unknown"
	for i := offset; i < end && !unknown; i++ {
		unknown = !known[i] || !singleRequestArgument(call.Args[i])
	}
	if unknown {
		if embedded {
			return rest, false
		}
		return nil, true
	}
	// Option words must remain fixed: interpolation could select -c or change
	// noexec mode. Script filenames and positional arguments are separate data.
	protected = append(protected, call.Args[offset:end]...)
	if source.kind == "command" && end < len(words) {
		protected = append(protected, call.Args[end])
	}
	if !source.command && end < len(words) && (!source.stopped || templateProcessSubstitution(call.Args[end])) {
		// A presumed script filename can become an option after substitution,
		// and a process substitution is program text even after a fixed --.
		protected = append(protected, call.Args[end])
	}
	return protected, false
}

// templateXargsProgram reports whether the words after xargs start a recognized
// interpreter with a command string while a replace-string option copies each
// input line into the arguments, that string included. A word before the
// interpreter that is not fixed text can be that option. Without a word after
// the command-string option, the first input word is the program itself.
func templateXargsProgram(words []string, known []bool) bool {
	for i := range words {
		name, rest := path.Base(words[i]), words[i+1:]
		if !known[i] || !templateCommandString(name, rest, known[i+1:]) {
			continue
		}
		for j, option := range words[:i] {
			short := strings.HasPrefix(option, "-") && !strings.HasPrefix(option, "--") && strings.ContainsAny(option, "Ii")
			if !known[j] || templatePlaceholder.MatchString(option) || short || strings.HasPrefix(option, "--r") {
				return true
			}
		}
		if slices.Contains(known[i+1:], false) {
			return false
		}
		if templateShellName(name) {
			source := shellProgramSource(name, rest)
			return source.command && source.index >= len(rest)
		}
		return !templateCommandString(name, rest[:len(rest)-1], known[i+1:len(words)-1])
	}
	return false
}

func templateProcessSubstitution(word *syntax.Word) bool {
	found := false
	syntax.Walk(word, func(node syntax.Node) bool {
		if _, ok := node.(*syntax.ProcSubst); ok {
			found = true
		}
		return !found
	})
	return found
}
