package traffic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type archiveZeros struct{}

func (archiveZeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestExportHostsStreamsMultipleExchanges(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	for _, row := range []struct {
		id, host string
		ts       int
	}{
		{"later", "stream.example", 2}, {"first-b", "stream.example", 1},
		{"first-a", "stream.example", 1}, {"foreign", "foreign.example", 0},
	} {
		if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,'GET','/','https://fixture/',200,'text/plain',3,3,'')`, row.id, row.ts, row.host); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.DB().Exec(`INSERT INTO exchange_bodies(id,req_head,req_body,resp_head,resp_body)
VALUES(?,'request',?,'response',?)`, row.id, []byte{0, 255, 1}, []byte("body")); err != nil {
			t.Fatal(err)
		}
	}
	destination := t.TempDir()
	count, err := tr.ExportHosts([]string{" STREAM.EXAMPLE ", "stream.example"}, destination)
	if err != nil || count != 3 {
		t.Fatalf("export count=%d err=%v", count, err)
	}
	file, err := os.Open(filepath.Join(destination, "traffic.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var snapshot ArchiveSnapshot
	if err := json.NewDecoder(file).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Exchanges) != 3 || len(snapshot.Blobs) != 0 {
		t.Fatal("streamed archive membership changed")
	}
	for i, id := range []string{"first-a", "first-b", "later"} {
		item := snapshot.Exchanges[i]
		if item.ID != id || string(item.ReqBody) != string([]byte{0, 255, 1}) || string(item.RespBody) != "body" {
			t.Fatalf("streamed ordering/raw bytes changed at %d", i)
		}
	}
}

func TestExportHostsStreamsLargeBlob(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	const size = 16 * 1024 * 1024
	file, err := os.CreateTemp(t.TempDir(), "blob")
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(file, h), archiveZeros{}, size); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	hash := hex.EncodeToString(h.Sum(nil))
	stored := filepath.Join(tr.dir, "_blobs", "sha256", hash[:2], hash+".bin")
	if err := os.MkdirAll(filepath.Dir(stored), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file.Name(), stored); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES('stream-blob',1,'stream.example','POST','/fixture','https://stream.example/fixture',200,'application/octet-stream',?,?,'')`, size, size); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.DB().Exec(`INSERT INTO exchange_bodies(id,req_head,req_body,req_blob,resp_head,resp_body,resp_blob)
VALUES('stream-blob','request',?,?,'response',?,?)`, []byte("preview"), hash, []byte("preview"), hash); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	count, err := tr.ExportHosts([]string{"stream.example"}, destination)
	runtime.ReadMemStats(&after)
	if err != nil || count != 1 {
		t.Fatalf("export count=%d err=%v", count, err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated >= 4*1024*1024 {
		t.Fatalf("16 MiB blob was buffered during export: allocated %d bytes", allocated)
	}
	t.Logf("16 MiB export allocated %d bytes", allocated)
	manifest, err := os.ReadFile(filepath.Join(destination, "traffic.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot ArchiveSnapshot
	if err := json.Unmarshal(manifest, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 || len(snapshot.Exchanges) != 1 || len(snapshot.Blobs) != 1 || snapshot.Blobs[0] != hash {
		t.Fatalf("archive compatibility/deduplication changed: %+v", snapshot)
	}
	body, err := os.Open(filepath.Join(destination, "blobs", hash+".bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	h.Reset()
	n, err := io.Copy(h, body)
	if err != nil || n != size || hex.EncodeToString(h.Sum(nil)) != hash {
		t.Fatal("streamed blob content changed")
	}
}

func TestExportHostsFailureKeepsPublishedManifest(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	hash := strings.Repeat("a", 64)
	if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES('missing-blob',1,'missing.example','GET','/','https://missing.example/',200,'text/plain',0,10,'')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.DB().Exec(`INSERT INTO exchange_bodies(id,req_head,req_body,resp_head,resp_body,resp_blob)
VALUES('missing-blob','',X'','',X'',?)`, hash); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	manifest := filepath.Join(destination, "traffic.json")
	const original = "unchanged previous manifest"
	if err := os.WriteFile(manifest, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.ExportHosts([]string{"missing.example"}, destination); err == nil {
		t.Fatal("missing body produced a successful export")
	}
	data, err := os.ReadFile(manifest)
	if err != nil || string(data) != original {
		t.Fatal("failed export replaced the published manifest")
	}
	leftovers, err := filepath.Glob(filepath.Join(destination, ".traffic-manifest-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatal("failed export left a partial temporary manifest")
	}
}

func TestExportSnapshotAllowsCaptureWhileDeletionWaits(t *testing.T) {
	for _, action := range []string{"substring", "all", "stage-commit", "stage-rollback", "recovery"} {
		t.Run(action, func(t *testing.T) {
			tr, err := Open(t.TempDir(), "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer tr.Close()
			const host = "snapshot.example"
			body := []byte(strings.Repeat("immutable body ", maxInlineBody/10))
			tr.record(newFlow(host, "GET", "/old", nil, body))
			legacy := filepath.Join(tr.dir, host, "legacy")
			if err := os.MkdirAll(legacy, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(legacy, "request.http"), []byte("legacy request"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := tr.DB().Exec(`INSERT INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES('legacy',1,?,'GET','/legacy','https://snapshot.example/legacy',200,'text/plain',0,0,?)`, host, filepath.Join(host, "legacy")); err != nil {
				t.Fatal(err)
			}
			destination := t.TempDir()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			exported := make(chan error, 1)
			defer func() {
				unblock()
				select {
				case <-exported:
				case <-time.After(5 * time.Second):
					t.Error("export goroutine did not exit during cleanup")
				}
			}()
			go func() {
				defer close(exported)
				_, err := tr.exportHosts([]string{host}, destination, func(source, destination string) error {
					close(entered)
					<-release
					return copyArchiveBlob(source, destination)
				})
				exported <- err
			}()
			select {
			case <-entered:
			case err := <-exported:
				t.Fatalf("export failed before blob copy: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("export never reached blob copy")
			}
			deleted := make(chan error, 1)
			defer func() {
				unblock()
				select {
				case <-deleted:
				case <-time.After(5 * time.Second):
					t.Error("deletion goroutine did not exit during cleanup")
				}
			}()
			go func() {
				defer close(deleted)
				var err error
				switch action {
				case "substring":
					_, err = tr.DeleteHost(host)
				case "all":
					_, _, err = tr.DeleteAll()
				case "recovery":
					err = tr.RecoverHostDeleteStages(nil)
				default:
					var stage *HostDeleteStage
					stage, err = tr.StageDeleteHostsExact([]string{host})
					if err == nil {
						if action == "stage-commit" {
							err = stage.Commit()
						} else {
							err = stage.Rollback()
						}
					}
				}
				deleted <- err
			}()
			select {
			case err := <-deleted:
				t.Fatalf("destructive operation bypassed active export: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			captured := make(chan struct{})
			defer func() {
				unblock()
				select {
				case <-captured:
				case <-time.After(5 * time.Second):
					t.Error("capture goroutine did not exit during cleanup")
				}
			}()
			go func() {
				tr.record(newFlow(host, "GET", "/new", nil, []byte(strings.Repeat("new capture ", maxInlineBody/8))))
				close(captured)
			}()
			select {
			case <-captured:
			case <-time.After(3 * time.Second):
				unblock()
				t.Fatal("stalled export or waiting deletion blocked capture")
			}
			if n, err := tr.Count(); err != nil || n != 3 {
				t.Fatalf("capture did not persist before export completed: count=%d err=%v", n, err)
			}
			unblock()
			for _, done := range []chan error{exported, deleted} {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("export/deletion did not finish after releasing output")
				}
			}
			file, err := os.Open(filepath.Join(destination, "traffic.json"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			var snapshot ArchiveSnapshot
			if err := json.NewDecoder(file).Decode(&snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Exchanges) != 2 || snapshot.Exchanges[0].ReqHead != "legacy request" || len(snapshot.Blobs) != 1 {
				t.Fatal("export snapshot changed membership or lost legacy/blob data")
			}
			copied, err := os.ReadFile(filepath.Join(destination, "blobs", snapshot.Blobs[0]+".bin"))
			if err != nil || string(copied) != string(body) {
				t.Fatal("concurrent deletion corrupted archived blob")
			}
			want := 0
			if action == "stage-rollback" || action == "recovery" {
				want = 3
			}
			if n, err := tr.Count(); err != nil || n != want {
				t.Fatalf("post-export deletion semantics changed: count=%d want=%d err=%v", n, want, err)
			}
		})
	}
}

func TestEmptyArchiveSnapshotExcludesLaterCapture(t *testing.T) {
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.archiveFiles.RLock()
	defer tr.archiveFiles.RUnlock()
	tx, err := tr.beginArchiveSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	tr.record(newFlow("later.example", "GET", "/", nil, []byte("later")))
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("empty snapshot included later writes: count=%d err=%v", n, err)
	}
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("capture after empty snapshot failed: count=%d err=%v", n, err)
	}
}
