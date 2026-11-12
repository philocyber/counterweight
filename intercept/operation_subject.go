package intercept

import (
	"encoding/json"
	"errors"
	"path"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// operationSubjects adds static executable views for restrictive rules only.
// Original input remains authoritative for audit, allow rules and execution.
// This is not a shell sandbox: variable/generated programs need runtime controls.
func operationSubjects(names []string, input []byte) []string {
	return analyzeOperation(names, input).subjects
}

type operationAnalysis struct {
	subjects             []string
	runtimeExecutable    bool
	requestMethodUnknown bool
	shellProgramUnknown  bool
	// requestMethodDynamic narrows requestMethodUnknown to calls that leave the
	// method open themselves, not merely to the client's default startup file.
	requestMethodDynamic bool
	// continuesLine is set for a shell_send call that adds to a line earlier
	// calls left pending, so its own input is only part of a command.
	continuesLine bool
}

func analyzeOperation(names []string, input []byte) operationAnalysis {
	var args struct {
		Command string `json:"command"`
		Text    string `json:"text"`
		Method  string `json:"method"`
	}
	var out operationAnalysis
	if err := json.Unmarshal(input, &args); err != nil {
		// The tool still reads the fields that did decode, so input that cannot
		// be read here is a shell program that was not inspected, not an empty one.
		for _, name := range names {
			if name == "Bash" || name == "shell_open" || name == "shell_send" {
				out.shellProgramUnknown = true
			}
		}
		// A field of the wrong type leaves the others decoded here as well, and
		// the rules still apply to the command they hold.
		if !out.shellProgramUnknown || !errors.As(err, new(*json.UnmarshalTypeError)) {
			return out
		}
	}
	for _, name := range names {
		switch name {
		case "Bash", "shell_open":
			analysis := analyzeShell(args.Command)
			out.subjects = append(out.subjects, analysis.subjects...)
			out.runtimeExecutable = out.runtimeExecutable || analysis.runtimeExecutable
			out.requestMethodUnknown = out.requestMethodUnknown || analysis.requestMethodUnknown
			out.requestMethodDynamic = out.requestMethodDynamic || analysis.requestMethodDynamic
			out.shellProgramUnknown = out.shellProgramUnknown || analysis.shellProgramUnknown
		case "shell_send":
			// Everything the call writes to the session is covered, in the order
			// the session receives it: text typed and text pasted onto one line
			// are one command, and bytes that act as key presses or cannot be
			// read as text leave the program not inspectable.
			var call shellSendCall
			failed := json.Unmarshal(input, &call) != nil
			send := call.read(failed)
			out.shellProgramUnknown = out.shellProgramUnknown || send.unknown
			programs := []string{send.program}
			if call.Before != "" || call.Unknown {
				// The line starts with what earlier calls left pending. A program
				// may have read that text in the meantime, so the call is also
				// read as the start of a line of its own.
				out.continuesLine = true
				alone := call
				alone.Before, alone.Unknown = "", false
				programs = append(programs, alone.read(failed).program)
			}
			if call.Text != "" && call.Paste != "" {
				// Each channel is read on its own as well.
				programs = append(programs, strings.ReplaceAll(call.Text, "\r", "\n"), strings.ReplaceAll(call.Paste, "\r", "\n"))
			}
			if len(programs) > 1 {
				// The input holds the line in pieces, so the restrictive rules
				// are also given the text those pieces form on the terminal.
				out.subjects = append(out.subjects, send.program)
			}
			for _, typed := range programs {
				analysis := analyzeShell(typed)
				out.subjects = append(out.subjects, analysis.subjects...)
				out.runtimeExecutable = out.runtimeExecutable || analysis.runtimeExecutable
				out.requestMethodUnknown = out.requestMethodUnknown || analysis.requestMethodUnknown
				out.requestMethodDynamic = out.requestMethodDynamic || analysis.requestMethodDynamic
				out.shellProgramUnknown = out.shellProgramUnknown || analysis.shellProgramUnknown
			}
		case "WebFetch":
			if args.Method != "" {
				out.subjects = append(out.subjects, "method: '"+args.Method+"'")
			}
		}
	}
	return out
}

func shellSubjects(program string) []string {
	return analyzeShell(program).subjects
}

// globWord reports whether the unquoted text of a word holds a pathname
// pattern (*, ? or a bracket expression), which the shell replaces with
// matching file names when the command runs. A lone "[" is the test command.
func globWord(word *syntax.Word) bool {
	var text strings.Builder
	for _, part := range word.Parts {
		if lit, ok := part.(*syntax.Lit); ok {
			text.WriteString(lit.Value)
		} else {
			text.WriteByte('x') // Quoted text is literal, also inside brackets.
		}
	}
	value := text.String()
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\':
			i++
		case '*', '?':
			return true
		case '[':
			if i+2 < len(value) && strings.IndexByte(value[i+2:], ']') >= 0 {
				return true
			}
		}
	}
	return false
}

