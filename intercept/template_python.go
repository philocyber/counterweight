package intercept

import (
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

var templatePythonName = regexp.MustCompile(`^python(?:[23](?:\.[0-9]+)?)?(?:\.exe)?$`)

// PythonTemplateReadsStdin classifies original CLI words, with <dynamic> used
// for an unresolved word. -i may read code even after a fixed -c/-m program.
// This does not model environment startup flags or arbitrary program behavior.
func PythonTemplateReadsStdin(args []string) bool {
	inspect := false
	for i := 0; i < len(args); i++ {
		word := args[i]
		if word == "<dynamic>" {
			return true
		}
		if word == "--" {
			return i+1 >= len(args) || args[i+1] == "-" || templateStdinAlias.MatchString(templateOperandPath(args[i+1])) || inspect
		}
		if word == "-" {
			return true
		}
		if !strings.HasPrefix(word, "-") {
			return inspect || templateStdinAlias.MatchString(templateOperandPath(word))
		}
		if strings.HasPrefix(word, "--") {
			if word != "--check-hash-based-pycs" || i+1 >= len(args) || args[i+1] == "<dynamic>" {
				return true
			}
			i++
			continue
		}
		for j := 1; j < len(word); j++ {
			switch word[j] {
			case 'i':
				inspect = true
			case 'c', 'm':
				return inspect
			case 'W', 'X':
				if j+1 == len(word) {
					i++
					if i >= len(args) || args[i] == "<dynamic>" {
						return true
					}
				}
				j = len(word)
			case 'b', 'B', 'd', 'E', 'I', 'O', 'P', 'q', 's', 'S', 'u', 'v', 'x':
			default:
				return true
			}
		}
	}
	return true
}

// Python stops option parsing at -c/-m/the script selector. Only the
// subsequent argv words are ordinary data. Classify original AST words;
// never parse parameter values or assume a dynamic option is harmless.
func pythonTemplateProgramWords(call *syntax.CallExpr, offset int, words []string, known []bool, protected []*syntax.Word) ([]*syntax.Word, bool) {
	for i := offset + 1; i < len(words); i++ {
		if !known[i] || !singleRequestArgument(call.Args[i]) {
			return nil, true
		}
		word := words[i]
		if word == "--" {
			end := i + 2
			if end > len(words) {
				end = len(words)
			}
			return append(protected, call.Args[offset:end]...), false
		}
		if word == "-" || !strings.HasPrefix(word, "-") {
			return append(protected, call.Args[offset:i+1]...), false
		}
		if strings.HasPrefix(word, "--") {
			if word != "--check-hash-based-pycs" || i+1 >= len(words) || !known[i+1] {
				return nil, true
			}
			i++
			continue
		}
		for j := 1; j < len(word); j++ {
			switch word[j] {
			case 'c', 'm':
				end := i + 1
				if j+1 == len(word) {
					end++
				}
				if end > len(words) {
					return nil, true
				}
				return append(protected, call.Args[offset:end]...), false
			case 'W', 'X':
				if j+1 == len(word) {
					i++
					if i >= len(words) || !known[i] || !singleRequestArgument(call.Args[i]) {
						return nil, true
					}
				}
				j = len(word)
			case 'b', 'B', 'd', 'E', 'i', 'I', 'O', 'P', 'q', 's', 'S', 'u', 'v', 'x':
			default:
				return nil, true
			}
		}
	}
	return append(protected, call.Args[offset:]...), false
}
