package db

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/locale"
)

// testDatabaseSuffix marks a database that tests may write to. Test binaries
// never touch the configured (development or production) database: DSN points
// them at "<dbname>_test" and Open refuses any other database.
const testDatabaseSuffix = "_test"

// testDatabaseDSN returns the database a test binary should use. An explicit
// COUNTERWEIGHT_TEST_PG_DSN wins; otherwise the configured DSN is reused with
// "_test" appended to the database name (Open creates it on first use).
func testDatabaseDSN(dsn string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("COUNTERWEIGHT_TEST_PG_DSN")); v != "" {
		return v, nil
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return "", locale.Errorf("tests need a postgres:// DSN to derive a test database; set COUNTERWEIGHT_TEST_PG_DSN")
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return "", locale.Errorf("tests need a database name in the DSN to derive a test database; set COUNTERWEIGHT_TEST_PG_DSN")
	}
	if !strings.HasSuffix(name, testDatabaseSuffix) {
		u.Path = "/" + name + testDatabaseSuffix
	}
	return u.String(), nil
}

// refuseNonTestDatabase stops a test binary from opening a database whose name
// does not end in "_test", whatever DSN the test passed in.
func refuseNonTestDatabase(dsn string) error {
	if !testing.Testing() {
		return nil
	}
	u, err := url.Parse(dsn)
	if err == nil && u.Scheme != "" && strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), testDatabaseSuffix) {
		return nil
	}
	return locale.Errorf("refusing to open a non-test database from a test binary; use a database whose name ends in %q (see COUNTERWEIGHT_TEST_PG_DSN)", testDatabaseSuffix)
}
