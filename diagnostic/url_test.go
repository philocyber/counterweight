package diagnostic

import (
	"strings"
	"testing"
)

func TestURLDiagnosticContainsNoCredentialComponents(t *testing.T) {
	for _, raw := range []string{
		"https://USER_SECRET:PASS_SECRET@example.test:8443/PATH_SECRET?key=QUERY_SECRET#FRAGMENT_SECRET",
		"socks5://USER_SECRET:PASS_SECRET@[::1]:1080/PATH_SECRET?key=QUERY_SECRET",
		"https://USER_SECRET:PASS_SECRET@example.test/%zz?key=QUERY_SECRET",
		"USER_SECRET:PASS_SECRET@HOST_SECRET",
		"SECRET_SCHEME://USER_SECRET:PASS_SECRET@example.test/PATH_SECRET",
	} {
		got := URL(raw)
		if strings.Contains(got, "SECRET") {
			t.Fatalf("diagnostic leaks credential: %s", got)
		}
	}
	if got := URL("https://user:pass@example.test:8443/a?key=secret"); got != "https://example.test:8443/[redacted]" {
		t.Fatalf("lost useful destination: %s", got)
	}
}
