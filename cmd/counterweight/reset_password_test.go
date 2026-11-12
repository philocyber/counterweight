package main

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestNativeResetRejectsInvalidPasswordAndRedactsConnection(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_RESET_DSN", "postgres://SECRET_USER:SECRET_PASSWORD@fixture.invalid/db?SECRET=query")
	for _, pw := range []string{"", strings.Repeat("a", 73), "valid fixture"} {
		t.Setenv("COUNTERWEIGHT_RESET_NEWPASS", pw)
		err := resetAdministratorPassword()
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("unsafe reset result %v", err)
		}
	}
}

func TestNativeResetEnforcesPasswordLength(t *testing.T) {
	// The DSN is unreachable, so a password that passes validation fails later
	// with a database error instead of the length error.
	t.Setenv("COUNTERWEIGHT_RESET_DSN", "postgres://user:pass@fixture.invalid:1/db?connect_timeout=1")
	for pw, rejected := range map[string]bool{
		"":                      true,
		"a":                     true,
		"seven77":               true,
		"ñññññññ":               true,
		strings.Repeat("a", 73): true,
		"eight888":              false,
		"ññññññññ":              false,
		strings.Repeat("a", 72): false,
	} {
		t.Setenv("COUNTERWEIGHT_RESET_NEWPASS", pw)
		err := resetAdministratorPassword()
		if err == nil {
			t.Fatalf("%q: reset unexpectedly succeeded", pw)
		}
		if got := strings.Contains(err.Error(), "at least 8 characters"); got != rejected {
			t.Errorf("%q: length rejection=%v, want %v (%v)", pw, got, rejected, err)
		}
	}
}

func TestNativeResetPreservesURIAndKeywordDSNs(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_NATIVE_RESET_E2E") != "1" {
		t.Skip("requires explicitly selected disposable PostgreSQL")
	}
	dsn := os.Getenv("COUNTERWEIGHT_PG_DSN")
	connection, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Exec(`CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL,updated_at TIMESTAMPTZ DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	keyword := os.Getenv("COUNTERWEIGHT_NATIVE_RESET_KEYWORD_DSN")
	if keyword == "" {
		t.Fatal("missing keyword DSN fixture")
	}
	for index, candidate := range []string{dsn, keyword, ""} {
		if index == 2 {
			t.Setenv("COUNTERWEIGHT_RESET_DBHOST", "127.0.0.1")
			t.Setenv("COUNTERWEIGHT_RESET_DBPORT", "51297")
			t.Setenv("COUNTERWEIGHT_RESET_DBUSER", "counterweight_test")
			t.Setenv("COUNTERWEIGHT_RESET_DBPASS", "test_only_password")
			t.Setenv("COUNTERWEIGHT_RESET_DBNAME", "native_reset_20261010")
			t.Setenv("COUNTERWEIGHT_RESET_SSLMODE", "disable")
		} else if candidate == "" {
			t.Fatal("missing keyword DSN fixture")
		}
		t.Setenv("COUNTERWEIGHT_RESET_DSN", candidate)
		t.Setenv("COUNTERWEIGHT_RESET_NEWPASS", "local reset fixture")
		if err := resetAdministratorPassword(); err != nil {
			t.Fatal(err)
		}
		var hash string
		if err := connection.QueryRow(`SELECT value FROM settings WHERE key='auth.password_hash'`).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("local reset fixture")); err != nil {
			t.Fatal(err)
		}
	}
}
