package intercept

import (
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ShellTemplateEvaluatedNodes returns the nodes whose expanded text Bash
// evaluates as an arithmetic expression, and those it takes as a variable
// reference. An array subscript in such text runs its command substitutions
// even when the value was quoted as one literal word. attributed are variables
// declared so that every later assignment to them is an arithmetic evaluation
// or a reference. A parameter expansion among references is an indirect one:
// the value of its parameter is the reference.
func ShellTemplateEvaluatedNodes(file *syntax.File) (arithmetic, references []syntax.Node, attributed []string) {
	return templateEvaluatedNodes(file, true)
}

// ShellTemplateEvaluatedText is ShellTemplateEvaluatedNodes for fixed text the
// running shell evaluates itself, as eval does. Such text is not a program of
// its own: a declaration in it does not prove what an array already is, so
// every subscript in it is arithmetic.
func ShellTemplateEvaluatedText(file *syntax.File) (arithmetic, references []syntax.Node, attributed []string) {
	return templateEvaluatedNodes(file, false)
}

func templateEvaluatedNodes(file *syntax.File, standalone bool) (arithmetic, references []syntax.Node, attributed []string) {
	// Associative subscripts are string keys, not arithmetic. An array is known
	// to be associative only when a plain top-level statement declares it and
	// no word names it anywhere else than in later assignments and expansions:
	// a declaration that did not run or failed, a function's local, an unset
	// or an earlier array of that name each leave an indexed array behind.
	associative := map[string]bool{}
	options := func(decl *syntax.DeclClause) (flags string, fixed bool) {
		fixed = true
		for _, arg := range decl.Args {
			if !arg.Naked || arg.Name != nil || arg.Value == nil {
				continue
			}
			text, known := staticWord(arg.Value)
			if !known {
				fixed = false
			} else if strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
				flags += text
			}
		}
		return flags, fixed
	}
	declared := map[string]uint{}
	plain := map[*syntax.Lit]bool{}
	for _, stmt := range file.Stmts {
		decl, ok := stmt.Cmd.(*syntax.DeclClause)
		if !ok || !standalone || stmt.Negated || stmt.Background || stmt.Coprocess || decl.Variant.Value != "declare" && decl.Variant.Value != "typeset" {
			continue
		}
		// -p, -f and -F only print, and +A is not a declaration.
		if flags, fixed := options(decl); fixed && strings.Contains(flags, "A") && !strings.ContainsAny(flags, "+pfF") {
			for _, arg := range decl.Args {
				if arg.Name != nil && !slices.Contains(templateIndexedVariables, arg.Name.Value) {
					if _, earlier := declared[arg.Name.Value]; !earlier {
						declared[arg.Name.Value] = stmt.End().Offset()
					}
					plain[arg.Name] = true
				}
			}
		}
	}
	// Variables whose assigned value Bash evaluates as an arithmetic expression,
	// in which a bare name is followed to that variable's value.
	integer := map[string]bool{}
	for _, name := range ShellTemplateIntegerVariables {
		integer[name] = true
	}
	if standalone {
		// Fixed text an eval or a trap runs declares variables for the whole
		// file: a trap can run between any two commands.
		for _, program := range ShellTemplateEvaluatedPrograms(file) {
			if program.File != nil {
				_, _, names := templateEvaluatedNodes(program.File, false)
				for _, name := range names {
					integer[name] = true
					attributed = append(attributed, name)
				}
			}
		}
	}
	declarations := map[*syntax.Assign]bool{}
	// A word of fixed text names a variable when it is that name, alone or
	// followed by an assignment or a subscript.
	named := func(text string) {
		for _, word := range strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(" \t\n;|&()<>'\"`", r) }) {
			if end := strings.IndexAny(word, "=+["); end >= 0 {
				word = word[:end]
			}
			delete(declared, word)
		}
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch value := node.(type) {
		case *syntax.DeclClause:
			flags, fixed := options(value)
			for _, arg := range value.Args {
				declarations[arg] = true
				if arg.Name != nil && (!fixed || strings.ContainsAny(flags, "in") || value.Variant.Value == "nameref") {
					integer[arg.Name.Value] = true
				}
			}
		case *syntax.CallExpr:
			if flags, fixed, operands, ok := templateDeclarationCall(value); ok && (!fixed || strings.ContainsAny(flags, "in")) {
				for _, operand := range operands {
					integer[templateDeclaredName(operand)] = true
				}
			}
		case *syntax.Assign:
			if end, known := declared[templateAssignedName(value)]; known && !declarations[value] && value.Pos().Offset() >= end {
				plain[value.Name] = true
			}
		case *syntax.ParamExp:
			if value.Param != nil {
				if end, known := declared[value.Param.Value]; known && value.Pos().Offset() >= end {
					plain[value.Param] = true
				}
			}
		case *syntax.Lit:
			if !plain[value] {
				named(value.Value)
			}
		case *syntax.SglQuoted:
			named(value.Value)
		}
		return true
	})
	for name := range declared {
		associative[name] = true
	}
	add := func(expressions ...syntax.ArithmExpr) {
		for _, expression := range expressions {
			if expression != nil {
				arithmetic = append(arithmetic, expression)
			}
		}
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch value := node.(type) {
		case *syntax.ArithmExp:
			add(value.X)
		case *syntax.ArithmCmd:
			add(value.X)
		case *syntax.LetClause:
			add(value.Exprs...)
		case *syntax.CStyleLoop:
			add(value.Init, value.Cond, value.Post)
		case *syntax.ParamExp:
			if value.Param == nil || !associative[value.Param.Value] {
				add(value.Index)
			}
			if value.Slice != nil {
				add(value.Slice.Offset, value.Slice.Length)
			}
			// ${!name} expands the variable that the value of name refers to,
			// subscript included. ${!prefix*} and ${!name[@]} only list names.
			if index, _ := value.Index.(*syntax.Word); value.Excl && value.Names == 0 && (index == nil || index.Lit() != "@" && index.Lit() != "*") {
				references = append(references, value)
			}
		case *syntax.Assign:
			if value.Name != nil && integer[value.Name.Value] {
				if value.Value != nil {
					arithmetic = append(arithmetic, value.Value)
				}
				if value.Array != nil {
					arithmetic = append(arithmetic, value.Array)
				}
			}
			if value.Name != nil && associative[value.Name.Value] {
				break
			}
			add(value.Index)
			if value.Array != nil {
				for _, element := range value.Array.Elems {
					add(element.Index)
				}
			}
		case *syntax.BinaryTest:
			switch value.Op {
			case syntax.TsEql, syntax.TsNeq, syntax.TsLeq, syntax.TsGeq, syntax.TsLss, syntax.TsGtr:
				arithmetic = append(arithmetic, value.X, value.Y)
			}
		case *syntax.UnaryTest:
			if value.Op == syntax.TsVarSet || value.Op == syntax.TsRefVar {
				references = append(references, value.X)
				if word, ok := value.X.(*syntax.Word); ok {
					add(templateSubscript(word))
				}
			}
		case *syntax.DeclClause:
			flags, fixed := options(value)
			// Integer and nameref attributes evaluate the assigned value. An
			// option word produced at run time can select either of them.
			evaluated := !fixed || strings.ContainsAny(flags, "in") || value.Variant.Value == "nameref"
			for _, arg := range value.Args {
				if arg.Naked && arg.Name == nil && arg.Value != nil {
					// A bare operand names a variable, subscript included.
					references = append(references, arg.Value)
					add(templateSubscript(arg.Value))
				}
				if !evaluated {
					continue
				}
				if arg.Name != nil {
					attributed = append(attributed, arg.Name.Value)
				}
				if !arg.Naked && arg.Value != nil {
					arithmetic = append(arithmetic, arg.Value)
				}
				if arg.Array != nil {
					arithmetic = append(arithmetic, arg.Array)
				}
			}
		case *syntax.CallExpr:
			// The same attributes set by a declaration builtin that is reached as
			// an ordinary command. Its operands are assignments Bash evaluates.
			if flags, fixed, operands, ok := templateDeclarationCall(value); ok && (!fixed || strings.ContainsAny(flags, "in")) {
				for _, operand := range operands {
					attributed = append(attributed, templateDeclaredName(operand))
					arithmetic = append(arithmetic, operand)
				}
			}
			// A fixed subscript in a variable name a builtin receives is
			// evaluated like one the parser sees.
			names, _ := ShellTemplateVariableWords(value)
			for _, name := range names {
				add(templateSubscript(name))
			}
			// let is a keyword only when spelled plainly; behind command or
			// builtin its operands are the same arithmetic expressions.
			if args := templateBuiltinArgs(value); len(args) > 1 {
				if name, _ := staticWord(args[0]); name == "let" {
					for _, expression := range args[1:] {
						arithmetic = append(arithmetic, expression)
					}
				}
			}
		}
		return true
	})
	return arithmetic, references, attributed
}

