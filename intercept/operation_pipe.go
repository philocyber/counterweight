package intercept

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type literalPipeInput struct {
	program string
	known   bool
}

// Model only producers with fully literal output and simple cat passthrough.
// No subprocess, filesystem read, variable expansion or environment lookup runs.
func literalPipelineOutput(stmt *syntax.Stmt, inherited []*syntax.Redirect, cache map[*syntax.Stmt]literalPipeInput) (output string, known bool) {
	if prior, found := cache[stmt]; found {
		return prior.program, prior.known
	}
	defer func() { cache[stmt] = literalPipeInput{program: output, known: known} }()
	redirs := append(append([]*syntax.Redirect(nil), inherited...), stmt.Redirs...)
	for _, redir := range redirs {
		if redir.Op == syntax.RdrAll || redir.Op == syntax.AppAll {
			return "", false
		}
		switch redir.Op {
		case syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.DplOut:
			if redir.N == nil || redir.N.Value == "1" {
				return "", false // Stdout no longer supplies this pipe.
			}
		}
	}
	switch cmd := stmt.Cmd.(type) {
	case *syntax.Block:
		if len(cmd.Stmts) == 1 {
			return literalPipelineOutput(cmd.Stmts[0], redirs, cache)
		}
	case *syntax.Subshell:
		if len(cmd.Stmts) == 1 {
			return literalPipelineOutput(cmd.Stmts[0], redirs, cache)
		}
	case *syntax.BinaryCmd:
		if cmd.Op == syntax.Pipe || cmd.Op == syntax.PipeAll {
			call, ok := cmd.Y.Cmd.(*syntax.CallExpr)
			if !ok {
				return "", false
			}
			words, known := literalCallWords(call)
			if !known || len(words) != 1 || path.Base(words[0]) != "cat" {
				return "", false
			}
			// Apply cat's explicit stdin override after the upstream stream.
			input, known := literalPipelineOutput(cmd.X, literalInputRedirects(redirs), cache)
			input, known = literalShellStdinWithInput(cmd.Y.Redirs, input, known)
			for _, redir := range cmd.Y.Redirs {
				switch redir.Op {
				case syntax.RdrAll, syntax.AppAll, syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.DplOut:
					if redir.N == nil || redir.N.Value == "1" {
						return "", false
					}
				}
			}
			return input, known
		}
	case *syntax.CallExpr:
		words, known := literalCallWords(cmd)
		if !known || len(words) == 0 {
			return "", false
		}
		for len(words) > 0 {
			tail, wrapper := wrappedCommand(path.Base(words[0]), words[1:])
			if !wrapper {
				break
			}
			words = tail
		}
		if len(words) == 0 {
			return "", false
		}
		switch path.Base(words[0]) {
		case "printf":
			return literalPrintfOutput(words[1:])
		case "echo":
			// Escape modes depend on shell options. Keep only unambiguous data.
			for index, word := range words[1:] {
				if strings.ContainsRune(word, '\\') || index == 0 && strings.HasPrefix(word, "-") {
					return "", false
				}
			}
			return strings.Join(words[1:], " ") + "\n", true
		case "cat":
			if len(words) == 1 {
				return literalShellStdin(redirs)
			}
		}
	}
	return "", false
}

func literalInputRedirects(redirs []*syntax.Redirect) []*syntax.Redirect {
	var input []*syntax.Redirect
	for _, redir := range redirs {
		if redir.N != nil && redir.N.Value != "0" {
			continue
		}
		switch redir.Op {
		case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			input = append(input, redir)
		}
	}
	return input
}

func literalCallWords(call *syntax.CallExpr) ([]string, bool) {
	words := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		value, known := staticWord(word)
		if !known {
			return nil, false
		}
		words = append(words, value)
	}
	return words, true
}

func literalPrintfOutput(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	if args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", false
	}
	format := args[0]
	// Restrict formatting before allocating: no widths, numeric/locale formats,
	// %b escape-mode differences, variables or time-dependent specifications.
	for index := 0; index < len(format); index++ {
		switch format[index] {
		case '%':
			index++
			if index == len(format) || format[index] != '%' && format[index] != 's' {
				return "", false
			}
		case '\\':
			index++
			if index == len(format) || !strings.ContainsRune("\\nrtabfv", rune(format[index])) {
				return "", false
			}
		}
	}
	const limit = 1 << 20
	inputSize := 0
	for _, arg := range args {
		if len(arg) > limit-inputSize {
			return "", false
		}
		inputSize += len(arg)
	}
	remaining := args[1:]
	var output strings.Builder
	for {
		chunk, consumed, err := expand.Format(nil, format, remaining)
		if err != nil || len(chunk) > limit-output.Len() {
			return "", false
		}
		output.WriteString(chunk)
		if consumed == 0 || consumed == len(remaining) {
			return output.String(), true
		}
		remaining = remaining[consumed:]
	}
}
