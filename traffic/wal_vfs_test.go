package traffic

import (
	"database/sql"
	"errors"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	sqlite "modernc.org/sqlite"
)

func TestBoundedWALVFSRejectsNativeWriteAndPreservesSnapshot(t *testing.T) {
	vfs, err := newBoundedWALVFS(65536)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := vfs.Close(); err != nil {
			t.Error(err)
		}
	})
	path := filepath.Join(t.TempDir(), "fixture.sqlite")
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := (&url.URL{Scheme: "file", Path: uriPath}).String() + "?vfs=" + vfs.name + "&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.Exec(`CREATE TABLE fixture(id INTEGER PRIMARY KEY, body BLOB); INSERT INTO fixture VALUES(1,zeroblob(16)); PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	var count int
	if err := snapshot.QueryRow(`SELECT COUNT(*) FROM fixture`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("snapshot: %d %v", count, err)
	}
	_, err = db.Exec(`INSERT INTO fixture VALUES(2,zeroblob(262144))`)
	var full *sqlite.Error
	if !errors.As(err, &full) || full.Code() != 13 {
		t.Fatalf("expected SQLITE_FULL before exceeding WAL cap: %v", err)
	}
	info, err := os.Stat(path + "-wal")
	if err != nil || info.Size() > 65536 {
		t.Fatalf("WAL allocation: %v %v", info, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM fixture`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback lost committed data: %d %v", count, err)
	}
	if err := snapshot.QueryRow(`SELECT COUNT(*) FROM fixture`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("snapshot invalidated: %d %v", count, err)
	}
	if err := vfs.Close(); err == nil {
		t.Fatal("VFS freed live SQLite handles")
	}
	if err := snapshot.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE); INSERT INTO fixture VALUES(3,zeroblob(16))`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM fixture`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("small write did not resume: %d %v", count, err)
	}
	tls := libc.NewTLS()
	defer tls.Close()
	var walFile uintptr
	walVFSFiles.Range(func(key, value any) bool {
		state := value.(*boundedWALFile)
		if state.owner == vfs && state.wal {
			walFile = key.(uintptr)
			return false
		}
		return true
	})
	if walFile == 0 {
		t.Fatal("native WAL handle missing")
	}
	oversize := sqlite3.Tsqlite3_int64(65537)
	if code := boundedWALControl(tls, walFile, sqlite3.SQLITE_FCNTL_SIZE_HINT, uintptr(unsafe.Pointer(&oversize))); code != sqlite3.SQLITE_FULL {
		t.Fatalf("allocation hint bypassed cap: %d", code)
	}
	chunk := int32(8192)
	if code := boundedWALControl(tls, walFile, sqlite3.SQLITE_FCNTL_CHUNK_SIZE, uintptr(unsafe.Pointer(&chunk))); code != sqlite3.SQLITE_FULL {
		t.Fatalf("rounded allocation enabled: %d", code)
	}
	if code := boundedWALTruncate(tls, walFile, oversize); code != sqlite3.SQLITE_FULL {
		t.Fatalf("truncate growth bypassed cap: %d", code)
	}
	if code := boundedWALWrite(tls, walFile, 0, 1, sqlite3.Tsqlite3_int64(1<<63-1)); code != sqlite3.SQLITE_FULL {
		t.Fatalf("offset overflow bypassed cap: %d", code)
	}
	info, err = os.Stat(path + "-wal")
	if err != nil || info.Size() > 65536 {
		t.Fatalf("control bypass grew WAL: %v %v", info, err)
	}
}

func TestBoundedWALVFSLeavesDefaultDatabaseAndLifecycleIntact(t *testing.T) {
	vfs, err := newBoundedWALVFS(65536)
	if err != nil {
		t.Fatal(err)
	}
	ptr := vfs.ptr
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ordinary.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE fixture(body BLOB); INSERT INTO fixture VALUES(zeroblob(262144))`); err != nil {
		t.Fatalf("unrelated default database inherited WAL cap: %v", err)
	}
	if err := vfs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := vfs.Close(); err != nil {
		t.Fatal(err)
	}
	if _, found := walVFSOwners.Load(ptr); found {
		t.Fatal("closed registration retained")
	}
}
