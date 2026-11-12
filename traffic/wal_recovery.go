package traffic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Philocyber/counterweight/locale"
	sqlite "modernc.org/sqlite"
)

func isSQLiteFull(err error) bool {
	var native *sqlite.Error
	return errors.As(err, &native) && native.Code()&255 == 13
}

// Explicit clear-all recovery may need to rewrite more pages than a small WAL
// can hold. Use the separately bounded rollback journal on the same connection,
// only after the WAL attempt has rolled back. Active readers prevent the mode
// change; never invalidate their snapshot to make recovery succeed.
func (t *Traffic) withRollbackRecovery(conn *sql.Conn, work func() error) (err error) {
	ctx := context.Background()
	// SQLite mode changes also need idle pooled native handles to release their
	// WAL locks. The traffic pool uses database/sql's default two idle handles.
	// Checked-out readers remain live and can still veto recovery with BUSY.
	t.db.SetMaxIdleConns(0)
	defer t.db.SetMaxIdleConns(2)
	var timeout int
	if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=0`); err != nil {
		return err
	}
	defer func() {
		_, restoreErr := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", timeout))
		err = errors.Join(err, restoreErr)
	}()
	var mode string
	if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		return err
	}
	if mode != "delete" {
		return locale.NewError("Traffic recovery could not acquire rollback journal")
	}
	t.recoveryPaused.Store(true)
	defer func() {
		var restored string
		restoreErr := conn.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&restored)
		if restoreErr == nil && restored != "wal" {
			restoreErr = locale.NewError("Traffic recovery could not restore WAL")
		}
		if restoreErr == nil {
			t.recoveryPaused.Store(false)
		}
		err = errors.Join(err, restoreErr)
	}()
	return work()
}
