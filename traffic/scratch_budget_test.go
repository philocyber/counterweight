package traffic

import (
	"bytes"
	"context"
	"database/sql"
	"sync"
	"testing"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestSQLiteScratchBudgetSharedAcrossPooledConnections(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if tr.walVFS.scratchLimit != tr.walVFS.tempLimit {
		t.Fatal("production scratch allowance not initialized")
	}
	tr.DB().SetMaxIdleConns(0)
	body := bytes.Repeat([]byte{0x7b}, 40*1024)
	tr.record(newFlow("scratch.fixture", "GET", "/", nil, body))
	tr.walVFS.tempLimit = 256 * 1024
	tr.walVFS.scratchLimit = 65536
	ctx := context.Background()
	first, err := tr.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	setup := `PRAGMA temp_store=FILE; PRAGMA temp.cache_size=10; PRAGMA temp.journal_mode=OFF; CREATE TEMP TABLE held AS SELECT resp_body FROM exchange_bodies`
	if _, err := first.ExecContext(ctx, setup); err != nil {
		t.Fatalf("one legitimate scratch file should fit: %v", err)
	}
	second, err := tr.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.ExecContext(ctx, setup); !isSQLiteFull(err) {
		t.Fatalf("multiple files bypassed shared allowance: %v", err)
	}
	tr.walVFS.mu.Lock()
	used := tr.walVFS.scratchUsed
	tr.walVFS.mu.Unlock()
	if used <= 0 || used > 65536 {
		t.Fatalf("scratch accounting outside allowance: %d", used)
	}
	var saved []byte
	if err := first.QueryRowContext(ctx, `SELECT resp_body FROM held`).Scan(&saved); err != nil || !bytes.Equal(saved, body) {
		t.Fatalf("competing allocation changed live scratch data: %v", err)
	}
	// The second file would fit its own 256-KiB ceiling. Allocation hints,
	// truncate growth and sparse writes must also respect the shared allowance.
	tls := libc.NewTLS()
	defer tls.Close()
	var file uintptr
	var state *boundedWALFile
	walVFSFiles.Range(func(key, value any) bool {
		candidate := value.(*boundedWALFile)
		if candidate.owner == tr.walVFS && candidate.scratch && candidate.allocated > 0 {
			file, state = key.(uintptr), candidate
			return false
		}
		return true
	})
	if file == 0 {
		t.Fatal("live native scratch file missing")
	}
	target := sqlite3.Tsqlite3_int64(state.allocated + 65536)
	if target > sqlite3.Tsqlite3_int64(state.limit) {
		t.Fatal("fixture exceeds per-file limit instead of shared budget")
	}
	if code := boundedWALControl(tls, file, sqlite3.SQLITE_FCNTL_SIZE_HINT, uintptr(unsafe.Pointer(&target))); code != sqlite3.SQLITE_FULL {
		t.Fatalf("size hint bypassed shared budget: %d", code)
	}
	if code := boundedWALTruncate(tls, file, target); code != sqlite3.SQLITE_FULL {
		t.Fatalf("truncate bypassed shared budget: %d", code)
	}
	if code := boundedWALWrite(tls, file, 0, 1, target-1); code != sqlite3.SQLITE_FULL {
		t.Fatalf("sparse write bypassed shared budget: %d", code)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	tr.walVFS.mu.Lock()
	used = tr.walVFS.scratchUsed
	tr.walVFS.mu.Unlock()
	if used != 0 {
		t.Fatalf("closed scratch files still consume allowance: %d", used)
	}
	third, err := tr.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if _, err := third.ExecContext(ctx, setup); err != nil {
		t.Fatalf("released allowance not reusable: %v", err)
	}
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("scratch failure lost capture: %d %v", n, err)
	}
}

func TestSQLiteScratchConcurrentAllocationsStayWithinBudget(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.DB().SetMaxIdleConns(0)
	tr.record(newFlow("parallel-scratch.fixture", "GET", "/", nil, bytes.Repeat([]byte{0x7b}, 40*1024)))
	tr.walVFS.tempLimit, tr.walVFS.scratchLimit = 256*1024, 65536
	ctx := context.Background()
	var connections []*sql.Conn
	defer func() {
		for _, conn := range connections {
			conn.Close()
		}
	}()
	for n := 0; n < 4; n++ {
		conn, err := tr.DB().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		if _, err := conn.ExecContext(ctx, `PRAGMA temp_store=FILE; PRAGMA temp.cache_size=10; PRAGMA temp.journal_mode=OFF`); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, len(connections))
	var workers sync.WaitGroup
	for _, conn := range connections {
		workers.Go(func() {
			<-start
			_, err := conn.ExecContext(ctx, `CREATE TEMP TABLE held AS SELECT resp_body FROM exchange_bodies`)
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	blocked := 0
	for err := range results {
		if isSQLiteFull(err) {
			blocked++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if blocked == 0 {
		t.Fatal("parallel allocations bypassed shared budget")
	}
	tls := libc.NewTLS()
	defer tls.Close()
	var actual int64
	walVFSFiles.Range(func(key, value any) bool {
		state := value.(*boundedWALFile)
		if state.owner == tr.walVFS && state.scratch {
			size, code := state.nativeSize(tls, key.(uintptr))
			if code != sqlite3.SQLITE_OK || size < 0 || size > state.allocated {
				t.Errorf("native scratch accounting: size=%d reserved=%d code=%d", size, state.allocated, code)
			}
			actual += size
		}
		return true
	})
	tr.walVFS.mu.Lock()
	used := tr.walVFS.scratchUsed
	tr.walVFS.mu.Unlock()
	if actual > 65536 || used > 65536 || used < actual {
		t.Fatalf("parallel scratch overshoot: actual=%d reserved=%d", actual, used)
	}
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("parallel temporary failures changed captures: %d %v", n, err)
	}
}
