package report

import (
	"strconv"
	"strings"

	"github.com/Philocyber/counterweight/db"
)

// FindingLocation is one head-side file and line range a finding cites. End
// equals Start for a single line.
type FindingLocation struct {
	File       string
	Start, End int
}

// FindingLocations lists the source locations cited in a finding's evidence,
// report and summary, as the exports read them.
func FindingLocations(f *db.DBFinding) []FindingLocation {
	var out []FindingLocation
	for _, loc := range codeLocations(f) {
		for _, lines := range loc.Lines {
			a, b, _ := strings.Cut(lines, "-")
			start, err := strconv.Atoi(a)
			if err != nil || start < 1 {
				continue
			}
			end := start
			if n, err := strconv.Atoi(b); err == nil && n > start {
				end = n
			}
			out = append(out, FindingLocation{File: loc.File, Start: start, End: end})
		}
	}
	return out
}

// FindingLocationsIn is FindingLocations plus citations of the given repository paths
// (the files of a change). It reads `<path>:<line>` and `<path>:<start>-<end>` for any
// listed path whatever its extension or characters, relative or behind an absolute
// prefix such as the checkout directory. Behind a relative prefix it is another file:
// `cmd/main.go:3` does not cite a listed `main.go`. A path below root, the checkout
// directory when it is known, is read relative to it.
func FindingLocationsIn(f *db.DBFinding, paths []string, root string) []FindingLocation {
	out := FindingLocations(f)
	root = strings.TrimRight(strings.ReplaceAll(root, "\\", "/"), "/")
	if len(root) > 1 && root[1] == ':' {
		root = root[2:] // a drive letter is not part of the run of names read back from the text
	}
	for _, text := range []string{f.Evidence, f.Report, f.Summary} {
		for _, p := range paths {
			if p == "" {
				continue
			}
			needle := p + ":"
			for from := 0; from < len(text); {
				i := strings.Index(text[from:], needle)
				if i < 0 {
					break
				}
				i += from
				from = i + len(needle)
				below := ""
				if i > 0 && text[i-1] == '/' {
					var absolute bool
					if below, absolute = absolutePrefix(text[:i], root); !absolute {
						continue
					}
				} else if i > 0 && isPathChar(text[i-1]) {
					continue
				}
				start, rest := leadingInt(text[from:])
				if start < 1 {
					continue
				}
				end := start
				if len(rest) > 1 && rest[0] == '-' {
					if n, _ := leadingInt(rest[1:]); n > start {
						end = n
					}
				}
				out = append(out, FindingLocation{File: below + p, Start: start, End: end})
			}
		}
	}
	return out
}

// isPathChar reports whether b could continue a file name, so a listed path preceded by
// it is the tail of another name rather than a citation of the listed file.
func isPathChar(b byte) bool {
	return b == '_' || b == '.' || b == '@' || b == '-' || b == '+' || b == '~' || b >= 0x80 ||
		(b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// absolutePrefix reports whether the directories that end prefix start at a root: the
// run of names and slashes before a cited path begins with a slash. below is the part
// of them that lies under the checkout directory, when that is known and they do.
func absolutePrefix(prefix, root string) (below string, absolute bool) {
	i := len(prefix)
	for i > 0 && (isPathChar(prefix[i-1]) || prefix[i-1] == '/') {
		i--
	}
	if i == len(prefix) || prefix[i] != '/' {
		return "", false
	}
	if dir := prefix[i:]; root != "" && strings.HasPrefix(dir, root+"/") {
		return dir[len(root)+1:], true
	}
	return "", true
}

// leadingInt reads the decimal digits at the start of s; 0 when there are none or the value is huge.
func leadingInt(s string) (int, string) {
	n := 0
	i := 0
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		if i == 9 {
			return 0, s[i:]
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, s[i:]
}
