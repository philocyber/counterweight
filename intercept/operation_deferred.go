package intercept

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Trap actions are complete deferred programs; aliases are replacement fragments
// whose meaning also depends on later arguments and shell state. Never execute
// handlers or expand aliases during review.
func deferredShellPrograms(words []string, known []bool, original []*syntax.Word) ([]string, bool) {
	name := path.Base(words[0])
	if name != "trap" && name != "alias" {
		return nil, false
	}
	start := 1
	listing := false
	for start < len(words) {
		if !known[start] || !singleRequestArgument(original[start]) {
			return nil, true
		}
		arg := words[start]
		if arg == "--" {
			start++
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		flags := strings.TrimPrefix(arg, "-")
		for _, flag := range flags {
			if name == "alias" && flag == 'p' {
				continue
			}
			if name == "trap" && strings.ContainsRune("lpP", flag) {
				listing = true
				continue
			}
			return nil, true // Unsupported option grammar is not proof of inert data.
		}
		start++
		if listing {
			return nil, false
		}
	}
	if name == "trap" {
		if listing || len(words)-start < 2 {
			return nil, false
		}
		if !known[start] || !singleRequestArgument(original[start]) {
			return nil, true
		}
		action := words[start]
		if action == "" || action == "-" {
			return nil, false
		}
		if len(action) > 1<<20 {
			return nil, true
		}
		return []string{action}, false
	}
	var programs []string
	unknown, total := false, 0
	for n := start; n < len(words); n++ {
		if !known[n] || !singleRequestArgument(original[n]) {
			unknown = true
			continue
		}
		_, value, definition := strings.Cut(words[n], "=")
		if !definition {
			continue
		} // A literal name queries existing definitions.
		unknown = true // Even literal replacement text may combine with later argv.
		if len(value) > (1<<20)-total {
			return programs, true
		}
		total += len(value)
		if value != "" {
			programs = append(programs, value)
		}
	}
	return programs, unknown
}
