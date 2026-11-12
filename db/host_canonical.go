package db

import (
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

// CanonicalHost returns the lowercase ASCII form of a host name without a
// trailing dot, mapped exactly as net/http maps it before dialing: a pure ASCII
// name is used as written, any other name goes through the IDNA lookup mapping
// (full-width letters, ideographic dots, soft hyphens and U-labels all become
// their A-label spelling). An error means the name cannot be dialed as written,
// so callers refuse it instead of treating it as unmatched.
func CanonicalHost(host string) (string, error) {
	ascii, err := hostASCII(host)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(ascii, "."), nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// canonicalURL rebuilds an absolute URL from its canonical host: lowercase
// scheme, default port dropped, an empty path written as "/", percent-encoding
// normalized, and no userinfo or fragment (neither is part of what a server is
// asked for). ok is false when the text is not an absolute URL with a host.
func canonicalURL(raw string) (canonical string, ok bool, err error) {
	u, perr := url.Parse(strings.TrimSpace(raw))
	if perr != nil || u.Hostname() == "" {
		return "", false, nil
	}
	host, err := CanonicalHost(u.Hostname())
	if err != nil {
		return "", true, err
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	path := (&url.URL{Path: u.Path}).EscapedPath()
	if path == "" {
		path = "/"
	}
	out := scheme + "://" + host + path
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out, true, nil
}

// firstUnconvertibleHost returns the first domain or URL host that cannot be
// converted to its canonical form.
func firstUnconvertibleHost(domains, urls []string) (string, bool) {
	for _, d := range domains {
		if d = strings.TrimSpace(d); d != "" {
			if _, err := CanonicalHost(d); err != nil {
				return d, true
			}
		}
	}
	for _, u := range urls {
		if _, _, err := canonicalURL(u); err != nil {
			return u, true
		}
	}
	return "", false
}

// hostASCII maps a host to lowercase ASCII like CanonicalHost but keeps any
// trailing dot, for partial matching where the dot is part of the pattern.
func hostASCII(host string) (string, error) {
	host = strings.TrimSpace(host)
	if !isASCII(host) {
		ascii, err := idna.Lookup.ToASCII(host)
		if err != nil {
			return "", err
		}
		host = ascii
	}
	return strings.ToLower(host), nil
}

// hostUnicode returns the lowercase Unicode spelling of a host, so a partial
// rule written with non-ASCII letters can match an A-label candidate.
func hostUnicode(host string) string {
	ascii, err := hostASCII(host)
	if err != nil || !strings.Contains(ascii, "xn--") {
		return ""
	}
	uni, err := idna.Lookup.ToUnicode(ascii)
	if err != nil {
		return ""
	}
	return strings.ToLower(uni)
}

// canonicalFuzzyURLPattern converts the host part of a partial URL pattern the
// same way URL candidates are converted. Anything that cannot be converted
// keeps its lowercase text.
func canonicalFuzzyURLPattern(p string) string {
	lp := strings.ToLower(strings.TrimSpace(p))
	if isASCII(lp) {
		return lp
	}
	if c, ok, err := canonicalURL(lp); ok && err == nil {
		return strings.ToLower(c)
	}
	end := strings.IndexAny(lp, "/?#")
	if end < 0 {
		end = len(lp)
	}
	if host, err := hostASCII(lp[:end]); err == nil {
		return host + lp[end:]
	}
	return lp
}
