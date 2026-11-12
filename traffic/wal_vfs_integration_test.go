package traffic

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	sqlite "modernc.org/sqlite"
)

func TestTrafficWALBoundaryRollsBackMaintenance(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte{0}, 200*1024)
	for n := 0; n < 8; n++ {
		tr.record(newFlow("wal-maintenance.fixture", "GET", "/"+strconv.Itoa(n), nil, body))
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
	if _, err := tr.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	snapshot, err := tr.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	var count int
	if err := snapshot.QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&count); err != nil || count != 8 {
		t.Fatalf("snapshot: %d %v", count, err)
	}
	_, err = tr.DeleteHost("wal-maintenance.fixture")
	var full *sqlite.Error
	if !errors.As(err, &full) || full.Code() != 13 {
		t.Fatalf("maintenance write did not stop at native WAL boundary: %v", err)
	}
	status, err := tr.WALStorageStatus()
	if err != nil || status.UsedBytes > status.AdmissionBytes {
		t.Fatalf("WAL exceeded configured cap: %+v %v", status, err)
	}
	if n, err := tr.Count(); err != nil || n != 8 {
		t.Fatalf("partial deletion committed: %d %v", n, err)
	}
	if err := snapshot.QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&count); err != nil || count != 8 {
		t.Fatalf("pinned snapshot changed: %d %v", count, err)
	}
	rows, err := tr.DB().Query(`SELECT resp_body FROM exchange_bodies`)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for rows.Next() {
		var saved []byte
		if err := rows.Scan(&saved); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(saved, body) {
			t.Fatal("rollback changed evidence bytes")
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if seen != 8 {
		t.Fatalf("evidence bodies lost: %d", seen)
	}
	if err := snapshot.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n, err := tr.ExportHosts([]string{"wal-maintenance.fixture"}, t.TempDir()); err != nil || n != 8 {
		t.Fatalf("bounded store evidence no longer exportable: %d %v", n, err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "134217728")
	retry, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer retry.Close()
	if n, _, err := retry.DeleteAll(); err != nil || n != 8 {
		t.Fatalf("recovery purge: %d %v", n, err)
	}
}

func TestTrafficWALVFSRetainsCheckedOutConnectionAfterClose(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	conn, err := tr.DB().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ptr := tr.walVFS.ptr
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if _, found := walVFSOwners.Load(ptr); !found {
		t.Fatal("registration freed before native connection close")
	}
	var count int
	if err := conn.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM exchanges`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("late connection no longer readable: %d %v", count, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, found := walVFSOwners.Load(ptr); found {
		t.Fatal("registration leaked after last native connection close")
	}
}

func TestTrafficWALVFSUsesActualEncodedRelativePath(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := "relative # % & traffic"
	if runtime.GOOS != "windows" {
		dir += "?"
	}
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr.record(newFlow("encoded.fixture", "GET", "/", nil, []byte("unchanged")))
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("capture: %d %v", n, err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "_index", "index.sqlite")); err != nil || info.Size() == 0 {
		t.Fatalf("index written to wrong URI path: %v %v", info, err)
	}
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("encoded-path evidence lost on restart: %d %v", n, err)
	}
}
