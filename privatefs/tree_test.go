package privatefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProtectTreePreservesEvidenceAndParent(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "owned")
	child := filepath.Join(root, "legacy")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(child, "capture.bin")
	if err := os.WriteFile(file, []byte("synthetic-secret-evidence"), 0644); err != nil {
		t.Fatal(err)
	}
	before := snapshotPermissions(t, parent)
	if err := ProtectTree(root); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, child, file} {
		assertPrivate(t, path)
	}
	if after := snapshotPermissions(t, parent); after != before {
		t.Fatal("permissions changed outside owned tree")
	}
	if body, err := os.ReadFile(file); err != nil || string(body) != "synthetic-secret-evidence" {
		t.Fatal("permission migration changed evidence")
	}
	if err := ProtectTree(root); err != nil {
		t.Fatal("permission migration is not idempotent", err)
	}
	fresh := filepath.Join(root, "new-secret")
	if err := os.WriteFile(fresh, []byte("new-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, fresh)
}
