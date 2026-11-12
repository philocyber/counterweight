package traffic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostCleanupPreservesSharedBlobStorage(t *testing.T) {
	for _, mode := range []string{"exact", "archive", "substring"} {
		t.Run(mode, func(t *testing.T) {
			tr, dir := openTraffic(t)
			body := []byte(strings.Repeat("retained research evidence\n", maxInlineBody/20+100))
			tr.record(newFlow("retained.example.com", "GET", "/evidence", nil, body))
			id := onlyExchangeID(t, tr)
			var hash string
			if err := tr.DB().QueryRow(`SELECT resp_blob FROM exchange_bodies WHERE id=?`, id).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			if len(hash) != 64 {
				t.Fatal("fixture did not spill its response to shared storage")
			}
			blob := filepath.Join(dir, "_blobs", "sha256", hash[:2], hash+".bin")
			// An asset-derived reserved host is allowed to have index rows; it
			// must never own the shared directory used by unrelated exchanges.
			tr.record(newFlow("_blobs", "GET", "/", nil, []byte("delete this row")))
			var deleted int64
			var err error
			if mode == "substring" {
				deleted, err = tr.DeleteHost("_blobs")
			} else {
				var stage *HostDeleteStage
				if mode == "archive" {
					stage, err = tr.StageDeleteHostsExactForArchive([]string{"_blobs"}, 41, 42)
				} else {
					stage, err = tr.StageDeleteHostsExact([]string{"_blobs"})
				}
				if err == nil {
					deleted = stage.Deleted()
					err = stage.Commit()
				}
			}
			if err != nil || deleted != 1 {
				t.Fatalf("deleted=%d err=%v", deleted, err)
			}
			if got, err := os.ReadFile(blob); err != nil || string(got) != string(body) {
				t.Fatalf("unrelated blob lost: %v", err)
			}
			_, response, err := tr.Get(id)
			if err != nil || !strings.Contains(response, "@blob sha256:"+hash) {
				t.Fatalf("retained evidence unreadable: %v", err)
			}
			if n, err := tr.Count(); err != nil || n != 1 {
				t.Fatalf("remaining rows=%d err=%v", n, err)
			}
		})
	}
}

func TestReservedHostCleanupDoesNotStageStoreDirectories(t *testing.T) {
	tr, dir := openTraffic(t)
	for _, host := range []string{"_blobs", "_BLOBS", "/blobs", "\\blobs", "_index", "_ca", "_delete_staging", ".", "..", "... ", " "} {
		stage, err := tr.StageDeleteHostsExact([]string{host})
		if err != nil {
			t.Fatalf("host=%q: %v", host, err)
		}
		if len(stage.moves) != 0 {
			t.Errorf("host=%q staged store or parent directory", host)
		}
		if err := stage.Rollback(); err != nil {
			t.Fatalf("host=%q rollback: %v", host, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "_index")); err != nil {
		t.Fatal(err)
	}
}
