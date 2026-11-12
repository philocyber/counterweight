package traffic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// A synthetic valid archive uses a sparse zero body, avoiding a body-sized
// fixture allocation before measuring the actual importer.
func largeBlobArchive(t *testing.T, size int64) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	zero := make([]byte, 64<<10)
	for remaining := size; remaining > 0; {
		n := min(remaining, int64(len(zero)))
		_, _ = digest.Write(zero[:n])
		remaining -= n
	}
	hash := hex.EncodeToString(digest.Sum(nil))
	f, err := os.Create(filepath.Join(dir, "blobs", hash+".bin"))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(size)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("fixture body: %v %v", err, closeErr)
	}
	manifest, err := json.Marshal(ArchiveSnapshot{Version: 1, Blobs: []string{hash}, Exchanges: []ArchiveExchange{{ID: "streamed-blob", Host: "fixture.invalid", URL: "https://fixture.invalid/", RespBlob: hash, RespLen: int(size)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "traffic.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestArchiveBlobImportBoundedAllocation(t *testing.T) {
	const size int64 = 64 << 20
	archive := largeBlobArchive(t, size)
	for _, limit := range []int64{1, size} {
		t.Run(strconv.FormatInt(limit, 10), func(t *testing.T) {
			t.Setenv("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES", strconv.FormatInt(limit, 10))
			tr, err := Open(t.TempDir(), "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer tr.Close()
			for attempt := 0; attempt < 2; attempt++ {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				n, err := tr.ImportArchive(archive)
				runtime.ReadMemStats(&after)
				if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
					t.Errorf("import allocated %d bytes for a %d-byte body (limit=%d)", allocated, size, limit)
				}
				t.Logf("attempt=%d allocated=%d bytes", attempt, after.TotalAlloc-before.TotalAlloc)
				if limit == 1 {
					if n != 0 || !errors.Is(err, ErrBlobBudget) || tr.BlobStorageStatus().UsedBytes != 0 {
						t.Fatalf("rejected import changed state: %d %v %+v", n, err, tr.BlobStorageStatus())
					}
				} else if err != nil || n != int64(1-attempt) || tr.BlobStorageStatus().UsedBytes != size {
					t.Fatalf("stream/retry: %d %v %+v", n, err, tr.BlobStorageStatus())
				}
			}
		})
	}
}

func TestArchiveBlobCopyRejectsChangedOrCorruptSource(t *testing.T) {
	digest := sha256.Sum256([]byte("body"))
	for _, fixture := range []struct {
		name, content, hash string
		size                int64
	}{
		{"grown", "body-extra", hex.EncodeToString(digest[:]), 4},
		{"shrunk", "bod", hex.EncodeToString(digest[:]), 4},
		{"corrupt", "evil", hex.EncodeToString(digest[:]), 4},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := copyVerifiedArchiveBlob(&output, bytes.NewBufferString(fixture.content), fixture.size, fixture.hash); err == nil {
				t.Fatal("invalid source accepted")
			}
			if int64(output.Len()) > fixture.size {
				t.Fatal("temporary output exceeded the admitted source size")
			}
		})
	}
}

func TestArchiveBlobCorruptionCleanupAndRetry(t *testing.T) {
	archive := largeBlobArchive(t, 1<<20)
	files, err := filepath.Glob(filepath.Join(archive, "blobs", "*.bin"))
	if err != nil || len(files) != 1 {
		t.Fatalf("fixture blob: %v %v", files, err)
	}
	f, err := os.OpenFile(files[0], os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{1}, 0); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	dir := t.TempDir()
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if n, err := tr.ImportArchive(archive); n != 0 || err == nil {
		t.Fatalf("corrupt archive accepted: %d %v", n, err)
	}
	if count, err := tr.Count(); count != 0 || err != nil || tr.BlobStorageStatus().UsedBytes != 0 {
		t.Fatalf("corrupt import retained rows/blob usage: %d %v %+v", count, err, tr.BlobStorageStatus())
	}
	err = filepath.WalkDir(filepath.Join(dir, "_blobs"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			t.Errorf("failed import retained file: %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err = os.OpenFile(files[0], os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := f.WriteAt([]byte{0}, 0); n != 1 || err != nil {
		f.Close()
		t.Fatal("fixture repair failed")
	}
	f.Close()
	for _, want := range []int64{1, 0} {
		if n, err := tr.ImportArchive(archive); n != want || err != nil {
			t.Fatalf("valid retry: %d %v", n, err)
		}
	}
}
