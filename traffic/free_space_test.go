package traffic

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestFreeReserveStopsCaptureWithoutChangingResponseOrSavedData(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr.record(newFlow("reserve.fixture", "GET", "/saved", nil, []byte("saved")))
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("seed: %d %v", n, err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_FREE_BYTES", strconv.FormatInt(math.MaxInt64, 10))
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := tr.checkFreeSpace(1); !errors.Is(err, ErrFreeReserve) {
		t.Fatalf("native free-space query did not block: %v", err)
	}
	body := bytes.Repeat([]byte{0x7b}, maxInlineBody+1)
	flow := newFlow("reserve.fixture", "GET", "/blocked", nil, body)
	tr.record(flow)
	if !bytes.Equal(flow.Response.Body, body) {
		t.Fatal("reserve changed target response")
	}
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("reserve admitted capture or lost saved rows: %d %v", n, err)
	}
	if status := tr.BlobStorageStatus(); status.UsedBytes != 0 || status.DroppedCaptures != 1 {
		t.Fatalf("rejected capture allocated body or omitted drop: %+v", status)
	}
	if n, err := tr.ExportHosts([]string{"reserve.fixture"}, t.TempDir()); err != nil || n != 1 {
		t.Fatalf("reserve made evidence unexportable: %d %v", n, err)
	}
	if n, _, err := tr.DeleteAll(); err != nil || n != 1 {
		t.Fatalf("reserve prevented operator reclamation: %d %v", n, err)
	}
}

func TestFreeReserveRecheckedDuringNativeCaptureGrowth(t *testing.T) {
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.record(newFlow("growth.fixture", "GET", "/saved", nil, []byte("saved")))
	if _, err := tr.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	var growths atomic.Int32
	original := tr.walVFS.growthCheck
	tr.walVFS.growthCheck = func(additional int64) error {
		if tr.writeReserveActive.Load() && growths.Add(1) > 1 {
			// Actual native volume query with an impossible reserve models free
			// space disappearing after admission and the first native write.
			low := &Traffic{dir: dir, freeReserve: math.MaxInt64}
			return low.checkFreeGrowth(additional)
		}
		return original(additional)
	}
	body := bytes.Repeat([]byte{0x7b}, maxInlineBody+1)
	flow := newFlow("growth.fixture", "POST", "/blocked", nil, body)
	tr.record(flow)
	if growths.Load() < 2 {
		t.Fatal("fixture never crossed an in-flight native allocation")
	}
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("partial capture committed or seed lost: %d %v", n, err)
	}
	if status := tr.BlobStorageStatus(); status.UsedBytes != 0 || status.DroppedCaptures != 1 {
		t.Fatalf("failed transaction leaked bodies or omitted drop: %+v", status)
	}
	if !bytes.Equal(flow.Response.Body, body) {
		t.Fatal("storage failure changed target response")
	}
	tr.walVFS.growthCheck = original
	tr.record(newFlow("growth.fixture", "GET", "/retry", nil, []byte("retry")))
	if n, err := tr.Count(); err != nil || n != 2 {
		t.Fatalf("capacity retry failed: %d %v", n, err)
	}
	if n, _, err := tr.DeleteAll(); err != nil || n != 2 {
		t.Fatalf("operator cleanup unavailable: %d %v", n, err)
	}
}

type disappearingReserveWriter struct {
	t *Traffic
	bytes.Buffer
	calls int
}