// templateIndexedVariables are indexed arrays Bash creates itself. Declaring
// one associative fails, so its subscripts stay arithmetic.
var templateIndexedVariables = []string{"BASH_ARGC", "BASH_ARGV", "BASH_LINENO", "BASH_REMATCH", "BASH_SOURCE", "BASH_VERSINFO", "COMP_WORDS", "COPROC", "DIRSTACK", "FUNCNAME", "GROUPS", "PIPESTATUS"}

// ShellTemplateIntegerVariables are created by Bash itself with the integer
// attribute, or are counters it may read the same way in another release:
// assigning to one is an arithmetic evaluation without any declaration.
var ShellTemplateIntegerVariables = []string{"BASHPID", "HISTCMD", "LINENO", "OPTIND", "RANDOM", "SECONDS", "SRANDOM"}

// ShellTemplateExpandedVariables hold text that Bash, or a shell started with
// them in its environment, expands again with its command substitutions: the
// prompt strings, the prompt command and the startup file names.
var ShellTemplateExpandedVariables = []string{"BASH_ENV", "ENV", "PROMPT_COMMAND", "PS0", "PS1", "PS2", "PS4"}

// ShellTemplateExpandedNodes returns the parameter expansions whose value Bash
// expands a second time, command substitutions included: ${name@P} reads the
// value as a prompt string. The other @ operators only quote or change case;
// an operator that is not one of those is taken to expand.
func ShellTemplateExpandedNodes(file *syntax.File) []syntax.Node {
	var expanded []syntax.Node
	syntax.Walk(file, func(node syntax.Node) bool {
		if parameter, ok := node.(*syntax.ParamExp); ok && parameter.Exp != nil && parameter.Exp.Op == syntax.OtherParamOps {
			operator, fixed := "", parameter.Exp.Word == nil
			if !fixed {
				operator, fixed = staticWord(parameter.Exp.Word)
			}
			if !fixed || len(operator) != 1 || !strings.Contains("QEAaUuLKk", operator) {
				expanded = append(expanded, parameter)
			}
		}
		return true
	})
	return expanded
}

