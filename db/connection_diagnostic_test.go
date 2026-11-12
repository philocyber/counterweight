package db

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/locale"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestConnectionDiagnosticsOmitParserAndServerSecrets(t *testing.T) {
	for _, err := range []error{
		pgconn.NewParseConfigError("SECRET_DSN", "SECRET_OPTION", errors.New("SECRET_CAUSE")),
		&pgconn.PgError{Code: "28P01", Message: "SECRET_MESSAGE", Detail: "SECRET_DETAIL"},
		&pgconn.PgError{Code: "SECRET_SQLSTATE", Message: "SECRET_MESSAGE"},
		&net.DNSError{Name: "SECRET_HOST", Err: "SECRET_ERROR"},
		errors.New("SECRET_UNKNOWN"),
	} {
		got := connectionDiagnostic(err)
		for _, text := range []string{fmt.Sprintf("%v", got), fmt.Sprintf("%+v", got), locale.ErrorMessage(locale.En, got)} {
			if strings.Contains(text, "SECRET") {
				t.Errorf("driver details leaked: %q", text)
			}
		}
	}
	if got := connectionDiagnostic(&pgconn.PgError{Code: "28P01", Message: "hidden"}); !strings.Contains(got.Error(), "28P01") {
		t.Fatal("diagnostic lost the actionable SQLSTATE")
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		if !errors.Is(connectionDiagnostic(err), err) {
			t.Fatal("cancellation classification lost")
		}
	}
}

func TestOpenConnectionErrorsNeverEchoSecrets(t *testing.T) {
	// All fail in the real pgx parser, before contacting a database.
	for _, dsn := range []string{
		"postgres://SECRET_USER:%ZZ_SECRET_PASSWORD@127.0.0.1/db",
		"postgres://SECRET_USER:SECRET_PASSWORD@127.0.0.1/db?sslmode=SECRET_INVALID",
		"host=127.0.0.1 user=SECRET_USER password=SECRET_PASSWORD sslmode=SECRET_INVALID",
	} {
		d, err := Open(dsn)
		if d != nil {
			_ = d.Close()
		}
		if err == nil {
			t.Fatal("invalid connection configuration was accepted")
		}
		for _, text := range []string{err.Error(), locale.ErrorMessage(locale.En, err)} {
			if strings.Contains(text, "SECRET") {
				t.Errorf("real pgx diagnostic leaked credentials: %q", text)
			}
		}
	}
}
