package server

import (
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/Philocyber/counterweight/intercept"
	"mvdan.cc/sh/v3/syntax"
)

// Track possible parameter data through syntactic variable definitions. This
// deliberately merges scopes/branches and does not erase earlier definitions:
// a later literal assignment is not proof that every runtime path overwrites it.
// It is not a classifier for arbitrary programs or runtime command aliases.
//
// The second result reports whether a node Bash evaluates as arithmetic or as a
// variable reference is influenced; there a bare name is a reference too. The
// third reports an influenced value stored in a variable where eval or trap
// text or a declaration that is not fixed may already have run.
func shellTemplateParameterFlow(file *syntax.File, containsParameter func(syntax.Node) bool) (func(syntax.Node) bool, func(syntax.Node) bool, bool) {
	definitions := map[string][]syntax.Node{}
	owners := map[syntax.Node]string{}
	// This sentinel cannot be a parsed Bash function name. It owns the
	// positional parameters of a child shell, apart from the outer script's.
	const childTainted = "\x00child-tainted"
	functions := map[string]bool{childTainted: true}
	literalShellPrograms := map[*syntax.Word]bool{}
	// Words a recognized invocation runs or sources are parsed as programs even
	// without an expansion in them. programTexts are the ones known to be the
	// whole program of a child shell: its command string or its stdin.
	programWords, programTexts := map[*syntax.Word]bool{}, map[*syntax.Word]bool{}
	// Words a child shell receives next to its program become its $0, $1, ...
	childArguments := map[*syntax.Word][]*syntax.Word{}
	// Programs started by xargs also receive its input as arguments.
	inputArguments := map[*syntax.Word]bool{}
	tainted := map[string]bool{}
	// Bash writes these without an assignment, from an input stream or the
	// previous command; nothing proves that data is independent of a parameter.
	seeded, allTainted := containsParameter(file), false
	if seeded {
		tainted["BASH_REMATCH"], tainted["_"] = true, true
	}
	var stdinFeeds []syntax.Node
	stdinFed := false
	var calls []*syntax.CallExpr
	var ownerStack []string
	// Where the outermost loop or function around a command starts, or the
	// command itself: nothing before that point can run after the command.
	starts := map[syntax.Node]uint{}
	var regionStack []uint
	// Builtins and loops that fill a variable from input or from the
	// positional parameters.
	var writes []syntax.Node
	bindChild := func(call *syntax.CallExpr, programs []*syntax.Word, protected []*syntax.Word) {
		fixed := map[*syntax.Word]bool{}
		for _, word := range protected {
			fixed[word] = true
		}
		for _, program := range programs {
			literalShellPrograms[program] = true
			for _, argument := range call.Args {
				if !fixed[argument] && argument != program {
					childArguments[program] = append(childArguments[program], argument)
				}
			}
		}
	}
	for _, program := range intercept.ShellTemplateStdinPrograms(file) {
		words, _ := intercept.ShellTemplateProgramWords(program.Call)
		for _, word := range words {
			if identity, fixed := intercept.ShellTemplateLiteralWord(word); fixed && shellTemplateChildShell(identity) {
				var programs []*syntax.Word
				for _, source := range program.Sources {
					if text, ok := source.(*syntax.Word); ok {
						programs = append(programs, text)
						programTexts[text] = true
					}
				}
				bindChild(program.Call, programs, words)
				break
			}
		}
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil {
			ownerStack = ownerStack[:len(ownerStack)-1]
			regionStack = regionStack[:len(regionStack)-1]
			return true
		}
		region := node.Pos().Offset() + 1
		if len(regionStack) != 0 && regionStack[len(regionStack)-1] != 0 {
			region = regionStack[len(regionStack)-1]
		} else {
			switch node.(type) {
			case *syntax.FuncDecl, *syntax.WhileClause, *syntax.ForClause:
			default:
				region = 0
			}
		}
		regionStack = append(regionStack, region)
		owner := ""
		if len(ownerStack) != 0 {
			owner = ownerStack[len(ownerStack)-1]
		}
		if function, ok := node.(*syntax.FuncDecl); ok {
			owner = function.Name.Value
			functions[owner] = false
		}
		owners[node] = owner
		ownerStack = append(ownerStack, owner)
		switch value := node.(type) {
		case *syntax.DeclClause:
			starts[value] = value.Pos().Offset()
			if region != 0 {
				starts[value] = region - 1
			}
		case *syntax.Stmt:
			for _, redirect := range value.Redirs {
				switch redirect.Op {
				case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn, syntax.WordHdoc:
					stdinFeeds = append(stdinFeeds, redirect.Word)
				case syntax.Hdoc, syntax.DashHdoc:
					if redirect.Hdoc != nil {
						stdinFeeds = append(stdinFeeds, redirect.Hdoc)
					}
				}
			}
			if pipe, ok := value.Cmd.(*syntax.BinaryCmd); ok && (pipe.Op == syntax.Pipe || pipe.Op == syntax.PipeAll) {
				stdinFeeds = append(stdinFeeds, pipe.X)
			}
		case *syntax.CallExpr:
			calls = append(calls, value)
			starts[value] = value.Pos().Offset()
			if region != 0 {
				starts[value] = region - 1
			}
			if target, ok := shellTemplatePrintfVariable(value); ok {
				definitions[target] = append(definitions[target], value)
			}
			if names, written := intercept.ShellTemplateWrittenVariables(value); written && seeded {
				writes = append(writes, value)
				allTainted = allTainted || names == nil
				for _, name := range names {
					tainted[name] = true
				}
			}
			words, _ := intercept.ShellTemplateProgramWords(value)
			for _, word := range words {
				programWords[word] = true
			}
			if len(value.Args) != 0 {
				name, known := intercept.ShellTemplateLiteralWord(value.Args[0])
				if known && path.Base(name) != "eval" {
					childShell, command := false, false
					var programs []*syntax.Word
					for _, word := range words {
						identity, fixed := intercept.ShellTemplateLiteralWord(word)
						if childShell {
							programs = append(programs, word)
							// A fixed -c makes the last of these words the program.
							command = command || fixed && len(identity) > 1 && identity[0] == '-' && identity[1] != '-' && strings.Contains(identity, "c")
						}
						childShell = childShell || fixed && shellTemplateChildShell(identity)
					}
					bindChild(value, programs, words)
					if command {
						programTexts[programs[len(programs)-1]] = true
					}
					if path.Base(name) == "xargs" {
						for _, program := range programs {
							inputArguments[program] = true
						}
					}
				}
			}
		case *syntax.Assign:
			if value.Name != nil {
				definitions[value.Name.Value] = append(definitions[value.Name.Value], value)
			}
		case *syntax.WordIter:
			if value.Name != nil {
				for _, word := range value.Items {
					definitions[value.Name.Value] = append(definitions[value.Name.Value], word)
				}
				if len(value.Items) == 0 && seeded {
					// "for name; do" iterates the positional parameters.
					tainted[value.Name.Value] = true
					writes = append(writes, value)
				}
			}
		case *syntax.ParamExp:
			// ${name:=word} and ${name=word} assign without an assignment node.
			if word := shellTemplateDefaultAssignment(value); word != nil {
				definitions[value.Param.Value] = append(definitions[value.Param.Value], word)
			}
		}
		return true
	})
	var influenced func(syntax.Node) bool
	var arithmeticTainted func(syntax.Node, int, string) bool
	var referencesTainted func(syntax.Node, int, string) bool
	// programAssignments lists what reparsed program text assigns and from
	// which of its nodes, as the walk above does for the template. written are
	// filled by a builtin from its input, iterated take the positional
	// parameters in turn, and unknown reports a builtin whose targets are not
	// fixed words.
	programAssignments := func(program *syntax.File) (assigned map[string][]syntax.Node, written, iterated []string, unknown bool) {
		assigned = map[string][]syntax.Node{}
		syntax.Walk(program, func(node syntax.Node) bool {
			switch value := node.(type) {
			case *syntax.CallExpr:
				if target, ok := shellTemplatePrintfVariable(value); ok {
					assigned[target] = append(assigned[target], value)
				}
				if names, filled := intercept.ShellTemplateWrittenVariables(value); filled {
					unknown = unknown || names == nil
					written = append(written, names...)
				}
			case *syntax.Assign:
				if value.Name != nil {
					assigned[value.Name.Value] = append(assigned[value.Name.Value], value)
				}
			case *syntax.WordIter:
				if value.Name != nil {
					for _, word := range value.Items {
						assigned[value.Name.Value] = append(assigned[value.Name.Value], word)
					}
					if len(value.Items) == 0 {
						iterated = append(iterated, value.Name.Value)
					}
				}
			case *syntax.ParamExp:
				if word := shellTemplateDefaultAssignment(value); word != nil {
					assigned[value.Param.Value] = append(assigned[value.Param.Value], word)
				}
			}
			return true
		})
		return assigned, written, iterated, unknown
	}
	// Arithmetic evaluates the text a variable holds as an expression of its
	// own, so fixed text that names an influenced variable carries the
	// influence to wherever it is evaluated, through any number of variables.
	// scopes are the assignments that can be in force: the template's, those
	// in fixed text this shell runs itself, and a child program's while it is
	// read. A variable that is only used as data is unaffected: none of this
	// is consulted outside arithmetic.
	evaluatedText := map[string][]syntax.Node{}
	for word := range programWords {
		if text, known := intercept.ShellTemplateLiteralWord(word); known && !literalShellPrograms[word] {
			if nested, err := syntax.NewParser().Parse(strings.NewReader(text), ""); err == nil {
				assigned, _, _, _ := programAssignments(nested)
				for name, sources := range assigned {
					evaluatedText[name] = append(evaluatedText[name], sources...)
				}
			}
		}
	}
	// Text an eval or a trap runs is read as parsed, also when variables that
	// only ever hold fixed text spell it. opaque are the commands whose text
	// is not fixed, and the declarations that give an attribute to a name that
	// is not: once one has run, any variable can carry any attribute.
	evaluatedFiles := map[*syntax.Word]*syntax.File{}
	// A callback's last command receives each input line as an operand; its
	// command is refused as a whole when that input is influenced.
	inputWords := map[*syntax.Word]bool{}
	var opaque []syntax.Node
	for _, program := range intercept.ShellTemplateEvaluatedPrograms(file) {
		if call, ok := program.Call.(*syntax.CallExpr); ok && program.Input {
			for _, word := range call.Args {
				inputWords[word] = true
			}
		}
		if program.File == nil {
			opaque = append(opaque, program.Call)
			continue
		}
		for _, word := range program.Words {
			evaluatedFiles[word] = program.File
		}
		assigned, written, _, unknown := programAssignments(program.File)
		for name, sources := range assigned {
			evaluatedText[name] = append(evaluatedText[name], sources...)
		}
		if seeded {
			allTainted = allTainted || unknown
			for _, name := range written {
				tainted[name] = true
			}
		}
	}
	scopes := []map[string][]syntax.Node{definitions, evaluatedText}
	// Fixed words a child program can receive as positional parameters.
	var operands []syntax.Node
	// The function whose positional parameters fixed text reads when this shell
	// runs it itself, as eval and trap do; nil inside a child program.
	var evaluatedOwner *string
	var arithmeticText func(syntax.Node, map[string]bool) bool
	arithmeticNames := func(text string, seen map[string]bool) bool {
		for _, name := range shellTemplateNames(text) {
			if allTainted || tainted[name] {
				return true
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			for _, scope := range scopes {
				for _, source := range scope[name] {
					if arithmeticText(source, seen) {
						return true
					}
				}
			}
		}
		return false
	}
	// A positional parameter holds the fixed text of an argument the same way:
	// of a call to the function it belongs to, of set, or of a child program.
	positionalText := func(parameter *syntax.ParamExp, seen map[string]bool) bool {
		if parameter.Param == nil || strings.Trim(parameter.Param.Value, "0123456789@*") != "" {
			return false
		}
		function, parsed := owners[parameter]
		if !parsed && evaluatedOwner != nil {
			function, parsed = *evaluatedOwner, true
		}
		key := "\x00" + function
		if !parsed {
			key = "\x00\x00"
		}
		if seen[key] {
			return false
		}
		seen[key] = true
		if !parsed {
			for _, operand := range operands {
				if arithmeticText(operand, seen) {
					return true
				}
			}
			return false
		}
		for _, call := range calls {
			if len(call.Args) < 2 {
				continue
			}
			name, known := intercept.ShellTemplateLiteralWord(call.Args[0])
			if !known || name != function && (name != "set" || owners[call] != function) {
				continue
			}
			for _, argument := range call.Args[1:] {
				if arithmeticText(argument, seen) {
					return true
				}
			}
		}
		return false
	}
	arithmeticText = func(node syntax.Node, seen map[string]bool) bool {
		found := false
		syntax.Walk(node, func(child syntax.Node) bool {
			switch value := child.(type) {
			case *syntax.ParamExp:
				if value.Length {
					return false
				}
				found = found || positionalText(value, seen)
			case *syntax.Lit:
				found = found || arithmeticNames(value.Value, seen)
			case *syntax.SglQuoted:
				found = found || arithmeticNames(value.Value, seen)
			}
			return !found
		})
		return found
	}
	// childVariables taints what a child program assigns from its arguments,
	// from tainted inherited variables or from an input stream, and returns
	// those names. A child's variables end with it, so the caller drops them,
	// and the scope added here, once that program has been read. unknown
	// reports a builtin whose targets are not fixed words.
	childVariables := func(program *syntax.File, depth int, owner string) (scoped []string, unknown bool) {
		taint := func(name string) {
			if !tainted[name] {
				tainted[name] = true
				scoped = append(scoped, name)
			}
		}
		assigned, written, iterated, unknown := programAssignments(program)
		scopes = append(scopes, assigned)
		for _, name := range written {
			taint(name)
		}
		for _, name := range iterated {
			if functions[owner] {
				taint(name)
			}
		}
		for changed := true; changed; {
			changed = false
			for name, sources := range assigned {
				if tainted[name] {
					continue
				}
				for _, source := range sources {
					if referencesTainted(source, depth, owner) {
						taint(name)
						changed = true
						break
					}
				}
			}
		}
		return scoped, unknown
	}
	referencesTainted = func(node syntax.Node, depth int, owner string) bool {
		found := false
		syntax.Walk(node, func(child syntax.Node) bool {
			if found {
				return false
			}
			if parameter, ok := child.(*syntax.ParamExp); ok && parameter.Param != nil {
				name := parameter.Param.Value
				functionOwner := owner
				if parsedOwner, exists := owners[child]; exists {
					functionOwner = parsedOwner
				}
				position, positionErr := strconv.Atoi(name)
				// A child shell's $0 is the first word after its program.
				positional := name == "@" || name == "*" || positionErr == nil && (position > 0 || functionOwner == childTainted)
				if tainted[name] || allTainted || functions[functionOwner] && positional || parameter.Excl && (len(tainted) != 0 || functions[functionOwner]) {
					found = true
				}
			}
			word, ok := child.(*syntax.Word)
			positionalData := inputArguments[word] && stdinFed
			for _, argument := range childArguments[word] {
				positionalData = positionalData || influenced(argument)
			}
			if ok && (len(tainted) != 0 || functions[owner] || positionalData) {
				// A literal eval/-c/stdin program can contain expansions which the
				// outer parse correctly treats as text. Parse that fixed text only;
				// no supplied parameter bytes enter this second parser.
				resolved := evaluatedFiles[word]
				if text, known := intercept.ShellTemplateLiteralWord(word); resolved != nil || known && (strings.Contains(text, "$") || programWords[word] || programTexts[word]) {
					if depth >= 16 {
						found = true
					} else if nested, err := shellTemplateProgramFile(text, resolved); err == nil {
						nestedOwner := owner
						// Text this shell runs itself cannot rely on its own
						// declarations the way a child program can.
						evaluatedNodes := intercept.ShellTemplateEvaluatedText
						if literalShellPrograms[word] {
							evaluatedNodes = intercept.ShellTemplateEvaluatedNodes
						}
						// Arithmetic follows bare names, which are not references
						// the walk over the program would find.
						arithmetic, references, attributed := evaluatedNodes(nested)
						evaluated, dataOnly := false, false
						var scoped []string
						outerScopes, outerOperands, outerEvaluated := scopes, operands, evaluatedOwner
						if function, parsed := owners[word]; parsed && !literalShellPrograms[word] {
							// This shell runs the text in the function the word is in.
							evaluatedOwner = &function
						}
						if literalShellPrograms[word] {
							evaluatedOwner = nil
							// A child shell owns its positional arguments. A fixed
							// program that only uses them and its inherited
							// variables as data is safe. If it contains another
							// program sink, conservatively retain influence through
							// any reference in its AST.
							programSink := len(intercept.ShellTemplateStdinSources(nested)) != 0
							positionalOwner := owner
							if positionalData {
								positionalOwner = childTainted
							}
							unknownTargets := false
							scoped, unknownTargets = childVariables(nested, depth+1, positionalOwner)
							operands = nil
							for _, argument := range childArguments[word] {
								operands = append(operands, argument)
							}
							syntax.Walk(nested, func(node syntax.Node) bool {
								if call, ok := node.(*syntax.CallExpr); ok && len(call.Args) > 1 {
									for _, argument := range call.Args[1:] {
										operands = append(operands, argument)
									}
								}
								return true
							})
							programSink = programSink || unknownTargets || len(attributed) != 0
							for _, name := range attributed {
								evaluated = evaluated || tainted[name]
							}
							for _, name := range scoped {
								evaluated = evaluated || shellTemplateOwnVariable(name)
							}
							for _, program := range intercept.ShellTemplateStdinPrograms(nested) {
								// It takes the stdin the child inherits as its program.
								evaluated = evaluated || stdinFed && len(program.Sources) == 0
							}
							for _, node := range references {
								programSink = programSink || referencesTainted(node, depth+1, positionalOwner)
							}
							for _, node := range intercept.ShellTemplateExpandedNodes(nested) {
								programSink = programSink || referencesTainted(node, depth+1, positionalOwner)
							}
							syntax.Walk(nested, func(node syntax.Node) bool {
								if call, ok := node.(*syntax.CallExpr); ok {
									words, unknown := intercept.ShellTemplateProgramWords(call)
									programSink = programSink || unknown || len(words) != 0
									variableWords, variableUnknown := intercept.ShellTemplateVariableWords(call)
									if variableUnknown && referencesTainted(call, depth+1, positionalOwner) {
										programSink = true
									}
									for _, target := range variableWords {
										if referencesTainted(target, depth+1, positionalOwner) {
											programSink = true
										}
									}
								}
								return true
							})
							// A substituted command can read the influenced stdin
							// the child inherits.
							evaluated = evaluated || programSink && stdinFed && shellTemplateCommandSubstitution(nested)
							nestedOwner, dataOnly = positionalOwner, !programSink
						}
						for _, node := range arithmetic {
							evaluated = evaluated || arithmeticTainted(node, depth+1, nestedOwner)
						}
						found = evaluated || !dataOnly && referencesTainted(nested, depth+1, nestedOwner)
						for _, name := range scoped {
							delete(tainted, name)
						}
						scopes, operands, evaluatedOwner = outerScopes, outerOperands, outerEvaluated
					} else if programTexts[word] {
						// A child shell may run what this grammar cannot read.
						found = true
					}
				}
			}
			found = found || ok && inputWords[word] && stdinFed
			return !found
		})
		return found
	}
	// Arithmetic evaluation follows a variable's value recursively, so a bare
	// name in an expression is a reference. A length is always a plain number.
	arithmeticTainted = func(node syntax.Node, depth int, owner string) bool {
		found := false
		syntax.Walk(node, func(child syntax.Node) bool {
			switch value := child.(type) {
			case *syntax.ParamExp:
				if value.Length {
					return false
				}
				found = found || referencesTainted(value, depth, owner) || positionalText(value, map[string]bool{})
			case *syntax.CmdSubst:
				// Offsets of a reparsed fixed program do not index the template.
				found = found || depth == 0 && influenced(value)
			case *syntax.Lit:
				found = found || arithmeticNames(value.Value, map[string]bool{})
			case *syntax.SglQuoted:
				found = found || arithmeticNames(value.Value, map[string]bool{})
			}
			return !found
		})
		return found || depth == 0 && containsParameter(node)
	}
	influenced = func(node syntax.Node) bool {
		if node == nil {
			return false
		}
		if containsParameter(node) {
			return true
		}
		if stdinFed && shellTemplateCommandSubstitution(node) {
			// The substituted command can read the influenced inherited stdin.
			return true
		}
		return referencesTainted(node, 0, owners[node])
	}
	for changed := true; changed; {
		changed = false
		for _, feed := range stdinFeeds {
			if !stdinFed && influenced(feed) {
				stdinFed, changed = true, true
			}
		}
		// Calls to parsed functions can deliver literal parameters to positional
		// variables. Keep that state local to the function, so a fixed bash -c
		// data program's $1 is not confused with the caller's parameter value.
		// Definitions/call sites are conservatively merged without assuming which
		// branch, recursive invocation or function redefinition executes.
		for _, call := range calls {
			if len(call.Args) == 0 {
				continue
			}
			name, known := intercept.ShellTemplateLiteralWord(call.Args[0])
			// set can replace positional data, and shift can move it. Never erase
			// influence on a later overwrite/shift: scopes and branches are merged.
			if known && name == "set" && !functions[owners[call]] {
				for _, argument := range call.Args[1:] {
					if influenced(argument) {
						functions[owners[call]] = true
						changed = true
						break
					}
				}
			}
			if current, exists := functions[name]; !known || !exists || current {
				continue
			}
			for _, argument := range call.Args[1:] {
				if influenced(argument) {
					functions[name] = true
					changed = true
					break
				}
			}
		}
		for name, sources := range definitions {
			if tainted[name] {
				continue
			}
			for _, source := range sources {
				if influenced(source) {
					tainted[name] = true
					changed = true
					break
				}
			}
		}
	}
	// Once text that is not fixed may have run, a variable can carry any
	// attribute, so an influenced value must not be stored in one, nor
	// appended to, and neither must fixed text that arithmetic would follow
	// to one. A store counts as earlier only where the order is certain:
	// outside any function, and before the outermost loop or function around
	// the command.
	unsettled := false
	for _, call := range opaque {
		later := func(node syntax.Node) bool { return owners[node] != "" || node.End().Offset() > starts[call] }
		for name, sources := range definitions {
			for _, source := range sources {
				assign, _ := source.(*syntax.Assign)
				unsettled = unsettled || later(source) && (influenced(source) || assign != nil && assign.Append && tainted[name])
				if assign != nil && assign.Value != nil && later(source) {
					unsettled = unsettled || arithmeticText(assign.Value, map[string]bool{})
				}
			}
		}
		for _, write := range writes {
			unsettled = unsettled || later(write) && (stdinFed || influenced(write))
		}
	}
	return influenced, func(node syntax.Node) bool {
		if name, ok := node.(*syntax.Lit); ok {
			// A builtin whose targets are not fixed words is not taken to write
			// one of the variables Bash defines itself.
			return tainted[name.Value] || allTainted && !shellTemplateOwnVariable(name.Value)
		}
		if indirect, ok := node.(*syntax.ParamExp); ok {
			// The reference an indirect expansion follows is the value of its
			// parameter, not whatever that reference then names.
			direct := *indirect
			direct.Excl = false
			return referencesTainted(&direct, 0, owners[node])
		}
		return arithmeticTainted(node, 0, owners[node])
	}, unsettled
}

