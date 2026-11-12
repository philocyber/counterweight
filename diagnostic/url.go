// Package diagnostic formats configuration addresses without credential data.
package diagnostic

import (
	"net/url"
	"strings"
)

// URL retains only the scheme and network authority. Paths, queries, fragments
// and URL user info may all carry credentials. Invalid input is never echoed.
func URL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "[invalid URL]"
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5":
		return strings.ToLower(u.Scheme) + "://" + u.Host + "/[redacted]"
	default:
		return "[unsupported URL]"
	}
}
