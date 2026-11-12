package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveStagingPrivatePayloadAndRollback(t *testing.T) {
	parent := t.TempDir()
	makeArchiveParentPublic(t, parent)
	before := archivePermissions(t, parent)
	source := filepath.Join(parent, "tasks", "42", "uploads", "evidence.txt")
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("private-stage-evidence"), 0644); err != nil {
		t.Fatal(err)
	}
	stage, err := stageTaskArchiveFiles(parent, 1, "42", 73)
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(stage.payload, "files", "tasks", "42", "uploads", "evidence.txt")
	assertArchivePrivate(t, staged)
	if err := stage.rollback(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != "private-stage-evidence" {
		t.Fatalf("rollback changed bytes: %v", err)
	}
	assertArchivePrivate(t, source)
	if archivePermissions(t, parent) != before {
		t.Fatal("shared parent permissions changed")
	}
}

func TestManagerRepairsLegacyArchiveAndStagingPermissions(t *testing.T) {
	parent := t.TempDir()
	makeArchiveParentPublic(t, parent)
	before := archivePermissions(t, parent)
	path := filepath.Join(taskArchiveRoot(parent), "legacy.tar.zst")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("legacy-package-placeholder"), 0644); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(taskArchiveRoot(parent), ".staging", "99", "payload", "evidence.txt")
	if err := os.MkdirAll(filepath.Dir(stage), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, []byte("legacy-staging-evidence"), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(parent, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	assertArchivePrivate(t, path)
	assertArchivePrivate(t, stage)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "legacy-package-placeholder" {
		t.Fatalf("legacy archive rewritten: %v", err)
	}
	if archivePermissions(t, parent) != before {
		t.Fatal("shared parent permissions changed")
	}
}