func TestFreeReserveRecheckedDuringNativeImportGrowth(t *testing.T) {
	source, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.record(newFlow("import-growth.fixture", "GET", "/", nil, bytes.Repeat([]byte{0x7b}, maxInlineBody+1)))
	archive := t.TempDir()
	if n, err := source.ExportHosts([]string{"import-growth.fixture"}, archive); err != nil || n != 1 {
		t.Fatalf("export: %d %v", n, err)
	}
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.record(newFlow("saved.fixture", "GET", "/", nil, []byte("saved")))
	if _, err := tr.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	var growths atomic.Int32
	original := tr.walVFS.growthCheck
	tr.walVFS.growthCheck = func(additional int64) error {
		if tr.writeReserveActive.Load() && growths.Add(1) > 1 {
			return (&Traffic{dir: dir, freeReserve: math.MaxInt64}).checkFreeGrowth(additional)
		}
		return original(additional)
	}
	if n, err := tr.ImportArchive(archive); n != 0 || !isSQLiteFull(err) {
		t.Fatalf("in-flight import escaped reserve: %d %v", n, err)
	}
	if growths.Load() < 2 {
		t.Fatal("import did not reach in-flight allocation")
	}
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("partial import committed or seed lost: %d %v", n, err)
	}
	if status := tr.BlobStorageStatus(); status.UsedBytes != 0 {
		t.Fatalf("failed import leaked new body: %+v", status)
	}
	tr.walVFS.growthCheck = original
	if n, err := tr.ImportArchive(archive); err != nil || n != 1 {
		t.Fatalf("capacity retry failed: %d %v", n, err)
	}
	if n, err := tr.Count(); err != nil || n != 2 {
		t.Fatalf("retry lost data: %d %v", n, err)
	}
}

func (w *disappearingReserveWriter) Write(body []byte) (int, error) {
	n, err := w.Buffer.Write(body)
	w.calls++
	w.t.freeReserve = math.MaxInt64
	return n, err
}

func TestFreeReserveWriterStopsBeforeNextChunk(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	output := &disappearingReserveWriter{t: tr}
	writer := reserveWriter{traffic: tr, writer: output}
	body := bytes.Repeat([]byte("x"), 64<<10)
	if n, err := writer.Write(body); err != nil || n != len(body) {
		t.Fatalf("first chunk: %d %v", n, err)
	}
	if n, err := writer.Write(body); n != 0 || !errors.Is(err, ErrFreeReserve) {
		t.Fatalf("second chunk consumed reserve: %d %v", n, err)
	}
	if output.calls != 1 || output.Len() != len(body) {
		t.Fatal("denied chunk reached storage")
	}
}

func TestFreeReserveRejectsImportAndPermitsCapacityRetry(t *testing.T) {
	source, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.record(newFlow("reserve-import.fixture", "GET", "/", nil, bytes.Repeat([]byte{0x7b}, maxInlineBody+1)))
	archive := t.TempDir()
	if n, err := source.ExportHosts([]string{"reserve-import.fixture"}, archive); err != nil || n != 1 {
		t.Fatalf("archive: %d %v", n, err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_FREE_BYTES", strconv.FormatInt(math.MaxInt64, 10))
	dir := t.TempDir()
	target, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := target.ImportArchive(archive); n != 0 || !errors.Is(err, ErrFreeReserve) {
		t.Fatalf("low-space import admitted: %d %v", n, err)
	}
	if status := target.BlobStorageStatus(); status.UsedBytes != 0 {
		t.Fatalf("low-space import allocated bodies: %+v", status)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_FREE_BYTES", "1")
	target, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if n, err := target.ImportArchive(archive); err != nil || n != 1 {
		t.Fatalf("capacity retry: %d %v", n, err)
	}
}

func TestTrafficAvailableBytesAndReserveConfiguration(t *testing.T) {
	dir := t.TempDir()
	if available, err := trafficAvailableBytes(dir); err != nil || available == 0 {
		t.Fatalf("native available bytes: %d %v", available, err)
	}
	if _, err := trafficAvailableBytes(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("invalid volume path reported usable space")
	}
	for _, value := range []string{"0", "-1", "invalid", "9223372036854775808"} {
		t.Setenv("COUNTERWEIGHT_TRAFFIC_FREE_BYTES", value)
		if _, err := freeReserveFromEnv(); err == nil {
			t.Fatalf("invalid reserve accepted: %s", value)
		}
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_FREE_BYTES", "")
	if reserve, err := freeReserveFromEnv(); err != nil || reserve != defaultFreeReserve {
		t.Fatalf("default reserve: %d %v", reserve, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
