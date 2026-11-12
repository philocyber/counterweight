package config

import (
	"strings"
	"testing"
)

func TestDSNDiagnosticsNeverEchoCredentialComponents(t *testing.T) {
	for _, dsn := range []string{
		"postgres://SECRET_USER:SECRET_PASSWORD@127.0.0.1:5432/SECRET_DB?password=SECRET_QUERY#SECRET_FRAGMENT",
		"postgresql://127.0.0.1/db?user=SECRET_USER&password=SECRET_PASSWORD",
		"postgres://SECRET_USER:SECRET_PASSWORD@127.0.0.1/db?password=%ZZ_SECRET_QUERY",
		"postgres://SECRET_USER:%ZZ_SECRET_PASSWORD@127.0.0.1/db",
		"host=127.0.0.1 user=SECRET_USER password='SECRET_PASSWORD' dbname=db",
		"SECRET_MALFORMED",
	} {
		if got := Redact(dsn); strings.Contains(got, "SECRET") {
			t.Errorf("credential-bearing DSN escaped the diagnostic boundary: %q", got)
		}
	}
	if got := Redact("postgres://user:password@[::1]:5432/db?sslmode=disable"); got != "postgres://[::1]:5432/[redacted]" {
		t.Fatalf("network destination lost: %q", got)
	}
}
