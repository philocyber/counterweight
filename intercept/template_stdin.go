package intercept

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ShellTemplateStdinSources returns syntactic inputs which a recognized shell or Python
// may parse as a program. It tracks redirection order and pipeline/group inputs,
// without reading files or executing producers. It is not variable-flow analysis
// or an attestation of runtime aliases, functions or arbitrary interpreters.
func ShellTemplateStdinSources(file *syntax.File) []syntax.Node {
	var sources []syntax.Node
	for _, program := range ShellTemplateStdinPrograms(file) {
		sources = append(sources, program.Sources...)
	}
	return sources
}

// ShellTemplateStdinProgram pairs a stdin-program consumer with its inputs, so
// a caller can relate the consumer's own arguments to that program.
type ShellTemplateStdinProgram struct {
	Call    *syntax.CallExpr
	Sources []syntax.Node
}

// ShellTemplateStdinPrograms is ShellTemplateStdinSources grouped by consumer.
func ShellTemplateStdinPrograms(file *syntax.File) []ShellTemplateStdinProgram {
	inputs := make(map[*syntax.Stmt]map[string][]syntax.Node)
	pending := make(map[*syntax.Stmt][]syntax.Node)
	producers := make(map[*syntax.Stmt]*syntax.Stmt)
	var stack []syntax.Node
	var sources []ShellTemplateStdinProgram
	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if stmt, ok := node.(*syntax.Stmt); ok {
			fds := make(map[string][]syntax.Node)
			for i := len(stack) - 1; i >= 0; i-- {
				if parent, ok := stack[i].(*syntax.Stmt); ok {
					for fd, input := range inputs[parent] {
						fds[fd] = input
					}
					break
				}
			}
			if producer, piped := producers[stmt]; piped {
				// Walk visits the producer first, so its ordered descriptor
				// overrides are available before binding the consumer's stdin.
				fds["0"] = templateProducerSources(producer, inputs[producer])
			} else if input, overridden := pending[stmt]; overridden {
				fds["0"] = input
			}
			for _, redir := range stmt.Redirs {
				fd := "0"
				if redir.Op == syntax.DplOut || redir.Op == syntax.RdrOut || redir.Op == syntax.AppOut || redir.Op == syntax.ClbOut {
					fd = "1"
				}
				if redir.N != nil {
					fd = redir.N.Value
				}
				switch redir.Op {
				case syntax.DplIn, syntax.DplOut:
					var input []syntax.Node
					from, known := staticWord(redir.Word)
					if known {
						input = append(input, fds[strings.TrimSuffix(from, "-")]...)
					} else {
						// A generated selector can copy any inherited descriptor.
						for _, prior := range fds {
							input = append(input, prior...)
						}
					}
					fds[fd] = append(input, redir.Word)
					if known && len(from) > 1 && strings.HasSuffix(from, "-") {
						fds[strings.TrimSuffix(from, "-")] = nil
					}
				case syntax.RdrIn, syntax.RdrInOut, syntax.WordHdoc:
					fds[fd] = []syntax.Node{redir.Word}
				case syntax.RdrOut, syntax.AppOut, syntax.ClbOut:
					// An output-only file descriptor cannot supply shell input.
					fds[fd] = nil
				case syntax.RdrAll, syntax.AppAll:
					fds["1"], fds["2"] = nil, nil
				case syntax.Hdoc, syntax.DashHdoc:
					fds[fd] = nil
					if redir.Hdoc != nil {
						fds[fd] = []syntax.Node{redir.Hdoc}
					}
				}
			}
			inputs[stmt] = fds
			if pipe, ok := stmt.Cmd.(*syntax.BinaryCmd); ok && (pipe.Op == syntax.Pipe || pipe.Op == syntax.PipeAll) {
				pending[pipe.X] = fds["0"]
				producers[pipe.Y] = pipe.X
			}
			if call, ok := stmt.Cmd.(*syntax.CallExpr); ok {
				switch fd := templateShellInput(call); fd {
				case "":
				case "*":
					// A script operand filled in at run time can name any descriptor.
					program := ShellTemplateStdinProgram{Call: call}
					for _, input := range fds {
						program.Sources = append(program.Sources, input...)
					}
					sources = append(sources, program)
				default:
					sources = append(sources, ShellTemplateStdinProgram{Call: call, Sources: fds[fd]})
				}
			}
		}
		stack = append(stack, node)
		return true
	})
	return sources
}

