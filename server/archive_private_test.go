package server

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"

	pgdb "github.com/Philocyber/counterweight/db"
)

func TestArchiveWritersPrivateUnderPermissiveParent(t *testing.T) {
	parent := t.TempDir()
	makeArchiveParentPublic(t, parent)
	before := archivePermissions(t, parent)
	journal := filepath.Join(parent, "journal.json")
	for _, value := range []string{"old-sensitive-journal", "new-sensitive-journal"} {
		if err := writeArchiveJournal(journal, map[string]string{"value": value}); err != nil {
			t.Fatal(err)
		}
		assertArchivePrivate(t, journal)
	}
	zipPath := filepath.Join(parent, "findings.zip")
	if err := buildFindingsEvidenceZip(zipPath, []*pgdb.DBFinding{{ID: 1, Name: "private-finding"}}, parent, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	assertArchivePrivate(t, zipPath)
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != 1 {
		t.Fatal("finding ZIP content lost")
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(parent, "payload")
	if err := os.Mkdir(payload, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "evidence.txt"), []byte("raw-sensitive-evidence"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "task.tar.zst")
	_, _, checksum, err := writeTaskArchivePackage(path, payload, &pgdb.TaskArchiveSnapshot{FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: 42})
	if err != nil {
		t.Fatal(err)
	}
	assertArchivePrivate(t, path)
	assertArchivePrivate(t, filepath.Join(payload, "manifest.json"))
	extracted := filepath.Join(parent, "extracted")
	if err := extractTaskArchivePackage(path, checksum, extracted); err != nil {
		t.Fatal(err)
	}
	assertArchivePrivate(t, filepath.Join(extracted, "evidence.txt"))
	assertArchivePrivate(t, filepath.Join(extracted, "manifest.json"))
	data, err := os.ReadFile(filepath.Join(extracted, "evidence.txt"))
	if err != nil || string(data) != "raw-sensitive-evidence" {
		t.Fatalf("archive evidence changed: %v", err)
	}
	if archivePermissions(t, parent) != before {
		t.Fatal("archive writer hardened an unrelated parent")
	}
}
