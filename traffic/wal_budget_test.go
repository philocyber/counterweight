package traffic

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/locale"
)

// A real archive read transaction pins old WAL frames while capture continues.
// Use small admission thresholds to exercise this without a large disk fixture.
func pinWALAtAdmission(t *testing.T, tr *Traffic) func() {
	t.Helper()
	tr.record(newFlow("seed.example", "GET", "/", nil, []byte("seed")))
	if _, err := tr.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	tr.archiveFiles.RLock()
	tx, err := tr.beginArchiveSnapshot()
	if err != nil {
		tr.archiveFiles.RUnlock()
		t.Fatal(err)
	}
	released := false
	ready := false
	release := func() {
		if !released {
			tx.Rollback()
			tr.archiveFiles.RUnlock()
			released = true
		}
	}
	t.Cleanup(release)
	defer func() {
		if !ready {
			release()
		}
	}()
	for i := 0; i < 100; i++ {
		dropped := tr.BlobStorageStatus().DroppedCaptures
		// Fill with small writes so exhaustion also blocks a small-row import,
		// rather than just a larger growth fixture's reservation.
		tr.record(newFlow("growth.example", "GET", "/"+strconv.Itoa(i), nil, nil))
		status, err := tr.WALStorageStatus()
		if err != nil {
			t.Fatal(err)
		}
		if tr.BlobStorageStatus().DroppedCaptures > dropped {
			if status.UsedBytes > status.AdmissionBytes {
				t.Fatal("fixture exceeded WAL admission before blocking")
			}
			var snapshotCount int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&snapshotCount); err != nil || snapshotCount != 1 {
				t.Fatalf("pinned archive snapshot changed: %d %v", snapshotCount, err)
			}
			ready = true
			return release
		}
	}
	t.Fatal("fixture did not exhaust WAL admission headroom")
	return release
}

func TestWALAdmissionPinnedSnapshotAndResume(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "524288")
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	release := pinWALAtAdmission(t, tr)
	defer release()
	before, _ := tr.WALStorageStatus()
	count, _ := tr.Count()
	dropped := tr.BlobStorageStatus().DroppedCaptures
	for i := 0; i < 20; i++ {
		tr.record(newFlow("rejected.example", "GET", "/"+strconv.Itoa(i), nil, bytes.Repeat([]byte{0}, maxInlineBody+1)))
	}
	after, err := tr.WALStorageStatus()
	if err != nil || after.UsedBytes != before.UsedBytes {
		t.Fatalf("rejected capture grew WAL: %+v %+v %v", before, after, err)
	}
	if n, err := tr.Count(); err != nil || n != count {
		t.Fatalf("rejected capture persisted: %d %v", n, err)
	}
	if s := tr.BlobStorageStatus(); s.UsedBytes != 0 || s.DroppedCaptures != dropped+20 {
		t.Fatalf("rejected capture leaked blobs or lost diagnostics: %+v", s)
	}
	var timeout int
	if err := tr.DB().QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("checkpoint changed pooled busy timeout: %d %v", timeout, err)
	}
	release()
	tr.record(newFlow("resumed.example", "GET", "/", nil, []byte("resumed")))
	if n, err := tr.Count(); err != nil || n != count+1 {
		t.Fatalf("capture did not resume after reader released: %d %v", n, err)
	}
}