func templateProducerSources(stmt *syntax.Stmt, fds map[string][]syntax.Node) []syntax.Node {
	if call, ok := stmt.Cmd.(*syntax.CallExpr); ok {
		words, known := literalCallWords(call)
		if known && len(words) > 0 {
			switch path.Base(words[0]) {
			case "printf", "echo":
				// Literal output does not consume inherited stdin.
				return []syntax.Node{call}
			case "cat":
				if len(words) == 1 {
					return append([]syntax.Node{call}, fds["0"]...)
				}
			}
		}
	}
	// An opaque producer can consume any readable inherited descriptor.
	sources := []syntax.Node{stmt}
	for _, input := range fds {
		sources = append(sources, input...)
	}
	return sources
}

// templateShellInput names the descriptor a recognized interpreter may read
// its program from: "" for none, "*" when the template cannot fix which one.
func templateShellInput(call *syntax.CallExpr) string {
	if len(call.Args) == 0 {
		return ""
	}
	words := make([]string, len(call.Args))
	for i, word := range call.Args {
		value, known := staticWord(word)
		if !known {
			value = "<dynamic>"
		}
		words[i] = value
	}
	return ShellTemplateWordsInput(words)
}

// ShellTemplateWordsInput is the same classification for original command-line
// words, with <dynamic> standing for a word that is not fixed text.
func ShellTemplateWordsInput(words []string) string {
	for len(words) > 0 {
		name := path.Base(words[0])
		tail, wrapper := wrappedCommand(name, words[1:])
		if !wrapper {
			break
		}
		if len(tail) == 0 {
			if name == "command" && len(words) > 1 && (words[1] == "-v" || words[1] == "-V") {
				return ""
			}
			// Unsupported wrapper options can choose a stdin interpreter.
			return "0"
		}
		words = tail
	}
	if len(words) == 0 {
		return ""
	}
	name := path.Base(words[0])
	if name == "<dynamic>" {
		// A command name selected at run time can be a stdin interpreter.
		return "0"
	}
	if name == "source" || name == "." {
		operands := words[1:]
		if len(operands) > 0 && operands[0] == "--" {
			operands = operands[1:]
		}
		if len(operands) == 0 {
			return ""
		}
		return templateAliasDescriptor(operands[0])
	}
	if !templateShellName(name) && !templatePythonName.MatchString(name) {
		// Behind a program that is not a listed wrapper only the command-string
		// form of an interpreter is recognized (templateCommandString), and
		// that form does not take its program from stdin. xargs with a
		// replace-string is the exception: its input becomes program text.
		if name == "xargs" {
			known := make([]bool, len(words))
			for i, word := range words {
				known[i] = word != "<dynamic>"
			}
			if templateXargsProgram(words[1:], known[1:]) {
				return "0"
			}
		}
		return ""
	}
	if templatePythonName.MatchString(name) {
		if !PythonTemplateReadsStdin(words[1:]) {
			return ""
		}
		for _, word := range words[1:] {
			if fd := templateAliasDescriptor(word); fd != "" && fd != "0" {
				return "*"
			}
		}
		return "0"
	}
	source := shellProgramSource(name, words[1:])
	if source.kind == "file" && source.index < len(words)-1 {
		return templateAliasDescriptor(words[1+source.index])
	}
	if source.kind == "stdin" || source.kind == "unknown" {
		return "0"
	}
	return ""
}

// templateAliasDescriptor maps a script operand to the descriptor it reads.
func templateAliasDescriptor(operand string) string {
	operand = templateOperandPath(operand)
	if !templateStdinAlias.MatchString(operand) {
		return ""
	}
	if operand == "/dev/stdin" {
		return "0"
	}
	if fd := path.Base(operand); strings.HasPrefix(operand, "/") && strings.Trim(fd, "0123456789") == "" {
		return fd
	}
	return "*"
}