func templateAssignedName(assign *syntax.Assign) string {
	if assign.Name == nil {
		return ""
	}
	return assign.Name.Value
}

// templateDeclaredName returns the variable a declaration operand such as
// name, name=value or name[index]+=value declares, or "" when the operand
// does not start with fixed text.
func templateDeclaredName(operand *syntax.Word) string {
	text, known := staticWord(operand)
	if !known {
		if lit, ok := operand.Parts[0].(*syntax.Lit); ok {
			text = lit.Value
		}
	}
	if end := strings.IndexAny(text, "=+["); end >= 0 {
		text = text[:end]
	}
	return text
}

// templateDeclarationCall reads a declaration builtin that the parser sees as
// an ordinary command: behind command or builtin, quoted, escaped or after an
// assignment prefix. fixed is false when a word that can be an option is not
// fixed text; a word that starts with other fixed text is an operand.
func templateDeclarationCall(call *syntax.CallExpr) (flags string, fixed bool, operands []*syntax.Word, ok bool) {
	args := templateBuiltinArgs(call)
	if len(args) == 0 {
		return "", false, nil, false
	}
	switch name, _ := staticWord(args[0]); name {
	case "declare", "typeset", "local", "export", "readonly":
	default:
		return "", false, nil, false
	}
	fixed = true
	for _, word := range args[1:] {
		text, known := staticWord(word)
		if !known {
			if lit, ok := word.Parts[0].(*syntax.Lit); ok {
				text = lit.Value
			}
		}
		if text == "" || strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
			fixed = fixed && known
			flags += text
		} else {
			operands = append(operands, word)
		}
	}
	return flags, fixed, operands, true
}

