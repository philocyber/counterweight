package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerRepairsOwnedLegacyEvidencePermissions(t *testing.T) {
	parent := t.TempDir()
	makeArchiveParentPublic(t, parent)
	before := archivePermissions(t, parent)
	root := filepath.Join(parent, "evidence", "blobs", "aa")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "legacy.bin")
	if err := os.WriteFile(path, []byte("legacy-sensitive-evidence"), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(parent, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	assertArchivePrivate(t, path)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "legacy-sensitive-evidence" {
		t.Fatalf("legacy evidence changed: %v", err)
	}
	if archivePermissions(t, parent) != before {
		t.Fatal("startup changed shared data-directory permissions")
	}
}
