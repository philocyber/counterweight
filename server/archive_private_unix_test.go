//go:build !windows

package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	pgdb "github.com/Philocyber/counterweight/db"
)

func makeArchiveParentPublic(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
}
func archivePermissions(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%o", info.Mode().Perm())
}
func assertArchivePrivate(t *testing.T, path string) {
	t.Helper()
	if archivePermissions(t, path) != "600" {
		t.Fatalf("archive file is not 0600: %s", path)
	}
}

func TestArchiveRestoreRetainsOwnerExecutionWithoutPublicAccess(t *testing.T) {
	parent := t.TempDir()
	payload := filepath.Join(parent, "payload")
	if err := os.Mkdir(payload, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "fixture.sh"), []byte("#!/bin/sh\nprintf 'restored-fixture'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "task.tar.zst")
	_, _, checksum, err := writeTaskArchivePackage(path, payload, &pgdb.TaskArchiveSnapshot{FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: 42})
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(parent, "restored")
	if err := extractTaskArchivePackage(path, checksum, destination); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(destination, "fixture.sh")
	if archivePermissions(t, script) != "700" {
		t.Fatal("owner execute or private mode lost")
	}
	output, err := exec.Command(script).Output()
	if err != nil || string(output) != "restored-fixture" {
		t.Fatalf("restored executable failed: %v", err)
	}
}

func TestArchiveStagingPreservesExecutionAndSkipsSymlinkTargets(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "tasks", "42", "uploads", "fixture.sh")
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf 'staged-fixture'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside-target"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(source), "outside-link")); err != nil {
		t.Fatal(err)
	}
	stage, err := stageTaskArchiveFiles(parent, 1, "42", 73)
	if err != nil {
		t.Fatal(err)
	}
	if archivePermissions(t, outside) != "644" {
		t.Fatal("staging changed symlink target permissions")
	}
	if err := stage.rollback(); err != nil {
		t.Fatal(err)
	}
	if archivePermissions(t, source) != "700" {
		t.Fatal("owner execution/private mode lost")
	}
	output, err := exec.Command(source).Output()
	if err != nil || string(output) != "staged-fixture" {
		t.Fatalf("rollback broke executable: %v", err)
	}
}

func TestArchiveStagingRejectsLinkedJournal(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(taskArchiveRoot(parent), ".staging", "1")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"archive_id":1,"task_id":"42","moves":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "journal.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := stageTaskArchiveFiles(parent, 1, "42", 73); err == nil {
		t.Fatal("linked journal was read")
	}
	if archivePermissions(t, outside) != "644" {
		t.Fatal("linked journal target permissions changed")
	}
}
