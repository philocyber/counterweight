package traffic

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	sqlite "modernc.org/sqlite"
)

func TestIndexBudgetAllPooledConnections(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES", "1048576")
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	var connections []*sql.Conn
	for i := 0; i < 3; i++ {
		conn, err := tr.DB().Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		defer conn.Close()
	}
	for n, conn := range connections {
		var pageSize, maxPages int64
		if err := conn.QueryRowContext(context.Background(), `PRAGMA page_size`).Scan(&pageSize); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(context.Background(), `PRAGMA max_page_count`).Scan(&maxPages); err != nil {
			t.Fatal(err)
		}
		if maxPages*pageSize > 1048576 {
			t.Fatal("pooled connection lacks allocation ceiling")
		}
		if _, err := conn.ExecContext(context.Background(), `INSERT INTO exchange_bodies(id,req_body) VALUES(?,zeroblob(2097152))`, fmt.Sprint(n)); err == nil {
			t.Fatal("native SQLite allocation limit bypassed")
		} else {
			var full *sqlite.Error
			if !errors.As(err, &full) || full.Code() != 13 {
				t.Fatalf("expected SQLITE_FULL, got %v", err)
			}
		}
	}
}

func TestIndexBudgetCaptureDeletionAndRestart(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES", "1048576")
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte{0}, 4096)
	for i := 0; i < 200; i++ {
		tr.record(newFlow("budget.example", "GET", "/"+strconv.Itoa(i), nil, body))
	}
	count, err := tr.Count()
	if err != nil || count == 0 || count >= 200 || tr.BlobStorageStatus().DroppedCaptures == 0 {
		t.Fatalf("recording did not stop cleanly at capacity: %d %v", count, err)
	}
	var pages, size int64
	if err := tr.DB().QueryRow(`SELECT page_count,page_size FROM pragma_page_count,pragma_page_size`).Scan(&pages, &size); err != nil || pages*size > 1048576 {
		t.Fatal("index allocation exceeds ceiling")
	}
	tr.Close()
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if got, err := tr.Count(); err != nil || got != count {
		t.Fatalf("restart lost accepted evidence: %d %v", got, err)
	}
	if _, _, err := tr.DeleteAll(); err != nil {
		t.Fatalf("maintenance blocked by quota: %v", err)
	}
	tr.record(newFlow("recovered.example", "GET", "/", nil, body))
	if got, _ := tr.Count(); got != 1 {
		t.Fatal("recording did not resume after purge/compaction")
	}
}

func TestIndexBudgetImportRollbackAndRetry(t *testing.T) {
	body := bytes.Repeat([]byte{0}, 256*1024)
	source, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.record(newFlow("archive.example", "GET", "/", nil, body))
	archive := t.TempDir()
	if _, err := source.ExportHosts([]string{"archive.example"}, archive); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES", "1048576")
	dir := t.TempDir()
	target, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ImportArchive(archive); err == nil {
		t.Fatal("oversized index import accepted")
	}
	if count, _ := target.Count(); count != 0 {
		t.Fatal("rejected import left rows")
	}
	target.Close()
	t.Setenv("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES", "16777216")
	target, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	for _, want := range []int64{1, 0} {
		if n, err := target.ImportArchive(archive); err != nil || n != want {
			t.Fatalf("import/retry failed: %d %v", n, err)
		}
	}
}

func TestIndexBudgetPreservesOversizedExistingStore(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr.record(newFlow("old.example", "GET", "/", nil, bytes.Repeat([]byte{0}, 200*1024)))
	tr.Close()
	t.Setenv("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES", "262144")
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatal("old evidence inaccessible after lowering limit")
	}
	if _, _, err := tr.DeleteAll(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "_index", "index.sqlite")); err != nil || info.Size() > 262144 {
		t.Fatal("purge did not reclaim oversized store")
	}
}

func TestIndexBudgetRejectsInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{"0", "-1", "262143", "not-a-number"} {
		t.Setenv("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES", raw)
		tr, err := Open(t.TempDir(), "127.0.0.1:0")
		if tr != nil {
			tr.Close()
		}
		if err == nil {
			t.Fatal("invalid index budget accepted")
		}
	}
}