// shellTemplateProgramFile returns program text as parsed: resolved when the
// text was already built from fixed parts, or text read by the grammar.
func shellTemplateProgramFile(text string, resolved *syntax.File) (*syntax.File, error) {
	if resolved != nil {
		return resolved, nil
	}
	return syntax.NewParser().Parse(strings.NewReader(text), "")
}

// shellTemplateOwnVariable reports whether the name is a variable Bash defines
// itself and evaluates whatever value it is given.
func shellTemplateOwnVariable(name string) bool {
	return slices.Contains(intercept.ShellTemplateIntegerVariables, name) || slices.Contains(intercept.ShellTemplateExpandedVariables, name)
}

// shellTemplateDefaultAssignment returns the word ${name:=word} or ${name=word}
// assigns to name, or nil for any other expansion.
func shellTemplateDefaultAssignment(parameter *syntax.ParamExp) *syntax.Word {
	if parameter.Param == nil || parameter.Exp == nil || parameter.Exp.Op != syntax.AssignUnset && parameter.Exp.Op != syntax.AssignUnsetOrNull {
		return nil
	}
	return parameter.Exp.Word
}

func shellTemplateChildShell(identity string) bool {
	switch path.Base(identity) {
	case "bash", "sh", "dash", "ksh":
		return true
	}
	return false
}

