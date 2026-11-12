package traffic

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedTemporarySortPreservesCommittedRows(t *testing.T) {
	vfs, err := newBoundedWALVFS(65536)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := vfs.Close(); err != nil {
			t.Error(err)
		}
	})
	vfs.tempLimit = 65536
	vfs.journalLimit = 65536
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "sort.sqlite"))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := &url.URL{Scheme: "file", Path: path}
	uri.RawQuery = url.Values{"vfs": {vfs.name}}.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA temp_store=FILE; PRAGMA cache_size=10; CREATE TABLE fixture(id INTEGER PRIMARY KEY, body BLOB); WITH RECURSIVE numbers(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM numbers WHERE n<10000) INSERT INTO fixture SELECT n,zeroblob(1024) FROM numbers`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT body FROM fixture ORDER BY random()`)
	if err == nil {
		for rows.Next() {
			var body []byte
			if scanErr := rows.Scan(&body); scanErr != nil {
				t.Fatal(scanErr)
			}
		}
		err = rows.Err()
		rows.Close()
	}
	if !isSQLiteFull(err) {
		t.Fatalf("disk sort escaped temporary-file allowance: %v", err)
	}
	// Temp tables use SQLITE_OPEN_TEMP_DB rather than the sorter's TEMP_JOURNAL.
	// Disable temp journaling so this assertion tests the temporary DB boundary.
	if _, err := db.Exec(`PRAGMA temp.cache_size=10; PRAGMA temp.journal_mode=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TEMP TABLE overflow AS SELECT * FROM fixture`); !isSQLiteFull(err) {
		t.Fatalf("temporary database escaped its allowance: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM fixture`).Scan(&count); err != nil || count != 10000 {
		t.Fatalf("sort failure lost committed rows: %d %v", count, err)
	}
	var body []byte
	if err := db.QueryRow(`SELECT body FROM fixture WHERE id=1`).Scan(&body); err != nil || len(body) != 1024 {
		t.Fatalf("failed sort lost readable body: %d %v", len(body), err)
	}
}

func TestTrafficTemporaryBoundaryUsesPooledConnections(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if tr.walVFS.tempLimit != tr.walVFS.journalLimit || tr.walVFS.tempLimit <= tr.indexLimit {
		t.Fatal("production temporary-file allowance not initialized")
	}
	body := bytes.Repeat([]byte{0}, 200*1024)
	tr.record(newFlow("temporary.fixture", "GET", "/", nil, body))
	// Tighten only this fixture's temporary allocation before any temp file opens.
	tr.walVFS.tempLimit = 65536
	var connections []*sql.Conn
	defer func() {
		for _, conn := range connections {
			conn.Close()
		}
	}()
	for n := 0; n < 2; n++ {
		conn, err := tr.DB().Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		if _, err := conn.ExecContext(context.Background(), `PRAGMA temp_store=FILE; PRAGMA temp.cache_size=10; PRAGMA temp.journal_mode=OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(context.Background(), `CREATE TEMP TABLE overflow AS SELECT resp_body FROM exchange_bodies`); !isSQLiteFull(err) {
			t.Fatalf("pooled connection %d escaped temp-file cap: %v", n, err)
		}
		var saved []byte
		if err := conn.QueryRowContext(context.Background(), `SELECT resp_body FROM exchange_bodies`).Scan(&saved); err != nil || !bytes.Equal(saved, body) {
			t.Fatalf("temporary failure changed evidence on connection %d: %v", n, err)
		}
	}
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("temporary failure lost capture: %d %v", n, err)
	}
}