// ShellTemplateWrittenVariables names the variables a builtin fills from its
// input stream or its arguments instead of an assignment. written is false
// for any other call. A nil list means the targets are not fixed words.
func ShellTemplateWrittenVariables(call *syntax.CallExpr) (names []string, written bool) {
	words := make([]string, 0, len(call.Args))
	fixed := true
	for _, word := range call.Args {
		value, known := staticWord(word)
		fixed = fixed && known
		words = append(words, value)
	}
	for len(words) > 1 && (words[0] == "command" || words[0] == "builtin") {
		words = words[1:]
		for len(words) > 1 && (words[0] == "-p" || words[0] == "--") {
			words = words[1:]
		}
	}
	if len(words) == 0 {
		return nil, false
	}
	values, fallback := "", ""
	switch words[0] {
	case "read":
		values, fallback = "adinNptu", "REPLY"
	case "mapfile", "readarray":
		values, fallback = "dnOsuCc", "MAPFILE"
	case "getopts":
		if !fixed || len(words) < 3 {
			return nil, true
		}
		return []string{"OPTARG", words[2]}, true
	default:
		return nil, false
	}
	if !fixed {
		return nil, true
	}
	operands := words[1:]
	for len(operands) > 0 && strings.HasPrefix(operands[0], "-") && operands[0] != "-" {
		option := operands[0]
		operands = operands[1:]
		if option == "--" {
			break
		}
		for flag := 1; flag < len(option); flag++ {
			if !strings.ContainsRune(values, rune(option[flag])) {
				continue
			}
			value := option[flag+1:]
			if value == "" {
				if len(operands) == 0 {
					return nil, true
				}
				value, operands = operands[0], operands[1:]
			}
			if words[0] == "read" && option[flag] == 'a' {
				names = append(names, value)
			}
			break
		}
	}
	names = append(names, operands...)
	if len(names) == 0 {
		names = []string{fallback}
	}
	for i, name := range names {
		// An element write taints its whole array.
		names[i], _, _ = strings.Cut(name, "[")
	}
	return names, true
}

// templateSubscript returns the subscript of a fixed word that names an array
// element, as name[subscript] or name[subscript]=value, for the arithmetic
// Bash evaluates it with. Any other word yields nil.
func templateSubscript(word *syntax.Word) syntax.ArithmExpr {
	text, known := staticWord(word)
	open, end := strings.IndexByte(text, '['), strings.LastIndexByte(text, ']')
	if !known || open <= 0 || end < open || strings.Trim(text[:open], "_0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return nil
	}
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{ValuePos: word.Pos(), ValueEnd: word.End(), Value: text[open+1 : end]}}}
}

// templateBuiltinArgs returns the words of a call from the builtin it runs,
// past any command or builtin in front of it.
func templateBuiltinArgs(call *syntax.CallExpr) []*syntax.Word {
	args := call.Args
	for len(args) > 1 {
		name, known := staticWord(args[0])
		if !known || name != "command" && name != "builtin" {
			break
		}
		args = args[1:]
		for len(args) > 1 {
			if option, known := staticWord(args[0]); !known || option != "-p" && option != "--" {
				break
			}
			args = args[1:]
		}
	}
	return args
}

// ShellTemplateEvaluatedProgram is text the running shell evaluates itself: the
// joined arguments of an eval, the action of a trap, or the callback of
// mapfile. Call is the command of the file that leads to it, also for text
// found inside other such text, and Words are the words that make it up. File
// is the parsed text, or nil when the text is not fixed or this grammar cannot
// read it: the shell may then declare or assign anything. Input marks a
// callback, whose last command also receives each line the command reads.
//
// A declaration that can give the integer or nameref attribute, and whose
// option or name is not a plain fixed word, is listed the same way: File is
// the declaration as it reads with those words filled in, or nil when one of
// them is not fixed, so that any variable may then carry the attribute.
type ShellTemplateEvaluatedProgram struct {
	Call  syntax.Node
	Words []*syntax.Word
	File  *syntax.File
	Input bool
	text  string
	// declared is the declaration itself, in the file or in other text.
	declared syntax.Node
}

// ShellTemplateEvaluatedPrograms lists that text for a file. Text is fixed
// when each part of it is literal, or a variable that one plain top-level
// assignment before the command sets to fixed text and that no other word of
// the file names: nothing can then have given the variable another value. No
// variable has a known value once the file writes to a name that is not fixed
// text, or runs text that is not fixed: either can change any variable.
func ShellTemplateEvaluatedPrograms(file *syntax.File) []ShellTemplateEvaluatedProgram {
	constants, split := templateConstants(file)
	programs := templateEvaluatedPrograms(file, constants, split)
	// Text spelled through variables can name, and so write, a variable that
	// no word of the file names; that variable has no known value either.
	for changed := true; changed; {
		changed = false
		for _, program := range programs {
			for _, name := range templateNamedWords(program.text) {
				// The same goes for IFS: unquoted values then split unpredictably.
				if name == "IFS" && split && program.File != nil {
					split, changed = false, true
				}
				if _, known := constants[name]; known && program.File != nil {
					delete(constants, name)
					changed = true
				}
			}
		}
		if changed {
			programs = templateEvaluatedPrograms(file, constants, split)
		}
	}
	// A declaration whose words were all filled in names what it writes.
	settled := map[syntax.Node]bool{}
	for _, program := range programs {
		if program.declared != nil && program.File != nil {
			settled[program.declared] = true
		}
	}
	unknown := templateIndirectWrite(file, settled)
	for _, program := range programs {
		unknown = unknown || program.File == nil || templateIndirectWrite(program.File, settled)
	}
	if unknown && len(constants) != 0 {
		return templateEvaluatedPrograms(file, nil, split)
	}
	return programs
}

