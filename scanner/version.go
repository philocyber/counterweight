package scanner

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	releaseRe = regexp.MustCompile(`^v?[0-9]`)
	hexRunRe  = regexp.MustCompile(`[0-9a-fA-F]{40}`)
)

// IsRelease reports whether an advisory "fixed" value looks like a release
// version: it starts with a digit (or v and a digit) and is not a commit hash.
// OSV data mixes both kinds in one list.
func IsRelease(v string) bool {
	v = strings.TrimSpace(v)
	return releaseRe.MatchString(v) && !hexRunRe.MatchString(v)
}

// numericParts reads the leading dotted numeric segments of a version, ignoring
// a leading v and anything after a pre-release or build marker. ok is false
// when no number can be read.
func numericParts(v string) (parts []int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+~_ "); i >= 0 {
		v = v[:i]
	}
	for _, seg := range strings.Split(v, ".") {
		n, err := strconv.Atoi(seg)
		if err != nil {
			break
		}
		parts = append(parts, n)
	}
	return parts, len(parts) > 0
}

// compareVersions orders two dotted numeric versions; ok is false when either
// cannot be read, so callers can fall back to listing.
func compareVersions(a, b string) (cmp int, ok bool) {
	pa, oka := numericParts(a)
	pb, okb := numericParts(b)
	if !oka || !okb {
		return 0, false
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// PickFixed reduces the fixed values of an advisory to release versions,
// de-duplicated. When the installed version is comparable it returns the lowest
// release above it; otherwise it lists at most three.
func PickFixed(installed string, fixed []string) string {
	var rel []string
	seen := map[string]bool{}
	for _, f := range fixed {
		f = strings.TrimSpace(f)
		if IsRelease(f) && !seen[f] {
			seen[f] = true
			rel = append(rel, f)
		}
	}
	best := ""
	for _, r := range rel {
		if c, ok := compareVersions(r, installed); !ok || c <= 0 {
			continue
		}
		if best == "" {
			best = r
		} else if c, ok := compareVersions(r, best); ok && c < 0 {
			best = r
		}
	}
	if best != "" {
		return best
	}
	if len(rel) > 3 {
		rel = rel[:3]
	}
	return strings.Join(rel, ", ")
}

// HighestFixed returns the highest of several fixed-version texts (each possibly
// a list), the version that clears every advisory of a package; the first value
// when they cannot be compared.
func HighestFixed(texts []string) string {
	best := ""
	for _, t := range texts {
		for _, f := range strings.Split(t, ",") {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if best == "" {
				best = f
			} else if c, ok := compareVersions(f, best); ok && c > 0 {
				best = f
			}
		}
	}
	return best
}
