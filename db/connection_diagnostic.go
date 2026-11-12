package db

import (
	"context"
	"errors"
	"net"

	"github.com/Philocyber/counterweight/locale"
	"github.com/jackc/pgx/v5/pgconn"
)

// Driver errors can embed the entire DSN, invalid option values or server text.
// Only structural categories cross the diagnostic boundary. Do not retain the
// original error as an unwrap cause: alternate formatting must stay safe too.
func connectionDiagnostic(err error) error {
	var parse *pgconn.ParseConfigError
	var pg *pgconn.PgError
	var network net.Error
	switch {
	case errors.As(err, &parse):
		return locale.NewError("Invalid PostgreSQL connection configuration")
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.As(err, &pg):
		// SQLSTATE has a fixed alphabet, unlike the server's message/detail fields.
		if len(pg.Code) == 5 {
			valid := true
			for _, c := range pg.Code {
				valid = valid && ((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z'))
			}
			if valid {
				return locale.Errorf("PostgreSQL rejected the request (SQLSTATE %s)", pg.Code)
			}
		}
		return locale.NewError("PostgreSQL rejected the request")
	case errors.As(err, &network):
		if network.Timeout() {
			return locale.NewError("PostgreSQL connection timed out")
		}
		return locale.NewError("PostgreSQL network connection failed")
	default:
		return locale.NewError("PostgreSQL connection failed")
	}
}