// Upper bounds on the work spent analysing one tool call. Reaching any of them
// marks the program as not inspectable rather than as clean.
const (
	maxAnalysisPrograms = 4096
	maxAnalysisBytes    = 8 << 20
	maxAnalysisSubjects = 1 << 16
)

func analyzeShell(program string) operationAnalysis {
	var out operationAnalysis
	programs, totalBytes := 0, 0
	// Nested literal shell programs are finite pieces of the original input.
	// Walk them iteratively rather than silently losing policy coverage at an
	// arbitrary recursion depth. Parsing performs no shell execution/expansion.
	// Every queued program counts against the budget, so derived programs
	// are bounded before they are parsed, not after.
	var pending []string
	enqueue := func(p string) {
		programs++
		totalBytes += len(p)
		if programs > maxAnalysisPrograms || totalBytes > maxAnalysisBytes {
			out.shellProgramUnknown = true
			return
		}
		pending = append(pending, p)
	}
	enqueue(program)
	for len(pending) > 0 {
		program := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		file, err := syntax.NewParser().Parse(strings.NewReader(program), "")
		if err != nil {
			// Missing/unsupported syntax is not evidence that restrictions do
			// not apply, including fragments sent to an interactive session.
			out.shellProgramUnknown = true
			continue
		}
		inherited := make(map[*syntax.Stmt][]*syntax.Redirect)
		pipeInputs := make(map[*syntax.Stmt]literalPipeInput)
		pipeOutputs := make(map[*syntax.Stmt]literalPipeInput)
		syntax.Walk(file, func(node syntax.Node) bool {
			stmt, ok := node.(*syntax.Stmt)
			if !ok {
				return true
			}
			if len(out.subjects) > maxAnalysisSubjects {
				out.shellProgramUnknown = true
				return false
			}
			redirs := append(append([]*syntax.Redirect(nil), inherited[stmt]...), stmt.Redirs...)
			// A single child can inherit the same untouched explicit stdin, even
			// in an async group. Do not assume where a shared stream is positioned
			// after earlier commands.
			var children []*syntax.Stmt
			switch cmd := stmt.Cmd.(type) {
			case *syntax.Block:
				children = cmd.Stmts
			case *syntax.Subshell:
				children = cmd.Stmts
			}
			if len(children) == 1 {
				inherited[children[0]] = redirs
				pipeInputs[children[0]] = pipeInputs[stmt]
			}
			if cmd, ok := stmt.Cmd.(*syntax.BinaryCmd); ok && (cmd.Op == syntax.Pipe || cmd.Op == syntax.PipeAll) {
				inherited[cmd.X] = redirs
				pipeInputs[cmd.X] = pipeInputs[stmt]
				// Pipe creation replaces the producer's inherited stdout, but
				// retains its inherited input (e.g. cat inside a redirected group).
				program, known := literalPipelineOutput(cmd.X, literalInputRedirects(redirs), pipeOutputs)
				// Retained pipe output counts against the budget as well.
				totalBytes += len(program)
				if totalBytes > maxAnalysisBytes {
					out.shellProgramUnknown = true
					return false
				}
				pipeInputs[cmd.Y] = literalPipeInput{program: program, known: known}
			}
			call, ok := stmt.Cmd.(*syntax.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			name, ok := staticWord(call.Args[0])
			if !ok {
				out.runtimeExecutable = true
				return true
			}
			words := []string{name}
			knownWords := []bool{true}
			for _, word := range call.Args[1:] {
				value, known := staticWord(word)
				if !known {
					value = "<dynamic>"
					if len(word.Parts) > 0 {
						if lit, ok := word.Parts[0].(*syntax.Lit); ok {
							if key, _, assignment := strings.Cut(lit.Value, "="); assignment && syntax.ValidName(key) {
								value = key + "=<dynamic>"
							}
						}
					}
				}
				words = append(words, value)
				knownWords = append(knownWords, known)
			}
			selected := words
			for len(selected) > 0 {
				tail, wrapper := wrappedCommand(path.Base(selected[0]), selected[1:])
				if !wrapper {
					break
				}
				selected = tail
			}
			// A command word with an unquoted pathname pattern names whatever
			// file matches when the shell runs it, so it is not a fixed program.
			if len(selected) > 0 && (!knownWords[len(words)-len(selected)] || globWord(call.Args[len(words)-len(selected)])) {
				out.runtimeExecutable = true
			}
			if len(selected) > 0 {
				offset := len(words) - len(selected)
				out.requestMethodUnknown = out.requestMethodUnknown || requestMethodNeedsReview(selected, knownWords[offset:], call.Args[offset:])
				out.requestMethodDynamic = out.requestMethodDynamic || requestMethodOpen(selected, knownWords[offset:], call.Args[offset:], true)
				input := pipeInputs[stmt]
				_, stdinKnown := literalShellStdinWithInput(redirs, input.program, input.known)
				out.shellProgramUnknown = out.shellProgramUnknown || shellProgramNeedsReview(selected, knownWords[offset:], call.Args[offset:], stdinKnown)
				builtinProgram, unknown := builtinShellProgram(selected, knownWords[offset:], call.Args[offset:])
				out.shellProgramUnknown = out.shellProgramUnknown || unknown
				if builtinProgram != "" {
					enqueue(builtinProgram)
				}
				deferred, deferredUnknown := deferredShellPrograms(selected, knownWords[offset:], call.Args[offset:])
				out.shellProgramUnknown = out.shellProgramUnknown || deferredUnknown
				for _, p := range deferred {
					enqueue(p)
				}
			}
			subjects, nested := commandSubjects(words)
			if shellReadsStdin(words) {
				input := pipeInputs[stmt]
				nested, _ = literalShellStdinWithInput(redirs, input.program, input.known)
			}
			out.subjects = append(out.subjects, subjects...)
			if nested != "" {
				enqueue(nested)
			}
			return true // Nested command substitutions are actual executable calls too.
		})
	}
	return out
}

func shellReadsStdin(words []string) bool {
	for len(words) > 0 {
		tail, wrapper := wrappedCommand(path.Base(words[0]), words[1:])
		if !wrapper {
			break
		}
		words = tail
	}
	if len(words) == 0 {
		return false
	}
	name := path.Base(words[0])
	if name != "bash" && name != "sh" && name != "dash" && name != "ksh" {
		return false
	}
	_, stdin := shellProgramInput(name, words[1:])
	return stdin
}

// Only the final direct stdin redirect supplies a literal program. Other file
// descriptors and output redirects are data; files/descriptor copies are unknown.
func literalShellStdin(redirs []*syntax.Redirect) (string, bool) {
	return literalShellStdinWithInput(redirs, "", false)
}

func literalShellStdinWithInput(redirs []*syntax.Redirect, program string, known bool) (string, bool) {
	for _, redir := range redirs {
		if redir.N != nil && redir.N.Value != "0" {
			continue
		}
		switch redir.Op {
		case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn:
			program, known = "", false
		case syntax.WordHdoc:
			program, known = staticWord(redir.Word)
		case syntax.Hdoc, syntax.DashHdoc:
			program, known = "", redir.Hdoc != nil
			if redir.Hdoc != nil {
				for _, part := range redir.Hdoc.Parts {
					lit, ok := part.(*syntax.Lit)
					if !ok {
						known = false
						break
					}
					program += lit.Value
				}
			}
			if redir.Op == syntax.DashHdoc {
				lines := strings.Split(program, "\n")
				for n := range lines {
					lines[n] = strings.TrimLeft(lines[n], "\t")
				}
				program = strings.Join(lines, "\n")
			}
		}
	}
	if !known {
		return "", false
	}
	return program, true
}

func commandSubjects(words []string) ([]string, string) {
	if len(words) == 0 {
		return nil, ""
	}
	for len(words) > 0 {
		tail, wrapper := wrappedCommand(path.Base(words[0]), words[1:])
		if !wrapper {
			break
		}
		words = tail // Each recognized wrapper consumes at least its own name.
	}
	if len(words) == 0 {
		return nil, ""
	}
	name := requestProgramName(words[0])
	if name == "sh" || name == "bash" || name == "dash" || name == "ksh" {
		if program, ok := shellCommandString(name, words[1:]); ok {
			return nil, program
		}
		return nil, ""
	}
	switch name {
	case "rm", "mkfs", "dd", "shutdown", "reboot", "halt", "poweroff", "init", "kill", "killall", "shred", "wipe", "iptables", "nft", "curl", "wget":
	default:
		if !strings.HasPrefix(name, "mkfs.") {
			return nil, ""
		}
	}
	words = append([]string{name}, words[1:]...)
	var out []string
	if name != "curl" && name != "wget" {
		out = append(out, strings.Join(words, " "))
	}
	// Canonical method markers avoid raw-JSON quoting and distance limits.
	for n := 1; n < len(words); n++ {
		arg := words[n]
		if arg == "--" {
			break
		}
		// Values of these options are data, not another request option.
		if requestValueOption(name, arg) && n+1 < len(words) {
			n++
			continue
		}
		var method string
		if name == "curl" {
			switch {
			case arg == "-X" || arg == "--request":
				if n+1 < len(words) {
					n++
					method = words[n]
				}
			case strings.HasPrefix(arg, "--request="):
				method = strings.TrimPrefix(arg, "--request=")
			case strings.HasPrefix(arg, "-X"):
				method = strings.TrimPrefix(arg, "-X")
			case strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--"):
				for k := 1; k < len(arg); k++ {
					if arg[k] == 'X' {
						method = arg[k+1:]
						if method == "" && n+1 < len(words) {
							n++
							method = words[n]
						}
						break
					}
					if !strings.ContainsRune("sSvfkLligNIOJaq", rune(arg[k])) {
						break
					}
				}
			}
		} else if name == "wget" {
			if arg == "--method" && n+1 < len(words) {
				n++
				method = words[n]
			}
			if strings.HasPrefix(arg, "--method=") {
				method = strings.TrimPrefix(arg, "--method=")
			}
		}
		if method != "" {
			if name == "curl" {
				out = append(out, "curl -X "+method)
			}
			if name == "wget" {
				out = append(out, "wget --method="+method)
			}
			out = append(out, "method: '"+method+"'")
		}
	}
	return out, ""
}

func curlValueOption(arg string) bool {
	switch arg {
	case "-H", "--header", "-d", "--data", "--data-raw", "--data-binary", "--data-urlencode", "--json", "-F", "--form", "--form-string", "-o", "--output", "-u", "--user", "-A", "--user-agent", "-e", "--referer", "-b", "--cookie", "-c", "--cookie-jar", "-x", "--proxy", "--url", "--resolve", "--connect-to", "-w", "--write-out", "-T", "--upload-file":
		return true
	}
	return false
}

// hasBraceExpansion reports whether the unquoted text of a word contains a
// brace expression with a comma or a ".." range, which a shell would expand
// into several fields. Quoted parts and backslash-escaped braces are literal,
// and a single-element form such as {value} is not an expansion. Detection is
// linear and never generates the expansion.
func hasBraceExpansion(word *syntax.Word) bool {
	var text strings.Builder
	for _, part := range word.Parts {
		if lit, ok := part.(*syntax.Lit); ok {
			text.WriteString(lit.Value)
		} else {
			text.WriteByte('x') // Quoted text carries no brace syntax.
		}
	}
	value := text.String()
	var separators []bool
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\\':
			i++
		case '{':
			separators = append(separators, false)
		case ',':
			if n := len(separators); n > 0 {
				separators[n-1] = true
			}
		case '.':
			if n := len(separators); n > 0 && i+1 < len(value) && value[i+1] == '.' {
				separators[n-1] = true
			}
		case '}':
			if n := len(separators); n > 0 {
				if separators[n-1] {
					return true
				}
				separators = separators[:n-1]
			}
		}
	}
	return false
}

// Only quote/escape removal is permitted. No environment, filesystem globbing,
// parameter expansion, arithmetic or command substitution can run here.
func staticWord(word *syntax.Word) (string, bool) {
	known := true
	if len(word.Parts) > 0 {
		if lit, ok := word.Parts[0].(*syntax.Lit); ok && strings.HasPrefix(lit.Value, "~") {
			return "", false // Do not resolve homes or consult the user database.
		}
	}
	syntax.Walk(word, func(node syntax.Node) bool {
		switch node.(type) {
		case nil, *syntax.Word, *syntax.Lit, *syntax.SglQuoted, *syntax.DblQuoted:
			return true
		default:
			known = false
			return false
		}
	})
	if !known || hasBraceExpansion(word) {
		return "", false
	}
	values, err := expand.Fields(&expand.Config{Env: expand.ListEnviron()}, word)
	if err != nil || len(values) != 1 {
		return "", false
	}
	return values[0], true
}
