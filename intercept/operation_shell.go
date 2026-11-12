package intercept

import (
	"mvdan.cc/sh/v3/syntax"
	"path"
)

func shellProgramNeedsReview(words []string, known []bool, original []*syntax.Word, stdinKnown bool) bool {
	name := path.Base(words[0])
	if name != "bash" && name != "sh" && name != "dash" && name != "ksh" {
		return false
	}
	source := shellProgramSource(name, words[1:])
	// Unresolved option words can change execution mode and the script source.
	for n := 1; n <= source.index && n < len(words); n++ {
		if !known[n] || !singleRequestArgument(original[n]) {
			return true
		}
	}
	switch source.kind {
	case "none":
		// Before -c or --, a variable ostensibly naming a syntax-check file
		// could instead enable interactive mode, which ignores noexec.
		n := source.index + 1
		return !source.command && !source.stopped && n < len(words) && (!known[n] || !singleRequestArgument(original[n]))
	case "command":
		n := source.index + 1
		return !known[n] || !singleRequestArgument(original[n])
	case "stdin":
		return !stdinKnown
	case "file", "unknown":
		return true
	}
	return false
}
