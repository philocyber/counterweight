package traffic

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/locale"
)

func TestArchiveManifestRejectsOversizeBeforeAllocation(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES", "1048576")
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "traffic.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(f, `{"version":1,"exchanges":[],"blobs":[]}`)
	padding := bytes.Repeat([]byte{' '}, 64<<10)
	for n := 0; n < 128 && err == nil; n++ {
		_, err = f.Write(padding)
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("fixture write: %v %v", err, closeErr)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	n, err := tr.ImportArchive(dir)
	runtime.ReadMemStats(&after)
	if n != 0 || !errors.Is(err, ErrIndexBudget) {
		t.Errorf("oversize manifest accepted or wrong error: %d %v", n, err)
	}
	if err != nil && !strings.Contains(locale.ErrorMessage(locale.En, err), "limit") {
		t.Error("manifest budget failure lost localization")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2<<20 {
		t.Errorf("oversize manifest allocated %d bytes before rejection", allocated)
	}
	t.Logf("rejected manifest allocated=%d bytes", after.TotalAlloc-before.TotalAlloc)
	if count, err := tr.Count(); count != 0 || err != nil || tr.BlobStorageStatus().UsedBytes != 0 {
		t.Fatalf("oversize metadata mutated storage: %d %v", count, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "traffic.json"), []byte(`{"version":1,"exchanges":[],"blobs":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if n, err := tr.ImportArchive(dir); n != 0 || err != nil {
		t.Fatalf("corrected manifest retry: %d %v", n, err)
	}
}

func TestArchiveManifestReadBoundariesAndCompatibility(t *testing.T) {
	const valid = `{"version":1,"exchanges":[],"blobs":[],"future":{"value":"compatible"}}`
	for _, fixture := range []struct {
		name, input string
		limit       int64
		wantError   bool
		wantBudget  bool
	}{
		{"exact", valid, int64(len(valid)), false, false},
		{"whitespace-exact", valid + " \n", int64(len(valid) + 2), false, false},
		{"duplicate-field-last-wins", `{"version":2,"version":1,"blobs":[],"exchanges":[]}`, 128, false, false},
		{"grew-body", valid, int64(len(valid) - 1), true, true},
		{"grew-whitespace", valid + " ", int64(len(valid)), true, true},
		{"second-object", valid + `{}`, int64(len(valid) + 2), true, false},
		{"second-scalar", valid + `true`, int64(len(valid) + 4), true, false},
		{"invalid-tail", valid + `!`, int64(len(valid) + 1), true, false},
		{"invalid-body", `{`, 1, true, false},
		{"empty", "", 0, true, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			snapshot, err := decodeArchiveSnapshot(strings.NewReader(fixture.input), fixture.limit)
			if (err != nil) != fixture.wantError || errors.Is(err, ErrIndexBudget) != fixture.wantBudget {
				t.Fatalf("wrong boundary result: %+v %v", snapshot, err)
			}
			if err == nil && snapshot.Version != 1 {
				t.Fatal("compatible manifest changed")
			}
		})
	}
}

func TestArchiveManifestRejectsDecodedCollectionAmplification(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES", "1048576")
	tr, err := Open(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	dir := t.TempDir()
	// This valid JSON is smaller than the raw-byte cap. Tiny objects expand
	// into full ArchiveExchange values before the old importer checks capacity.
	manifest := `{"version":1,"exchanges":[` + strings.Repeat(`{},`, 49999) + `{}],"blobs":[]}`
	if err := os.WriteFile(filepath.Join(dir, "traffic.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	n, err := tr.ImportArchive(dir)
	runtime.ReadMemStats(&after)
	if n != 0 || !errors.Is(err, ErrIndexBudget) {
		t.Fatalf("amplified manifest was admitted: %d %v", n, err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("raw=%d allocated=%d bytes", len(manifest), allocated)
	if allocated > 8<<20 {
		t.Errorf("decoded collection allocated %d bytes before admission", allocated)
	}
	if count, err := tr.Count(); count != 0 || err != nil || tr.BlobStorageStatus().UsedBytes != 0 {
		t.Fatalf("failed metadata admission changed storage: %d %v", count, err)
	}
}

func TestArchiveManifestProgressiveDecodePreservesFormat(t *testing.T) {
	for _, input := range []string{
		`{"VERSION":1,"EXCHANGES":null,"BLOBS":null}`,
		`{"version":1,"version":null,"exchanges":[null],"blobs":[null]}`,
		`{"version":1,"exchanges":[{"id":"old"}],"exchanges":[{"id":"new","req_body":"YQ=="}],"blobs":["old"],"blobs":["new"]}`,
		`{"version":1,"future":{"number":1e1000,"data":[[],{},null,true]},"exchanges":[],"blobs":[]}`,
		`{"version":1,"exchanges":[],"exchanges":null,"blobs":[],"blobs":null}`,
		`null`,
		`{"version":1,"exchanges":{}}`,
		`{"version":1,"blobs":[42]}`,
		`{"version":1,"exchanges":[{"req_body":"not base64"}]}`,
		`{"version":1,"future":[{},]}`,
	} {
		t.Run(input, func(t *testing.T) {
			var expected ArchiveSnapshot
			priorErr := json.Unmarshal([]byte(input), &expected)
			actual, err := decodeArchiveSnapshot(strings.NewReader(input), 1<<20)
			if (err != nil) != (priorErr != nil) || err == nil && !reflect.DeepEqual(actual, expected) {
				t.Fatalf("archive format changed: prior=%+v %v actual=%+v %v", expected, priorErr, actual, err)
			}
		})
	}
}