func shellTemplateCommandSubstitution(node syntax.Node) bool {
	found := false
	syntax.Walk(node, func(child syntax.Node) bool {
		if _, ok := child.(*syntax.CmdSubst); ok {
			found = true
		}
		return !found
	})
	return found
}

// shellTemplateNames returns the variable names arithmetic text can refer to.
func shellTemplateNames(text string) []string {
	var names []string
	for _, name := range strings.FieldsFunc(text, func(r rune) bool {
		return r != '_' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z')
	}) {
		if name[0] < '0' || name[0] > '9' {
			names = append(names, name)
		}
	}
	return names
}

// Bash printf -v writes a named variable without an assignment AST node. Keep
// the target fixed and merge array elements into their base variable. Dynamic
// targets and runtime aliases are separate unresolved channels.
func shellTemplatePrintfVariable(call *syntax.CallExpr) (string, bool) {
	if len(call.Args) < 2 {
		return "", false
	}
	offset := 0
	name, known := intercept.ShellTemplateLiteralWord(call.Args[offset])
	if known && name == "builtin" {
		offset++
		name, known = intercept.ShellTemplateLiteralWord(call.Args[offset])
	}
	if !known || name != "printf" || offset+1 >= len(call.Args) {
		return "", false
	}
	option, known := intercept.ShellTemplateLiteralWord(call.Args[offset+1])
	if !known || !strings.HasPrefix(option, "-v") {
		return "", false
	}
	target := strings.TrimPrefix(option, "-v")
	if target == "" {
		if offset+2 >= len(call.Args) {
			return "", false
		}
		target, known = intercept.ShellTemplateLiteralWord(call.Args[offset+2])
		if !known {
			return "", false
		}
	}
	if index := strings.IndexByte(target, '['); index >= 0 {
		target = target[:index]
	}
	return target, target != ""
}