func templateEvaluatedPrograms(file *syntax.File, constants map[string]templateConstant, split bool) []ShellTemplateEvaluatedProgram {
	var resolve func(word *syntax.Word, at uint, splits bool, depth int) (string, bool)
	// part returns the text of one quoted or literal part of a word.
	part := func(parts ...syntax.WordPart) (string, bool) {
		return staticWord(&syntax.Word{Parts: parts})
	}
	variable := func(parameter *syntax.ParamExp, at uint, depth int) (string, bool) {
		if parameter.Param == nil || parameter.Excl || parameter.Length || parameter.Width || parameter.Index != nil || parameter.Slice != nil || parameter.Repl != nil || parameter.Names != 0 || parameter.Exp != nil {
			return "", false
		}
		constant, known := constants[parameter.Param.Value]
		if !known || constant.end > at || depth >= 16 {
			return "", false
		}
		if constant.value == nil {
			return "", true
		}
		// An assignment neither splits nor globs what it assigns.
		return resolve(constant.value, constant.end, false, depth+1)
	}
	resolve = func(word *syntax.Word, at uint, splits bool, depth int) (string, bool) {
		// Braces are expanded over the whole word, before any of its parts.
		if splits && hasBraceExpansion(word) {
			return "", false
		}
		var text strings.Builder
		for _, piece := range word.Parts {
			value, fixed := "", false
			switch piece := piece.(type) {
			case *syntax.Lit:
				// An unquoted pattern becomes whatever file names it matches.
				value, fixed = part(piece)
				fixed = fixed && !(splits && strings.ContainsAny(piece.Value, "*?["))
			case *syntax.SglQuoted:
				value, fixed = part(piece)
			case *syntax.DblQuoted:
				fixed = !piece.Dollar
				for _, inner := range piece.Parts {
					if !fixed {
						break
					}
					quoted := ""
					switch inner := inner.(type) {
					case *syntax.Lit:
						quoted, fixed = part(&syntax.DblQuoted{Parts: []syntax.WordPart{inner}})
					case *syntax.ParamExp:
						quoted, fixed = variable(inner, at, depth)
					default:
						fixed = false
					}
					value += quoted
				}
			case *syntax.ParamExp:
				value, fixed = variable(piece, at, depth)
				if splits && fixed {
					// Unquoted, the value is split into words that eval joins
					// again with single spaces; a pattern in it is matched
					// against file names.
					fixed = split && !strings.ContainsAny(value, "*?[")
					value = templateSplitJoined(value)
				}
			}
			if !fixed {
				return "", false
			}
			text.WriteString(value)
		}
		return text.String(), true
	}
	var programs []ShellTemplateEvaluatedProgram
	var read func(scope *syntax.File, origin syntax.Node, depth int)
	read = func(scope *syntax.File, origin syntax.Node, depth int) {
		syntax.Walk(scope, func(node syntax.Node) bool {
			if variant, words, ok := templateDeclaration(node); ok {
				program := ShellTemplateEvaluatedProgram{Call: origin, declared: node}
				if origin == nil {
					program.Call = node
				}
				texts, fixed := []string{variant}, depth < 16
				// Options end at the first operand; until then a word that is
				// not fixed text can be the option that gives the attribute.
				flags, options, uncertain := "", true, variant == "nameref"
				for _, word := range words {
					text, known := resolve(word, program.Call.Pos().Offset(), true, 0)
					fixed = fixed && known
					texts = append(texts, text)
					plain, static := staticWord(word)
					switch {
					case !options:
					case static && plain != "--" && (strings.HasPrefix(plain, "-") || strings.HasPrefix(plain, "+")):
						flags += plain
					case static || templateFixedStart(word):
						options = false
					default:
						uncertain = true
					}
				}
				if fixed {
					program.text = strings.Join(texts, " ")
					if nested, err := syntax.NewParser().Parse(strings.NewReader(program.text), ""); err == nil {
						program.File = nested
					}
				}
				if program.File != nil || uncertain || strings.ContainsAny(flags, "in") {
					programs = append(programs, program)
				}
				return true
			}
			call, ok := node.(*syntax.CallExpr)
			if !ok {
				return true
			}
			words, operand, single, unreadable := templateEvaluatedWords(call)
			if len(words) == 0 {
				return true
			}
			program := ShellTemplateEvaluatedProgram{Call: origin, Words: words, Input: operand != nil}
			if origin == nil {
				program.Call = call
			}
			if operand != nil {
				program.Words = []*syntax.Word{operand}
			}
			texts, fixed := make([]string, len(words)), depth < 16 && !unreadable
			for i, word := range words {
				text, known := resolve(word, program.Call.Pos().Offset(), true, 0)
				// A trap action or a callback is one word; a value that splits
				// is not it.
				fixed = fixed && known && !(single && strings.ContainsAny(text, " \t\n") && templateUnquotedExpansion(word))
				texts[i] = text
			}
			if fixed {
				program.text = strings.Join(texts, " ")
				if nested, err := syntax.NewParser().Parse(strings.NewReader(program.text), ""); err == nil {
					program.File = nested
				}
			}
			programs = append(programs, program)
			if program.File != nil {
				read(program.File, program.Call, depth+1)
			}
			return true
		})
	}
	read(file, nil, 0)
	return programs
}

