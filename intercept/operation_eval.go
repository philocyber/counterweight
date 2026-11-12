package intercept

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// eval reparses its arguments joined with spaces; source/dot execute file
// contents. Neither may be treated as inert argument data. No file is opened.
func builtinShellProgram(words []string, known []bool, original []*syntax.Word) (program string, unknown bool) {
	name := path.Base(words[0])
	if name == "source" || name == "." {
		return "", len(words) > 1
	}
	if name != "eval" {
		return "", false
	}
	start := 1
	if len(words) > 1 && known[1] && words[1] == "--" {
		start++
	} else if len(words) > 1 && known[1] && strings.HasPrefix(words[1], "-") {
		return "", false // Invalid eval options do not evaluate their arguments.
	}
	length := 0
	for n := start; n < len(words); n++ {
		if !known[n] || !singleRequestArgument(original[n]) {
			return "", true
		}
		if len(words[n])+1 > (1<<20)-length {
			return "", true // Bound generated-code analysis without guessing.
		}
		length += len(words[n]) + 1
	}
	return strings.Join(words[start:], " "), false
}
