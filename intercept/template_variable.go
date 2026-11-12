package intercept

import (
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// A placeholder is spelled as literal text but filled in at run time, so the
// builtin that receives it can be given an option in its place.
var templatePlaceholder = regexp.MustCompile(`\{[^{}]+\}`)

// ShellTemplateVariableWords finds Bash builtin operands that name variables.
// Array subscripts in these names can execute arithmetic/command expansions even
// when the operand was quoted as a literal argument by the caller.
func ShellTemplateVariableWords(call *syntax.CallExpr) (protected []*syntax.Word, unknown bool) {
	if len(call.Args) == 0 {
		return nil, false
	}
	offset := 0
	name, fixed := staticWord(call.Args[offset])
	if !fixed {
		return nil, false
	}
	// command and builtin preserve builtin execution while bypassing function
	// lookup. Keep identity queries as data and refuse unknown wrapper options.
	for name == "command" || name == "builtin" {
		offset++
		for offset < len(call.Args) {
			option, known := staticWord(call.Args[offset])
			if !known {
				return nil, true
			}
			if option == "-v" || option == "-V" {
				return nil, false
			}
			if option == "--" {
				offset++
				break
			}
			if option == "-p" {
				offset++
				continue
			}
			if strings.HasPrefix(option, "-") {
				return nil, true
			}
			break
		}
		if offset >= len(call.Args) {
			return nil, false
		}
		name, fixed = staticWord(call.Args[offset])
		if !fixed {
			return nil, true
		}
	}
	args := call.Args[offset+1:]
	switch name {
	case "printf":
		if len(args) == 0 {
			return nil, false
		}
		option, known := staticWord(args[0])
		// A dynamic first operand can select -v instead of a format string.
		protected = append(protected, args[0])
		if !known {
			return protected, true
		}
		if option == "-v" {
			if len(args) < 2 {
				return protected, true
			}
			protected = append(protected, args[1])
		}
		return protected, false
	case "unset", "declare", "typeset", "local", "export", "readonly", "let":
		// Function/nameref flags are kept fixed as well. A literal name does not
		// depend on knowing which runtime array/nameref binding already exists.
		// The declaration builtins and let reach this call form only behind
		// command or builtin; their operands name or evaluate variables too.
		return args, false
	case "test", "[":
		for i := 0; i+1 < len(args); i++ {
			// A word that is not fixed text can be either operator as well.
			if option, known := staticWord(args[i]); !known || templatePlaceholder.MatchString(option) || option == "-v" || option == "-R" {
				protected = append(protected, args[i+1])
			}
		}
		return protected, false
	case "wait":
		// -p names a variable in the same word or in the next one. Until a
		// fixed operand or -- ends the options, a word that is not fixed text
		// can be that option with its variable attached.
		for i := 0; i < len(args); i++ {
			option, known := staticWord(args[i])
			if !known || templatePlaceholder.MatchString(option) {
				protected = append(protected, args[i])
				continue
			}
			if option == "--" || !strings.HasPrefix(option, "-") {
				break
			}
			if strings.HasSuffix(option, "p") && i+1 < len(args) {
				i++
				protected = append(protected, args[i])
			}
		}
		return protected, false
	case "getopts":
		if len(args) > 1 {
			protected = append(protected, args[1])
		}
		return protected, false
	case "read":
		for i := 0; i < len(args); i++ {
			option, known := staticWord(args[i])
			if !known {
				return append(protected, args[i:]...), true
			}
			if option == "--" {
				return append(protected, args[i:]...), false
			}
			if !strings.HasPrefix(option, "-") || option == "-" {
				return append(protected, args[i:]...), false
			}
			protected = append(protected, args[i])
			for flag := 1; flag < len(option); flag++ {
				switch option[flag] {
				case 'r', 's', 'e', 'E':
					continue
				case 'a', 'd', 'i', 'n', 'N', 'p', 't', 'u':
					// A fixed prompt/delimiter/default flag makes its attached
					// suffix data too. Unknown/dynamic flag spellings were refused
					// above; parameter bytes cannot change this fixed prefix.
					dataOption := option[flag] == 'd' || option[flag] == 'i' || option[flag] == 'p'
					if flag+1 < len(option) && dataOption {
						protected = protected[:len(protected)-1]
					}
					if flag+1 == len(option) {
						i++
						if i >= len(args) {
							return protected, true
						}
						if !dataOption {
							protected = append(protected, args[i])
						}
					}
					flag = len(option)
				default:
					return protected, true
				}
			}
		}
		return protected, false
	default:
		return nil, false
	}
}
