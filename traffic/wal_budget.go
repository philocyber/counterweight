package traffic

import (
	"context"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Philocyber/counterweight/locale"
)

const defaultWALAdmission int64 = 128 << 20

var ErrWALAdmission = locale.NewError("Traffic WAL storage admission blocked by an active snapshot")
var ErrWALWriteEstimate = locale.NewError("Traffic WAL write estimate exceeds storage admission limit")

func walAdmissionFromEnv() (int64, error) {
	limit := defaultWALAdmission
	if raw := os.Getenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES"); raw != "" {
		var err error
		limit, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || limit < 65536 {
			return 0, locale.NewError("COUNTERWEIGHT_TRAFFIC_WAL_BYTES must be at least 65536 bytes")
		}
	}
	return limit, nil
}

type WALStorageStatus struct {
	AdmissionBytes int64 `json:"admission_bytes"`
	UsedBytes      int64 `json:"used_bytes"`
}

func (t *Traffic) WALStorageStatus() (WALStorageStatus, error) {
	status := WALStorageStatus{AdmissionBytes: t.walAdmission}
	info, err := os.Stat(filepath.Join(t.dir, "_index", "index.sqlite-wal"))
	if os.IsNotExist(err) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.UsedBytes = info.Size()
	return status, nil
}

// Called under wmu before a capture/import transaction. Never wait for a reader
// or terminate its snapshot. This is admission between transactions, not a
// byte-by-byte limit on a transaction already in progress.
func (t *Traffic) checkWALAdmission(additional int64) error {
	if additional < 0 || additional > t.walAdmission {
		return locale.Errorf("%w (threshold=%d bytes)", ErrWALWriteEstimate, t.walAdmission)
	}
	status, err := t.WALStorageStatus()
	if err != nil {
		return err
	}
	if status.UsedBytes <= t.walAdmission-additional {
		return nil
	}
	ctx := context.Background()
	conn, err := t.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=0`); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, `PRAGMA busy_timeout=5000`)
	var busy, frames, checkpointed int
	if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &frames, &checkpointed); err != nil {
		return err
	}
	if busy == 0 && frames == checkpointed {
		if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &checkpointed); err != nil {
			return err
		}
	}
	status, err = t.WALStorageStatus()
	if err != nil {
		return err
	}
	if status.UsedBytes > t.walAdmission-additional {
		return locale.Errorf("%w (threshold=%d bytes)", ErrWALAdmission, t.walAdmission)
	}
	return nil
}
