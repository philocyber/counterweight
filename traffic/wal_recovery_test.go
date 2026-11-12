package traffic

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"

	sqlite "modernc.org/sqlite"
)

func TestMinimumWALAllowanceCanPurgeAndResume(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 8; n++ {
		tr.record(newFlow("recovery.fixture", "GET", "/"+strconv.Itoa(n), nil, bytes.Repeat([]byte{0}, 200*1024)))
	}
	if n, err := tr.Count(); err != nil || n != 8 {
		t.Fatalf("seed: %d %v", n, err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "65536")
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if n, reclaimed, err := tr.DeleteAll(); err != nil || n != 8 || reclaimed <= 0 {
		t.Fatalf("same-limit purge cannot recover: deleted=%d reclaimed=%d err=%v", n, reclaimed, err)
	}
	var mode string
	if err := tr.DB().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL was not restored: %s %v", mode, err)
	}
	if n, err := tr.Count(); err != nil || n != 0 {
		t.Fatalf("purge incomplete: %d %v", n, err)
	}
	tr.record(newFlow("resumed.fixture", "GET", "/", nil, []byte("resumed")))
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("capture did not resume at unchanged capacity: %d %v", n, err)
	}
	status, err := tr.WALStorageStatus()
	if err != nil || status.AdmissionBytes != 65536 || status.UsedBytes > 65536 {
		t.Fatalf("recovery changed or exceeded WAL allowance: %+v %v", status, err)
	}
}

func TestRollbackRecoveryPreservesActiveSnapshot(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.record(newFlow("reader.fixture", "GET", "/", nil, []byte("saved")))
	snapshot, err := tr.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	var count int
	if err := snapshot.QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("snapshot: %d %v", count, err)
	}
	conn, err := tr.DB().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	called := false
	err = tr.withRollbackRecovery(conn, func() error { called = true; return nil })
	var native *sqlite.Error
	if !errors.As(err, &native) || native.Code()&255 != 5 || called {
		t.Fatalf("reader did not veto recovery: called=%v err=%v", called, err)
	}
	if err := snapshot.QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("snapshot invalidated: %d %v", count, err)
	}
	if err := snapshot.Rollback(); err != nil {
		t.Fatal(err)
	}
	err = tr.withRollbackRecovery(conn, func() error { called = true; return nil })
	if err != nil || !called || tr.recoveryPaused.Load() {
		t.Fatalf("released-reader recovery: called=%v paused=%v err=%v", called, tr.recoveryPaused.Load(), err)
	}
	var mode string
	if err := conn.QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL restore: %s %v", mode, err)
	}
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("recovery lost committed data: %d %v", n, err)
	}
}

