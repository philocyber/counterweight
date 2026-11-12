// Package codesrc ingests source code for white-box (security code review)
// tasks: it parses and validates a GitHub repository reference and clones it
// into an isolated task working directory. It is the white-box counterpart of
// recontools (which only downloads release binaries): here the whole source
// tree is fetched so the review agent can Read/Grep/Glob over it locally.
//
// Trust boundary: the cloned tree is adversarial input (it may carry hostile
// git hooks, Makefiles or interpreter-hijacking files). This package only
// fetches; it never builds or runs anything from the repo. Execution, if any,
// stays behind the task's approval gate.
package codesrc

import (
	"strings"

	"github.com/Philocyber/counterweight/locale"
)

// Repo identifies a GitHub repository by owner and name, plus an optional
// git ref (branch, tag or commit) to check out.
type Repo struct {
	Owner string // e.g. "Philocyber"
	Name  string // e.g. "counterweight"
	Ref   string // optional branch/tag/commit; empty = default branch
}

// cloneHost is the only host this package clones from. It mirrors the
// recontools allowlist intent: GitHub only, HTTPS only. Kept as a private
// constant rather than a shared export because the trust boundary differs
// (arbitrary third-party org source, not Counterweight's own release assets).
const cloneHost = "github.com"

// Slug is "owner/name", the canonical short form.
func (r Repo) Slug() string { return r.Owner + "/" + r.Name }

// CloneURL is the fixed HTTPS clone URL. It is always built from validated
// owner/name, never from caller-supplied raw text, so it cannot be redirected
// to another host.
func (r Repo) CloneURL() string {
	return "https://" + cloneHost + "/" + r.Owner + "/" + r.Name + ".git"
}

// ParseRepo accepts the forms a user is likely to paste and returns a validated
// Repo. Accepted:
//
//	owner/name
//	github.com/owner/name
//	https://github.com/owner/name
//	https://github.com/owner/name.git
//	any of the above with a trailing /tree/<ref> or #<ref>
//
// It refuses any other host, embedded credentials, and owner/name/ref segments
// that are not safe git-argument tokens (so a value can never be mistaken for a
// command-line flag or inject path traversal).
func ParseRepo(raw string) (Repo, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Repo{}, locale.NewError("empty repository reference")
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return Repo{}, locale.NewError("repository reference must not contain whitespace")
	}

	// Split off an explicit #ref fragment before anything else.
	var ref string
	if i := strings.IndexByte(s, '#'); i >= 0 {
		ref = s[i+1:]
		s = s[:i]
	}

	// Strip scheme. Reject embedded credentials (user@ / user:pass@).
	for _, scheme := range []string{"https://", "http://", "git://", "ssh://", "git@"} {
		if strings.HasPrefix(strings.ToLower(s), scheme) {
			if scheme == "git@" || scheme == "ssh://" {
				return Repo{}, locale.NewError("only HTTPS GitHub references are accepted")
			}
			s = s[len(scheme):]
			break
		}
	}
	if strings.ContainsRune(s, '@') {
		return Repo{}, locale.NewError("repository reference must not contain embedded credentials")
	}

	// If a host is present, it must be github.com; then drop it.
	if h, rest, ok := strings.Cut(s, "/"); ok && strings.Contains(h, ".") {
		if strings.ToLower(h) != cloneHost {
			return Repo{}, locale.Errorf("only %s repositories are accepted; got host %q", cloneHost, h)
		}
		s = rest
	}

	// Pull a trailing /tree/<ref> (GitHub web URL form) if no #ref was given.
	if idx := strings.Index(s, "/tree/"); idx >= 0 {
		if ref == "" {
			ref = s[idx+len("/tree/"):]
		}
		s = s[:idx]
	}

	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")

	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" {
		return Repo{}, locale.NewError("repository reference must be owner/name")
	}
	if strings.ContainsRune(name, '/') {
		return Repo{}, locale.NewError("repository reference must be exactly owner/name")
	}

	if err := validSegment(owner, "owner"); err != nil {
		return Repo{}, err
	}
	if err := validSegment(name, "name"); err != nil {
		return Repo{}, err
	}
	if ref != "" {
		if err := validRef(ref); err != nil {
			return Repo{}, err
		}
	}
	return Repo{Owner: owner, Name: name, Ref: ref}, nil
}

// validSegment allows GitHub owner/name characters only, and never a leading
// '-' (which git would read as a flag).
func validSegment(seg, label string) error {
	if seg == "" {
		return locale.Errorf("empty %s", label)
	}
	if len(seg) > 100 {
		return locale.Errorf("%s too long", label)
	}
	if seg[0] == '-' || seg[0] == '.' {
		return locale.Errorf("%s must not start with %q", label, string(seg[0]))
	}
	for _, c := range seg {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return locale.Errorf("invalid character %q in %s", string(c), label)
		}
	}
	return nil
}

// validRef allows the characters git permits in a ref plus a 40-hex commit,
// and never a leading '-'. It is deliberately stricter than git itself.
func validRef(ref string) error {
	if len(ref) > 255 {
		return locale.NewError("ref too long")
	}
	if ref[0] == '-' {
		return locale.NewError("ref must not start with '-'")
	}
	if strings.Contains(ref, "..") {
		return locale.NewError("ref must not contain '..'")
	}
	for _, c := range ref {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' || c == '/') {
			return locale.Errorf("invalid character %q in ref", string(c))
		}
	}
	return nil
}