// templateEvaluatedWords returns the words of a call that the running shell
// evaluates as a program: every argument of eval, the action of a trap, or the
// callback of mapfile. An action or a callback is a single word. callback is
// the operand of the call that holds the callback, which the option can be
// attached to. unreadable reports a word that may or may not select one.
func templateEvaluatedWords(call *syntax.CallExpr) (words []*syntax.Word, callback *syntax.Word, single, unreadable bool) {
	args := templateBuiltinArgs(call)
	if len(args) < 2 {
		return nil, nil, false, false
	}
	name, _ := staticWord(args[0])
	operands := args[1:]
	if name == "mapfile" || name == "readarray" {
		for i := 0; i < len(operands); i++ {
			option, known := staticWord(operands[i])
			if !known {
				// An option that is not fixed text can be -C with its callback.
				return operands[i : i+1], operands[i], true, true
			}
			if option == "--" || option == "-" || !strings.HasPrefix(option, "-") {
				break
			}
			for flag := 1; flag < len(option); flag++ {
				if !strings.ContainsRune("dnOsuCc", rune(option[flag])) {
					continue
				}
				attached := option[flag+1:]
				if option[flag] != 'C' {
					if attached == "" {
						i++
					}
					break
				}
				if attached != "" {
					quoted := &syntax.SglQuoted{Left: operands[i].Pos(), Right: operands[i].End(), Value: attached}
					return []*syntax.Word{{Parts: []syntax.WordPart{quoted}}}, operands[i], true, false
				}
				if i+1 < len(operands) {
					return operands[i+1 : i+2], operands[i+1], true, false
				}
				break
			}
		}
		return nil, nil, false, false
	}
	if option, known := staticWord(operands[0]); known && option == "--" {
		operands = operands[1:]
	} else if known && name == "trap" && (option == "-l" || option == "-p" || option == "-lp" || option == "-pl") {
		return nil, nil, false, false
	}
	switch name {
	case "eval":
		return operands, nil, false, false
	case "trap":
		// A single operand, a lone - and a signal number reset conditions, and
		// an empty action ignores them.
		if len(operands) < 2 {
			return nil, nil, false, false
		}
		if first, known := staticWord(operands[0]); known && strings.Trim(first, "0123456789") == "" || known && first == "-" {
			return nil, nil, false, false
		}
		return operands[:1], nil, true, false
	}
	return nil, nil, false, false
}

// templateDeclaration reads a declare, typeset or local, as a keyword or behind
// command or builtin, that has a word the parser does not read as a plain
// option or name: one that is not fixed text, or a quoted name. words are its
// options and operands in order, with a name the parser did read standing for
// its whole assignment. export and readonly cannot give either attribute.
func templateDeclaration(node syntax.Node) (variant string, words []*syntax.Word, ok bool) {
	switch value := node.(type) {
	case *syntax.DeclClause:
		variant = value.Variant.Value
		for _, arg := range value.Args {
			switch {
			case arg.Name != nil:
				words = append(words, &syntax.Word{Parts: []syntax.WordPart{arg.Name}})
			case arg.Value != nil:
				words = append(words, arg.Value)
				text, fixed := staticWord(arg.Value)
				ok = ok || !fixed || !strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "+")
			}
		}
	case *syntax.CallExpr:
		if _, _, _, declaration := templateDeclarationCall(value); !declaration {
			return "", nil, false
		}
		args := templateBuiltinArgs(value)
		variant, _ = staticWord(args[0])
		words = args[1:]
		for _, word := range words {
			_, fixed := staticWord(word)
			ok = ok || !fixed
		}
	}
	if variant != "declare" && variant != "typeset" && variant != "local" && variant != "nameref" {
		return "", nil, false
	}
	return variant, words, ok
}

