package intercept

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// These are argument-taking data options, not method/configuration selectors.
// curl and wget differ: wget -H is a flag, whereas curl -H consumes a header.
func requestValueOption(name, arg string) bool {
	if name == "curl" {
		return curlValueOption(arg)
	}
	if name != "wget" {
		return false
	}
	switch arg {
	case "--header", "--body-data", "--body-file", "--post-data", "--post-file", "--user", "--password", "--http-user", "--http-password", "--proxy-user", "--proxy-password", "-o", "--output-file", "-a", "--append-output", "-O", "--output-document", "-P", "--directory-prefix", "-i", "--input-file", "-U", "--user-agent", "-T", "--timeout", "-t", "--tries", "-w", "--wait", "--referer", "-X", "--exclude-directories":
		return true
	}
	return false
}

func requestMethodNeedsReview(words []string, known []bool, original []*syntax.Word) bool {
	return requestMethodOpen(words, known, original, false)
}

// requestMethodOpen is the same check; with startupIgnored it disregards the
// client's own default startup file, leaving only what the call itself makes
// depend on run-time arguments or a configuration it names.
func requestMethodOpen(words []string, known []bool, original []*syntax.Word, startupIgnored bool) bool {
	name := requestProgramName(words[0])
	if name != "curl" && name != "wget" {
		return false
	}
	// Startup files are mutable inputs too. Do not inspect a local rc file and
	// assume it will stay unchanged until dispatch. Curl only disables default
	// config when this is its first argument; wget accepts --no-config as an option.
	configDisabled := startupIgnored || name == "curl" && len(words) > 1 && known[1] &&
		(words[1] == "-q" || words[1] == "--disable")
	for n := 1; n < len(words); n++ {
		arg := words[n]
		if known[n] && arg == "--" {
			return !configDisabled
		}
		if !singleRequestArgument(original[n]) {
			return true // Splitting/globbing can introduce additional options.
		}
		if !known[n] {
			if attachedRequestData(name, requestLiteralPrefix(original[n])) {
				continue
			}
			return true
		}
		if arg == "--config" || strings.HasPrefix(arg, "--config=") {
			return true // Do not read a mutable config file during policy matching.
		}
		if unresolvedMethodOption(name, arg) {
			return true
		}
		if name == "wget" && arg == "--no-config" {
			configDisabled = true
		}
		if name == "wget" && (arg == "--execute" || strings.HasPrefix(arg, "--execute=") || strings.HasPrefix(arg, "-e")) {
			return true
		}
		if name == "curl" && strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
			for k := 1; k < len(arg); k++ {
				if arg[k] == 'K' {
					return true
				}
				if !strings.ContainsRune("sSvfkLligNIOJaq", rune(arg[k])) {
					break // Remaining bytes may be the value of a short option.
				}
			}
		}
		if requestValueOption(name, arg) && n+1 < len(words) {
			n++
			if !singleRequestArgument(original[n]) {
				return true
			}
		}
	}
	return !configDisabled
}

// CLI option abbreviation and curl's own variable expansion are not shell
// expansion. A literal shell word can still select a generated HTTP method or
// mutable configuration; defer those forms to a person rather than guessing.
func unresolvedMethodOption(name, arg string) bool {
	if !strings.HasPrefix(arg, "--") {
		return false
	}
	option, _, _ := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
	if option == "" {
		return false
	}
	if name == "curl" {
		for _, sensitive := range []string{"request", "config", "expand-request", "expand-config"} {
			if strings.HasPrefix(sensitive, option) && option != "request" {
				return true
			}
		}
	} else {
		for _, sensitive := range []string{"method", "config", "execute"} {
			if strings.HasPrefix(sensitive, option) && option != "method" {
				return true
			}
		}
	}
	return false
}

// Native Windows names and explicit Windows paths select the same HTTP tools.
// Other executable names keep their existing command-analysis behavior.
func requestProgramName(program string) string {
	name := strings.TrimSuffix(strings.ToLower(path.Base(strings.ReplaceAll(program, `\`, "/"))), ".exe")
	if name == "curl" || name == "wget" {
		return name
	}
	return path.Base(program)
}

// A quoted scalar is one argv element. Unquoted expansion and quoted arrays
// cannot be assumed to preserve option boundaries. This performs no expansion.
func singleRequestArgument(word *syntax.Word) bool {
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(part.Value, "*?[") {
				return false
			}
		case *syntax.SglQuoted:
		case *syntax.DblQuoted:
			array := false
			syntax.Walk(part, func(node syntax.Node) bool {
				if p, ok := node.(*syntax.ParamExp); ok && (p.Param.Value == "@" || p.Index != nil) {
					array = true
				}
				return true
			})
			if array {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func requestLiteralPrefix(word *syntax.Word) string {
	if len(word.Parts) == 0 {
		return ""
	}
	part := word.Parts[0]
	if quoted, ok := part.(*syntax.DblQuoted); ok && len(quoted.Parts) > 0 {
		part = quoted.Parts[0]
	}
	if lit, ok := part.(*syntax.Lit); ok {
		return lit.Value
	}
	return ""
}

func attachedRequestData(name, prefix string) bool {
	if strings.HasPrefix(prefix, "--") {
		option, _, attached := strings.Cut(prefix, "=")
		return attached && requestValueOption(name, option)
	}
	return len(prefix) >= 2 && requestValueOption(name, prefix[:2])
}