func TestMinimumWALPurgeRetriesOnlyRolledBackDeletion(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 8; n++ {
		tr.record(newFlow("retry.fixture", "GET", "/"+strconv.Itoa(n), nil, bytes.Repeat([]byte{0x7b}, 200*1024)))
	}
	if n, err := tr.Count(); err != nil || n != 8 {
		t.Fatalf("seed: %d %v", n, err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "65536")
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	conn, err := tr.DB().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Secure deletion forces the deleted body pages to be rewritten, proving the
	// pre-commit quota-failure path rather than just post-commit compaction.
	if _, err := conn.ExecContext(context.Background(), `PRAGMA secure_delete=ON`); err != nil {
		t.Fatal(err)
	}
	deleted, committed, err := tr.purgeIndex(conn)
	if !isSQLiteFull(err) || committed || deleted != 0 {
		t.Fatalf("expected rolled-back purge: %d %v %v", deleted, committed, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := tr.Count(); err != nil || n != 8 {
		t.Fatalf("failed purge lost captures: %d %v", n, err)
	}
	if n, reclaimed, err := tr.DeleteAll(); err != nil || n != 8 || reclaimed <= 0 {
		t.Fatalf("pre-commit recovery: %d %d %v", n, reclaimed, err)
	}
	if n, err := tr.Count(); err != nil || n != 0 {
		t.Fatalf("recovery left captures: %d %v", n, err)
	}
	status, err := tr.WALStorageStatus()
	if err != nil || status.AdmissionBytes != 65536 || status.UsedBytes > 65536 {
		t.Fatalf("recovery bypassed allowance: %+v %v", status, err)
	}
}

func TestRollbackJournalCapRollsBackAndRestoresWAL(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	body := bytes.Repeat([]byte{0x7b}, 200*1024)
	tr.record(newFlow("journal.fixture", "GET", "/", nil, body))
	// Tighten only this test's journal allowance before any rollback file opens.
	tr.walVFS.journalLimit = 65536
	conn, err := tr.DB().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = tr.withRollbackRecovery(conn, func() error {
		tx, err := conn.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE exchange_bodies SET resp_body=zeroblob(204800)`); err != nil {
			return err
		}
		return tx.Commit()
	})
	if !isSQLiteFull(err) {
		t.Fatalf("rollback journal escaped its bound: %v", err)
	}
	var saved []byte
	if err := conn.QueryRowContext(context.Background(), `SELECT resp_body FROM exchange_bodies`).Scan(&saved); err != nil || !bytes.Equal(saved, body) {
		t.Fatalf("journal failure lost evidence: %v", err)
	}
	var mode string
	if err := conn.QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" || tr.recoveryPaused.Load() {
		t.Fatalf("journal failure did not restore capture mode: %s %v", mode, err)
	}
}

func TestMinimumWALCommittedPurgeDoesNotInvalidateReader(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte{0}, 200*1024)
	for n := 0; n < 8; n++ {
		tr.record(newFlow("committed.fixture", "GET", "/"+strconv.Itoa(n), nil, body))
	}
	if n, err := tr.Count(); err != nil || n != 8 {
		t.Fatalf("seed: %d %v", n, err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "65536")
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	snapshot, err := tr.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	var count int
	if err := snapshot.QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&count); err != nil || count != 8 {
		t.Fatalf("snapshot: %d %v", count, err)
	}
	if n, _, err := tr.DeleteAll(); err != nil || n != 8 {
		t.Fatalf("committed purge lost its result: %d %v", n, err)
	}
	if n, err := tr.Count(); err != nil || n != 0 {
		t.Fatalf("current captures: %d %v", n, err)
	}
	if err := snapshot.QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&count); err != nil || count != 8 {
		t.Fatalf("reader was invalidated: %d %v", count, err)
	}
	var saved []byte
	if err := snapshot.QueryRow(`SELECT resp_body FROM exchange_bodies LIMIT 1`).Scan(&saved); err != nil || !bytes.Equal(saved, body) {
		t.Fatalf("reader body changed: %v", err)
	}
	if err := snapshot.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n, reclaimed, err := tr.DeleteAll(); err != nil || n != 0 || reclaimed <= 0 {
		t.Fatalf("post-commit recovery repeated deletion: %d %d %v", n, reclaimed, err)
	}
}

func TestWALRestoreFailurePausesWritesAndCanRecover(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.record(newFlow("pause.fixture", "GET", "/saved", nil, []byte("saved")))
	conn, err := tr.DB().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var reader *sql.Tx
	err = tr.withRollbackRecovery(conn, func() error {
		var err error
		reader, err = tr.DB().Begin()
		if err != nil {
			return err
		}
		var count int
		return reader.QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&count)
	})
	if reader != nil {
		defer reader.Rollback()
	}
	var native *sqlite.Error
	if !errors.As(err, &native) || native.Code()&255 != 5 || !tr.recoveryPaused.Load() {
		t.Fatalf("restoration failure was not fail-closed: paused=%v err=%v", tr.recoveryPaused.Load(), err)
	}
	tr.record(newFlow("pause.fixture", "GET", "/blocked", nil, []byte("blocked")))
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("paused capture wrote or lost saved data: %d %v", n, err)
	}
	if err := tr.checkIndexAdmission(0); err == nil {
		t.Fatal("zero-estimate import bypasses pause")
	}
	if n, err := tr.ExportHosts([]string{"pause.fixture"}, t.TempDir()); err != nil || n != 1 {
		t.Fatalf("paused evidence cannot export: %d %v", n, err)
	}
	if err := reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if n, _, err := tr.DeleteAll(); err != nil || n != 1 || tr.recoveryPaused.Load() {
		t.Fatalf("clear-all recovery: %d paused=%v err=%v", n, tr.recoveryPaused.Load(), err)
	}
	tr.record(newFlow("pause.fixture", "GET", "/resumed", nil, []byte("resumed")))
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("capture did not resume: %d %v", n, err)
	}
}