// templateFixedStart reports a word that begins with fixed text other than an
// option sign, so that whatever follows in it cannot make it an option.
func templateFixedStart(word *syntax.Word) bool {
	first := word.Parts[0]
	if quoted, ok := first.(*syntax.DblQuoted); ok && len(quoted.Parts) != 0 {
		first = quoted.Parts[0]
	}
	lit, ok := first.(*syntax.Lit)
	return ok && lit.Value != "" && !strings.HasPrefix(lit.Value, "-") && !strings.HasPrefix(lit.Value, "+")
}

// templateNamedWords returns the variables fixed text can name as something to
// write: each word of it, up to an assignment, a subscript or an arithmetic
// operator, and without the option printf attaches a name to.
func templateNamedWords(text string) []string {
	words := strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(" \t\n;|&()<>'\"`{},", r) })
	for i, word := range words {
		word = strings.TrimPrefix(word, "-v")
		if end := strings.IndexAny(word, "=+-[*/%<>!&|^?:"); end >= 0 {
			word = word[:end]
		}
		words[i] = word
	}
	return words
}

// templateIndirectWrite reports a write to a variable whose name is not fixed
// text: an operand of a builtin that assigns or declares, a nameref target, or
// the target of an assignment inside arithmetic. Such a write can reach any
// variable of the file. settled are declarations whose words are known after
// all, which therefore write the variables they name and no other.
func templateIndirectWrite(file *syntax.File, settled map[syntax.Node]bool) bool {
	// A word that starts with a fixed name and = or [ names that variable.
	named := func(word *syntax.Word) bool {
		if _, fixed := staticWord(word); fixed {
			return true
		}
		first := word.Parts[0]
		if quoted, ok := first.(*syntax.DblQuoted); ok && len(quoted.Parts) != 0 {
			first = quoted.Parts[0]
		}
		lit, ok := first.(*syntax.Lit)
		return ok && strings.ContainsAny(lit.Value, "=[") && !strings.HasPrefix(lit.Value, "-") && !strings.HasPrefix(lit.Value, "+")
	}
	// expanded reports an arithmetic assignment target that an expansion
	// spells, apart from a subscript.
	expanded := func(target syntax.ArithmExpr) bool {
		word, _ := target.(*syntax.Word)
		if word == nil {
			return false
		}
		parts := word.Parts
		if quoted, ok := parts[0].(*syntax.DblQuoted); ok && len(parts) == 1 {
			parts = quoted.Parts
		}
		for _, part := range parts {
			switch part := part.(type) {
			case *syntax.ParamExp:
				if part.Dollar.IsValid() {
					return true
				}
			case *syntax.CmdSubst:
				return true
			}
		}
		return false
	}
	indirect := false
	syntax.Walk(file, func(node syntax.Node) bool {
		switch value := node.(type) {
		case *syntax.BinaryArithm:
			switch value.Op {
			case syntax.Assgn, syntax.AddAssgn, syntax.SubAssgn, syntax.MulAssgn, syntax.QuoAssgn, syntax.RemAssgn, syntax.AndAssgn, syntax.OrAssgn, syntax.XorAssgn, syntax.ShlAssgn, syntax.ShrAssgn:
				indirect = indirect || expanded(value.X)
			}
		case *syntax.UnaryArithm:
			if value.Op == syntax.Inc || value.Op == syntax.Dec {
				indirect = indirect || expanded(value.X)
			}
		case *syntax.LetClause:
			for _, expression := range value.Exprs {
				if word, ok := expression.(*syntax.Word); ok {
					indirect = indirect || !named(word)
				}
			}
		case *syntax.DeclClause:
			if settled[value] {
				break
			}
			reference := value.Variant.Value == "nameref"
			for _, arg := range value.Args {
				if arg.Naked && arg.Name == nil && arg.Value != nil {
					text, _ := staticWord(arg.Value)
					indirect = indirect || !named(arg.Value)
					reference = reference || strings.HasPrefix(text, "-") && strings.Contains(text, "n")
				}
			}
			for _, arg := range value.Args {
				if arg.Name != nil && arg.Value != nil && reference {
					_, fixed := staticWord(arg.Value)
					indirect = indirect || !fixed
				}
			}
		case *syntax.CallExpr:
			args := templateBuiltinArgs(value)
			if len(args) < 2 || settled[value] {
				break
			}
			name, _ := staticWord(args[0])
			if name == "test" || name == "[" {
				// These only read the variables they name.
				break
			}
			if name == "mapfile" || name == "readarray" {
				// The array is the first operand after the options; an option
				// value, the callback among them, is not a name.
				operands := args[1:]
				for i := 0; i < len(operands); i++ {
					option, known := staticWord(operands[i])
					if !known {
						indirect = true
					}
					if !known || option == "--" || option == "-" || !strings.HasPrefix(option, "-") {
						break
					}
					for flag := 1; flag < len(option); flag++ {
						if strings.ContainsRune("dnOsuCc", rune(option[flag])) {
							if flag+1 == len(option) {
								i++
							}
							break
						}
					}
				}
			}
			words, unknown := ShellTemplateVariableWords(value)
			indirect = indirect || unknown
			for _, word := range words {
				// A printf format that starts with fixed text is not -v.
				format := name == "printf" && word == args[1]
				if lit, ok := word.Parts[0].(*syntax.Lit); format && ok && !strings.HasPrefix(lit.Value, "-") {
					continue
				}
				indirect = indirect || !named(word)
			}
			if flags, _, operands, ok := templateDeclarationCall(value); ok && strings.Contains(flags, "n") {
				for _, operand := range operands {
					_, fixed := staticWord(operand)
					indirect = indirect || !fixed
				}
			}
		}
		return !indirect
	})
	return indirect
}

