package traffic

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/locale"
)

func TestBlobBudgetCaptureDedupRestartAndRecovery(t *testing.T) {
	body := bytes.Repeat([]byte{1}, maxInlineBody+1)
	t.Setenv("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES", strconv.Itoa(len(body)))
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr.record(newFlow("kept.example", "GET", "/one", nil, body))
	tr.record(newFlow("kept.example", "GET", "/two", nil, body))
	status := tr.BlobStorageStatus()
	if status.UsedBytes != int64(len(body)) {
		t.Fatalf("dedup consumes extra budget: %+v", status)
	}
	tr.Close()
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if tr.BlobStorageStatus().UsedBytes != int64(len(body)) {
		t.Fatal("restart lost physical blob usage")
	}
	other := bytes.Repeat([]byte{2}, len(body))
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); tr.record(newFlow("rejected.example", "GET", "/new", nil, other)) }()
	}
	workers.Wait()
	count, err := tr.Count()
	if err != nil || count != 2 {
		t.Fatalf("rejected capture created evidence rows: %d %v", count, err)
	}
	status = tr.BlobStorageStatus()
	if status.UsedBytes > status.LimitBytes || status.DroppedCaptures != 8 {
		t.Fatalf("concurrent quota bypass: %+v", status)
	}
	if _, err := tr.DeleteHost("kept.example"); err != nil {
		t.Fatal(err)
	}
	if tr.BlobStorageStatus().UsedBytes != 0 {
		t.Fatal("deleted blobs did not release capacity")
	}
	tr.record(newFlow("new.example", "GET", "/new", nil, other))
	if count, _ := tr.Count(); count != 1 {
		t.Fatal("capture did not recover after deletion")
	}
}

func TestBlobBudgetRollsBackBothCaptureBodies(t *testing.T) {
	body := bytes.Repeat([]byte{1}, maxInlineBody+1)
	t.Setenv("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES", strconv.Itoa(len(body)))
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.record(newFlow("target.example", "POST", "/", body, bytes.Repeat([]byte{2}, len(body))))
	if count, _ := tr.Count(); count != 0 || tr.BlobStorageStatus().UsedBytes != 0 {
		t.Fatal("response rejection left request body or exchange behind")
	}
	if _, err := tr.DB().Exec(`CREATE TRIGGER deny_capture BEFORE INSERT ON exchanges BEGIN SELECT RAISE(ABORT,'synthetic index failure'); END`); err != nil {
		t.Fatal(err)
	}
	tr.record(newFlow("target.example", "GET", "/", nil, body))
	if tr.BlobStorageStatus().UsedBytes != 0 {
		t.Fatal("index failure retained unreferenced blob")
	}
}

func TestBlobBudgetArchiveImportAtomicAndRetry(t *testing.T) {
	body := bytes.Repeat([]byte{1}, maxInlineBody+1)
	source, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.record(newFlow("archive.example", "GET", "/a", nil, body))
	source.record(newFlow("archive.example", "GET", "/b", nil, bytes.Repeat([]byte{2}, len(body))))
	archive := t.TempDir()
	if _, err := source.ExportHosts([]string{"archive.example"}, archive); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES", strconv.Itoa(len(body)))
	dir := t.TempDir()
	target, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ImportArchive(archive); !errors.Is(err, ErrBlobBudget) {
		t.Fatalf("oversized import accepted or wrong failure: %v", err)
	} else if !strings.Contains(locale.ErrorMessage(locale.En, err), "limit") {
		t.Fatal("storage budget error not localized")
	}
	if count, _ := target.Count(); count != 0 || target.BlobStorageStatus().UsedBytes != 0 {
		t.Fatal("failed import retained partial state")
	}
	target.Close()
	t.Setenv("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES", strconv.Itoa(2*len(body)))
	target, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	for _, want := range []int64{2, 0} {
		if n, err := target.ImportArchive(archive); err != nil || n != want {
			t.Fatalf("import/retry failed: %d %v", n, err)
		}
	}
	if target.BlobStorageStatus().UsedBytes != int64(2*len(body)) {
		t.Fatal("import retry charged duplicate bodies")
	}
	files, err := filepath.Glob(filepath.Join(dir, "_blobs", "sha256", "*", ".blob-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("atomic blob temporary files leaked")
	}
}

func TestBlobBudgetRejectsInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{"0", "-1", "not-a-number"} {
		t.Setenv("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES", raw)
		tr, err := Open(t.TempDir(), "127.0.0.1:0")
		if tr != nil {
			tr.Close()
		}
		if err == nil {
			t.Fatal("invalid byte limit accepted")
		}
	}
}

func TestBlobBudgetDoesNotBreakProxyResponse(t *testing.T) {
	testStorageAdmissionProxyResponse(t, false)
}

func TestWALAdmissionDoesNotBreakProxyResponse(t *testing.T) {
	testStorageAdmissionProxyResponse(t, true)
}

func testStorageAdmissionProxyResponse(t *testing.T, wal bool) {
	if wal {
		t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "524288")
	} else {
		t.Setenv("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES", "1")
	}
	body := bytes.Repeat([]byte{1}, maxInlineBody+1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(body)
	}))
	defer target.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	tr, err := Open(t.TempDir(), addr)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if wal {
		release := pinWALAtAdmission(t, tr)
		defer release()
	}
	droppedBeforeRequest := tr.BlobStorageStatus().DroppedCaptures
	finished := make(chan error, 1)
	go func() { finished <- tr.Start() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		tr.proxy.Shutdown(ctx)
		select {
		case <-finished:
		case <-ctx.Done():
			t.Error("proxy did not shut down")
		}
	}()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("proxy did not start")
		}
	}
	proxyURL, _ := url.Parse("http://" + addr)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !bytes.Equal(got, body) {
		t.Fatal("storage rejection altered target response")
	}
	if status := tr.BlobStorageStatus(); status.UsedBytes != 0 || status.DroppedCaptures != droppedBeforeRequest+1 {
		t.Fatalf("capture loss not reported: %+v", status)
	}
}
