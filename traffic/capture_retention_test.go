package traffic

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCaptureRetentionPersistsAcrossRestartAndLoweredLimit(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES", "2")
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"one.fixture", "two.fixture", "three.fixture"} {
		tr.record(newFlow(host, "GET", "/", nil, nil))
	}
	if n, err := tr.Count(); err != nil || n != 2 {
		t.Fatalf("retention limit bypassed: %d %v", n, err)
	}
	if status := tr.BlobStorageStatus(); status.DroppedCaptures != 1 {
		t.Fatalf("missing retention drop: %+v", status)
	}
	if n, err := tr.ExportHosts([]string{"one.fixture", "two.fixture"}, t.TempDir()); err != nil || n != 2 {
		t.Fatalf("retention removed saved evidence: %d %v", n, err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES", "1")
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.record(newFlow("new.fixture", "GET", "/", nil, nil))
	if n, err := tr.Count(); err != nil || n != 2 {
		t.Fatalf("lowered limit evicted or admitted evidence: %d %v", n, err)
	}
	if n, err := tr.DeleteHost("one.fixture"); err != nil || n != 1 {
		t.Fatalf("delete: %d %v", n, err)
	}
	tr.record(newFlow("new.fixture", "GET", "/still-full", nil, nil))
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("capture continued at retention boundary: %d %v", n, err)
	}
	if n, _, err := tr.DeleteAll(); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	tr.record(newFlow("new.fixture", "GET", "/released", nil, nil))
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("deletion did not release retention capacity: %d %v", n, err)
	}
}

func TestCaptureRetentionSerializesConcurrentAdmissions(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES", "7")
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	var workers sync.WaitGroup
	for n := 0; n < 40; n++ {
		workers.Add(1)
		go func(n int) {
			defer workers.Done()
			tr.record(newFlow("host"+strconv.Itoa(n)+".fixture", "GET", "/", nil, nil))
		}(n)
	}
	workers.Wait()
	if n, err := tr.Count(); err != nil || n != 7 {
		t.Fatalf("parallel retention overshoot: %d %v", n, err)
	}
	if status := tr.BlobStorageStatus(); status.DroppedCaptures != 33 {
		t.Fatalf("parallel drop accounting: %+v", status)
	}
}

func TestCaptureRetentionCountsExplicitlyRestoredEvidence(t *testing.T) {
	source, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for n := 0; n < 3; n++ {
		source.record(newFlow("restore.fixture", "GET", "/"+strconv.Itoa(n), nil, nil))
	}
	archive := t.TempDir()
	if n, err := source.ExportHosts([]string{"restore.fixture"}, archive); err != nil || n != 3 {
		t.Fatalf("export: %d %v", n, err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES", "1")
	target, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if n, err := target.ImportArchive(archive); err != nil || n != 3 {
		t.Fatalf("explicit restore lost evidence: %d %v", n, err)
	}
	target.record(newFlow("different.fixture", "GET", "/", nil, nil))
	if n, err := target.Count(); err != nil || n != 3 {
		t.Fatalf("restore reset capture allowance: %d %v", n, err)
	}
	if status := target.BlobStorageStatus(); status.DroppedCaptures != 1 {
		t.Fatalf("restored retention drop missing: %+v", status)
	}
}

func TestCaptureRetentionRealProxyPreservesResponseAtCapacity(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES", "2")
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte("tiny response")) }))
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
	finished := make(chan error, 1)
	go func() { finished <- tr.Start() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		tr.proxy.Shutdown(ctx)
		select {
		case <-finished:
		case <-ctx.Done():
			t.Error("proxy did not stop")
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
	for n := 0; n < 3; n++ {
		response, err := client.Get(target.URL + "/" + strconv.Itoa(n))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(body) != "tiny response" {
			t.Fatalf("quota changed wire response: %q %v", body, err)
		}
	}
	if n, err := tr.Count(); err != nil || n != 2 || requests.Load() != 3 {
		t.Fatalf("wire retention/forwarding changed: %d %d %v", n, requests.Load(), err)
	}
	if status := tr.BlobStorageStatus(); status.DroppedCaptures != 1 {
		t.Fatalf("wire retention drop missing: %+v", status)
	}
}

func TestCaptureRetentionConfiguration(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES", "")
	if limit, err := captureRetentionFromEnv(); err != nil || limit != 100000 {
		t.Fatalf("default: %d %v", limit, err)
	}
	for _, raw := range []string{"0", "-1", "text", "9223372036854775808"} {
		t.Setenv("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES", raw)
		if tr, err := Open(t.TempDir(), "127.0.0.1:0"); err == nil {
			tr.Close()
			t.Fatalf("invalid row limit accepted: %s", raw)
		}
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES", "1")
	if limit, err := captureRetentionFromEnv(); err != nil || limit != 1 {
		t.Fatalf("positive row count: %d %v", limit, err)
	}
}
