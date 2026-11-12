package db

import "testing"

func TestTestDSNAppendsTestSuffix(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TEST_PG_DSN", "")
	for in, want := range map[string]string{
		"postgres://u:p@127.0.0.1:5433/counterweight?sslmode=disable":      "postgres://u:p@127.0.0.1:5433/counterweight_test?sslmode=disable",
		"postgres://u:p@127.0.0.1:5433/counterweight_test?sslmode=disable": "postgres://u:p@127.0.0.1:5433/counterweight_test?sslmode=disable",
	} {
		got, err := testDatabaseDSN(in)
		if err != nil || got != want {
			t.Errorf("testDatabaseDSN(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"host=127.0.0.1 dbname=counterweight", "postgres://u:p@127.0.0.1:5433/"} {
		if _, err := testDatabaseDSN(in); err == nil {
			t.Errorf("testDatabaseDSN(%q) should fail", in)
		}
	}
}

func TestTestDSNPrefersExplicitTestDSN(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TEST_PG_DSN", "postgres://x@h/other_test")
	if got, err := testDatabaseDSN("postgres://x@h/counterweight"); err != nil || got != "postgres://x@h/other_test" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDSNUnderTestsNeverReturnsConfiguredDatabase(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TEST_PG_DSN", "")
	t.Setenv("COUNTERWEIGHT_PG_DSN", "postgres://u:p@127.0.0.1:5433/counterweight")
	got, _, err := DSN()
	if err != nil || got != "postgres://u:p@127.0.0.1:5433/counterweight_test" {
		t.Fatalf("DSN() = %q, %v", got, err)
	}
}

func TestOpenRefusesNonTestDatabase(t *testing.T) {
	for _, dsn := range []string{
		"postgres://u:p@127.0.0.1:1/counterweight",
		"postgres://u:p@127.0.0.1:1/",
		"host=127.0.0.1 dbname=counterweight_test",
	} {
		if _, err := Open(dsn); err == nil || err.Error() != refuseNonTestDatabase(dsn).Error() {
			t.Errorf("Open(%q) error = %v; want refusal", dsn, err)
		}
	}
	if err := refuseNonTestDatabase("postgres://u:p@127.0.0.1:1/counterweight_test"); err != nil {
		t.Fatalf("test database refused: %v", err)
	}
}
