package traffic

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func TestExchangeCaptureLimitPreservesResponseAndSavedEvidence(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_EXCHANGE_BYTES", "4096")
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.record(newFlow("quota.fixture", "GET", "/saved", nil, []byte("saved")))
	body := bytes.Repeat([]byte{0x7b}, maxInlineBody+1)
	flow := newFlow("quota.fixture", "GET", "/large", nil, body)
	tr.record(flow)
	if !bytes.Equal(flow.Response.Body, body) {
		t.Fatal("capture limit changed target response")
	}
	if n, err := tr.Count(); n != 1 || err != nil {
		t.Fatalf("saved evidence lost or oversized capture admitted: %d %v", n, err)
	}
	if status := tr.BlobStorageStatus(); status.UsedBytes != 0 || status.DroppedCaptures != 1 {
		t.Fatalf("capture allocated a blob before quota check: %+v", status)
	}
	if n, err := tr.ExportHosts([]string{"quota.fixture"}, t.TempDir()); n != 1 || err != nil {
		t.Fatalf("saved export: %d %v", n, err)
	}
}

func TestHostCaptureLimitDedupRestartDeletionAndIndependentHosts(t *testing.T) {
	body := bytes.Repeat([]byte{0x7b}, maxInlineBody+1)
	// Enough for two full bodies and metadata, but not a third shared body.
	t.Setenv("COUNTERWEIGHT_TRAFFIC_HOST_BYTES", strconv.Itoa(2*len(body)+8192))
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr.record(newFlow("quota.fixture:80", "GET", "/one", nil, body))
	tr.record(newFlow("quota.fixture:443", "GET", "/two", nil, body))
	if status := tr.BlobStorageStatus(); status.UsedBytes != int64(len(body)) {
		t.Fatalf("fixture did not deduplicate: %+v", status)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.record(newFlow("QUOTA.fixture.:8080", "GET", "/three", nil, body))
	tr.record(newFlow("other.fixture", "GET", "/one", nil, body))
	if n, err := tr.Count(); n != 3 || err != nil {
		t.Fatalf("host limit reset, alias bypass or unrelated host blocked: %d %v", n, err)
	}
	if status := tr.BlobStorageStatus(); status.DroppedCaptures != 1 {
		t.Fatalf("missing host drop: %+v", status)
	}
	if n, err := tr.DeleteHost("quota.fixture"); n != 2 || err != nil {
		t.Fatalf("delete: %d %v", n, err)
	}
	tr.record(newFlow("quota.fixture", "GET", "/after-delete", nil, body))
	if n, err := tr.Count(); n != 2 || err != nil {
		t.Fatalf("deletion did not release allowance: %d %v", n, err)
	}
}

func TestCaptureLimitConfiguration(t *testing.T) {
	for _, name := range []string{"COUNTERWEIGHT_TRAFFIC_EXCHANGE_BYTES", "COUNTERWEIGHT_TRAFFIC_HOST_BYTES"} {
		t.Run(name, func(t *testing.T) {
			for _, raw := range []string{"0", "-1", "text", "9223372036854775808"} {
				t.Setenv(name, raw)
				if tr, err := Open(t.TempDir(), "127.0.0.1:0"); err == nil {
					tr.Close()
					t.Fatalf("accepted invalid limit %q", raw)
				}
			}
		})
	}
}

func TestCaptureLimitCountsHeadersAndRestoredEvidence(t *testing.T) {
	source, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.record(newFlow("restore-quota.fixture", "GET", "/saved", nil, bytes.Repeat([]byte{1}, 2048)))
	archive := t.TempDir()
	if n, err := source.ExportHosts([]string{"restore-quota.fixture"}, archive); n != 1 || err != nil {
		t.Fatalf("export: %d %v", n, err)
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_EXCHANGE_BYTES", "4096")
	t.Setenv("COUNTERWEIGHT_TRAFFIC_HOST_BYTES", "4096")
	target, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if n, err := target.ImportArchive(archive); n != 1 || err != nil {
		t.Fatalf("restore: %d %v", n, err)
	}
	target.record(newFlow("restore-quota.fixture", "GET", "/new", nil, bytes.Repeat([]byte{2}, 2048)))
	flow := newFlow("headers.fixture", "GET", "/", nil, nil)
	flow.Response.Header.Set("X-Large", strings.Repeat("x", 4096))
	target.record(flow)
	if n, err := target.Count(); n != 1 || err != nil {
		t.Fatalf("restored evidence or header limit bypass: %d %v", n, err)
	}
	if status := target.BlobStorageStatus(); status.DroppedCaptures != 2 {
		t.Fatalf("missing quota drops: %+v", status)
	}
}