// templateSplitJoined returns a value as it reads after the shell has split it
// into words and the words are joined with single spaces again.
func templateSplitJoined(value string) string {
	blank := func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }
	joined := strings.Join(strings.FieldsFunc(value, blank), " ")
	if joined == "" {
		// Blanks alone still separate the text on either side of them.
		if value != "" {
			joined = " "
		}
		return joined
	}
	if blank(rune(value[0])) {
		joined = " " + joined
	}
	if blank(rune(value[len(value)-1])) {
		joined += " "
	}
	return joined
}

func templateUnquotedExpansion(word *syntax.Word) bool {
	for _, piece := range word.Parts {
		if _, ok := piece.(*syntax.ParamExp); ok {
			return true
		}
	}
	return false
}

type templateConstant struct {
	value *syntax.Word
	end   uint
}

// templateConstants returns the variables whose value is known wherever the
// file reads them after end: one plain top-level statement assigns them, and
// no other word of the file names them, so no builtin, loop, function, eval
// text or later assignment can have written them. split is false when the
// file names IFS that way, so that unquoted values split unpredictably.
func templateConstants(file *syntax.File) (constants map[string]templateConstant, split bool) {
	constants = map[string]templateConstant{"IFS": {}}
	plain := map[*syntax.Lit]bool{}
	candidate := func(stmt *syntax.Stmt, assign *syntax.Assign) {
		if assign.Name == nil || assign.Naked || assign.Append || assign.Index != nil || assign.Array != nil {
			return
		}
		if _, earlier := constants[assign.Name.Value]; !earlier {
			constants[assign.Name.Value] = templateConstant{value: assign.Value, end: stmt.End().Offset()}
			plain[assign.Name] = true
		}
	}
	for _, stmt := range file.Stmts {
		if stmt.Negated || stmt.Background || stmt.Coprocess {
			continue
		}
		switch command := stmt.Cmd.(type) {
		case *syntax.CallExpr:
			if len(command.Args) == 0 {
				for _, assign := range command.Assigns {
					candidate(stmt, assign)
				}
			}
		case *syntax.DeclClause:
			// An option can change what is stored; local is not top-level.
			operands := command.Variant.Value != "local" && command.Variant.Value != "nameref"
			for _, arg := range command.Args {
				operands = operands && arg.Name != nil
			}
			if operands {
				for _, arg := range command.Args {
					candidate(stmt, arg)
				}
			}
		}
	}
	named := func(text string) {
		for _, name := range templateNamedWords(text) {
			delete(constants, name)
		}
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch value := node.(type) {
		case *syntax.ParamExp:
			// Reading a variable does not change it; ${name:=word} can.
			if value.Param != nil && (value.Exp == nil || value.Exp.Op != syntax.AssignUnset && value.Exp.Op != syntax.AssignUnsetOrNull) {
				plain[value.Param] = true
			}
		case *syntax.Lit:
			if !plain[value] {
				named(value.Value)
			}
		case *syntax.SglQuoted:
			named(value.Value)
		}
		return true
	})
	_, split = constants["IFS"]
	delete(constants, "IFS")
	return constants, split
}