func TestWALAdmissionImportRollbackAndRetry(t *testing.T) {
	source, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	// Separate independent stores' fixture IDs from the existing per-second
	// capture counter, so this exercises a new import rather than an ID retry.
	source.seq.Store(5000)
	source.record(newFlow("import.example", "GET", "/", nil, bytes.Repeat([]byte{0}, maxInlineBody+1)))
	archive := t.TempDir()
	if _, err := source.ExportHosts([]string{"import.example"}, archive); err != nil {
		t.Fatal(err)
	}
	// This blob import must fit an empty WAL, so this fixture isolates a pinned
	// reader's headroom rather than permanent rejection of an oversized write.
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "524288")
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	release := pinWALAtAdmission(t, tr)
	defer release()
	count, _ := tr.Count()
	if _, err := tr.ImportArchive(archive); !errors.Is(err, ErrWALAdmission) {
		t.Fatalf("pinned WAL import accepted or wrong failure: %v", err)
	} else if !strings.Contains(locale.ErrorMessage(locale.En, err), "snapshot") {
		t.Fatal("WAL error not localized")
	}
	if n, _ := tr.Count(); n != count || tr.BlobStorageStatus().UsedBytes != 0 {
		t.Fatal("rejected import left partial state")
	}
	release()
	for _, want := range []int64{1, 0} {
		if n, err := tr.ImportArchive(archive); err != nil || n != want {
			t.Fatalf("import/retry failed: %d %v", n, err)
		}
	}
}

func TestWALAdmissionInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{"0", "-1", "65535", "invalid"} {
		t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", raw)
		tr, err := Open(t.TempDir(), "127.0.0.1:0")
		if tr != nil {
			tr.Close()
		}
		if err == nil {
			t.Fatal("invalid WAL admission accepted")
		}
	}
}

func TestWALAdmissionAccountsForIncomingCapture(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "262144")
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if _, err := tr.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	before, err := tr.WALStorageStatus()
	if err != nil {
		t.Fatal(err)
	}
	// An exactly-inline binary body alone fills the configured WAL allowance.
	// Its metadata and SQLite page frames also need storage in the transaction.
	flow := newFlow("incoming.example", "GET", "/", nil, bytes.Repeat([]byte{0}, maxInlineBody))
	tr.record(flow)
	after, err := tr.WALStorageStatus()
	if err != nil || after.UsedBytes != before.UsedBytes {
		t.Fatalf("oversized incoming capture grew WAL: %+v => %+v %v", before, after, err)
	}
	if n, err := tr.Count(); err != nil || n != 0 || tr.BlobStorageStatus().DroppedCaptures != 1 {
		t.Fatalf("oversized capture was retained: %d %v", n, err)
	}
	if len(flow.Response.Body) != maxInlineBody {
		t.Fatal("capture admission changed the target response")
	}
}

func TestWALAdmissionMinimumAllowsSmallCapture(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "65536")
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.record(newFlow("small.example", "GET", "/", nil, []byte("small")))
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("minimum supported threshold cannot capture small evidence: %d %v", n, err)
	}
	status, err := tr.WALStorageStatus()
	if err != nil || status.UsedBytes > status.AdmissionBytes {
		t.Fatalf("small capture exceeded allowance: %+v %v", status, err)
	}
}

func TestWALAdmissionIncomingImportRollbackAndRetry(t *testing.T) {
	source, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.record(newFlow("large-import.example", "GET", "/", nil, bytes.Repeat([]byte{0}, maxInlineBody)))
	archive := t.TempDir()
	if n, err := source.ExportHosts([]string{"large-import.example"}, archive); err != nil || n != 1 {
		t.Fatalf("source export failed: %d %v", n, err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "262144")
	dir := t.TempDir()
	target, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ImportArchive(archive); !errors.Is(err, ErrWALWriteEstimate) {
		t.Fatalf("oversized import did not report its estimated write: %v", err)
	} else if !strings.Contains(locale.ErrorMessage(locale.En, err), "estimate") {
		t.Fatal("estimated-write error not localized")
	}
	status, err := target.WALStorageStatus()
	if n, countErr := target.Count(); err != nil || countErr != nil || n != 0 || status.UsedBytes != 0 || target.BlobStorageStatus().UsedBytes != 0 {
		t.Fatal("rejected incoming import left partial state")
	}
	target.Close()
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "134217728")
	target, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	for _, want := range []int64{1, 0} {
		if n, err := target.ImportArchive(archive); err != nil || n != want {
			t.Fatalf("import retry failed after raising capacity: %d %v", n, err)
		}
	}
}
