package traffic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestTrafficPrivateCaptureRestoreAndLegacy(t *testing.T) {
	parent := t.TempDir()
	makePublicTrafficTestPath(t, parent)
	dir := filepath.Join(parent, "live")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "legacy-fixture.bin")
	legacyBytes := []byte("synthetic legacy evidence")
	if err := os.WriteFile(legacy, legacyBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(legacy, 0644); err != nil {
		t.Fatal(err)
	}
	assertTrafficUnrelatedRead(t, legacy, true)
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	body := bytes.Repeat([]byte("private capture\n"), maxInlineBody/16+64)
	if len(body) <= maxInlineBody {
		t.Fatal("fixture must exercise a spilled body")
	}
	tr.record(newFlow("privacy.fixture", "GET", "/body", nil, body))
	if count, err := tr.Count(); err != nil || count != 1 {
		t.Fatalf("capture workflow failed: %d %v", count, err)
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	blob := filepath.Join(dir, "_blobs", "sha256", hash[:2], hash+".bin")
	assertTrafficPrivateArtifact(t, blob, false, true)
	assertTrafficPrivateArtifact(t, legacy, false, true)
	for _, path := range []string{blob, legacy, filepath.Join(dir, "_index", "index.sqlite"), filepath.Join(dir, "_ca", "mitmproxy-ca.pem")} {
		assertTrafficUnrelatedRead(t, path, false)
	}
	checkTree := func(root string) {
		t.Helper()
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Dependency-created SQLite/CA files are independently checked on
			// Windows; on Unix their private ancestors deny unrelated readers.
			assertTrafficPrivateArtifact(t, path, entry.IsDir(), entry.IsDir())
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	checkTree(dir)
	archive := filepath.Join(parent, "export")
	if count, err := tr.ExportHosts([]string{"privacy.fixture"}, archive); err != nil || count != 1 {
		t.Fatalf("export workflow failed: %d %v", count, err)
	}
	assertTrafficPrivateArtifact(t, filepath.Join(archive, "traffic.json"), false, true)
	assertTrafficPrivateArtifact(t, filepath.Join(archive, "blobs", hash+".bin"), false, true)
	restoreDir := filepath.Join(parent, "restore")
	restored, err := Open(restoreDir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if count, err := restored.ImportArchive(archive); err != nil || count != 1 {
		t.Fatalf("restore workflow failed: %d %v", count, err)
	}
	restoredBlob := filepath.Join(restoreDir, "_blobs", "sha256", hash[:2], hash+".bin")
	assertTrafficPrivateArtifact(t, restoredBlob, false, true)
	assertTrafficUnrelatedRead(t, restoredBlob, false)
	checkTree(restoreDir)
	caPath := filepath.Join(dir, "_ca", "mitmproxy-ca.pem")
	caBefore, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	tr.Close()
	// Simulate a complete old installation with public permissions, including
	// its real SQLite index, generated CA/key and captured body hierarchy.
	if err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		makePublicTrafficTestPath(t, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertTrafficUnrelatedRead(t, legacy, true)
	tr, err = Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	checkTree(dir)
	for _, path := range []string{blob, legacy, caPath, filepath.Join(dir, "_index", "index.sqlite")} {
		assertTrafficUnrelatedRead(t, path, false)
	}
	if caAfter, err := os.ReadFile(caPath); err != nil || !bytes.Equal(caBefore, caAfter) {
		t.Fatal("legacy repair rotated the CA/key")
	}
	if count, err := tr.Count(); err != nil || count != 1 {
		t.Fatalf("legacy repair lost capture rows: %d %v", count, err)
	}
	for _, path := range []string{blob, restoredBlob} {
		if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, body) {
			t.Fatalf("owner evidence changed: %s: %v", path, err)
		}
	}
	if data, err := os.ReadFile(legacy); err != nil || !bytes.Equal(data, legacyBytes) {
		t.Fatal("legacy repair changed evidence")
	}
}
