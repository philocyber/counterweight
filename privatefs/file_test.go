package privatefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrivateFilePermissionsBeforeWriteAndPublication(t *testing.T) {
	parent := t.TempDir()
	before := snapshotPermissions(t, parent)
	path := filepath.Join(parent, "secret")
	f, err := createPrivate(path)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, path) // The file is already private while empty and open.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := PublishFile(path, []byte("preserved-secret")); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, path)
	if err := PublishFile(path, []byte("replacement")); !os.IsExist(err) {
		t.Fatalf("existing destination must not be replaced: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserved-secret" {
		t.Fatalf("secret changed: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v %v", entries, err)
	}
	if snapshotPermissions(t, parent) != before {
		t.Fatal("parent permissions changed")
	}
}

func TestProtectFilePreservesBytesAndParent(t *testing.T) {
	parent := t.TempDir()
	before := snapshotPermissions(t, parent)
	path := filepath.Join(parent, "legacy-secret")
	if err := os.WriteFile(path, []byte("legacy-secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ProtectFile(path); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, path)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "legacy-secret" {
		t.Fatalf("legacy secret changed: %v", err)
	}
	if snapshotPermissions(t, parent) != before {
		t.Fatal("parent permissions changed")
	}
	if err := ProtectFile(parent); err == nil {
		t.Fatal("directory accepted as secret file")
	}
}

func TestPrivateReadRejectsIndirectFiles(t *testing.T) {
	parent := t.TempDir()
	outside := filepath.Join(parent, "outside")
	if err := os.WriteFile(outside, []byte("outside-bytes-must-not-be-read"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "secret-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadFile(link); err == nil || len(data) != 0 {
		t.Fatalf("indirect private read returned bytes: %q %v", data, err)
	}
	if data, err := ReadFile(parent); err == nil || len(data) != 0 {
		t.Fatalf("directory accepted as private file: %q %v", data, err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "outside-bytes-must-not-be-read" {
		t.Fatal("outside file changed")
	}
	data, err = ReadFile(outside)
	if err != nil || string(data) != "outside-bytes-must-not-be-read" {
		t.Fatalf("ordinary legacy file read changed: %q %v", data, err)
	}
}

func TestPrivateReadChecksOpenedIdentityAndPinsRetiredFile(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "secret")
	if err := os.WriteFile(path, []byte("approved-object"), 0600); err != nil {
		t.Fatal(err)
	}
	expected, identityHandle, err := snapshotPrivateRead(path)
	if err != nil {
		t.Fatal(err)
	}
	if identityHandle != nil {
		defer identityHandle.Close()
	}
	f, err := openPrivateRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Place a different file at the validated path while the original handle
	// remains open. This is the exact replacement window between check/open.
	if err := os.Rename(path, filepath.Join(parent, "retired-secret")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement-object"), 0600); err != nil {
		t.Fatal(err)
	}
	replacement, err := openPrivateRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if data, err := readOpenedPrivateFile(replacement, expected); err == nil || len(data) != 0 {
		t.Fatalf("replaced object accepted: %q %v", data, err)
	}
	data, err := readOpenedPrivateFile(f, expected)
	if err != nil || string(data) != "approved-object" {
		t.Fatalf("retired pinned object lost: %q %v", data, err)
	}
}

func TestPrivateTemporaryAndTruncatedFiles(t *testing.T) {
	parent := t.TempDir()
	before := snapshotPermissions(t, parent)
	f, err := CreateTemp(parent, ".export-*.json")
	if err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, f.Name())
	if filepath.Ext(f.Name()) != ".json" {
		t.Fatal("temporary suffix lost")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "manifest.json")
	if err := os.WriteFile(path, []byte("old-sensitive-manifest"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err = CreateTruncate(path)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, path)
	if _, err := f.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("manifest truncation failed: %v", err)
	}
	if _, err := Create(path); !os.IsExist(err) {
		t.Fatalf("exclusive creation replaced file: %v", err)
	}
	if _, err := CreateTemp(parent, "../escape"); err == nil {
		t.Fatal("temporary pattern escaped directory")
	}
	if snapshotPermissions(t, parent) != before {
		t.Fatal("parent permissions changed")
	}
}

func TestPrivateTruncationKeepsOpenedObjectAndRejectsLinks(t *testing.T) {
	parent := t.TempDir()
	before := snapshotPermissions(t, parent)
	path := filepath.Join(parent, "journal")
	if err := os.WriteFile(path, []byte("old-private-data"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := openExistingPrivateWrite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	retired := filepath.Join(parent, "retired-journal")
	if err := os.Rename(path, retired); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unrelated-replacement"), 0644); err != nil {
		t.Fatal(err)
	}
	replacementPermissions := snapshotPermissions(t, path)
	if err := protectAndTruncateOpenedFile(f); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, retired)
	if _, err := f.Write([]byte("new-private-data")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(retired)
	if err != nil || string(data) != "new-private-data" {
		t.Fatalf("opened object not written: %q %v", data, err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "unrelated-replacement" || snapshotPermissions(t, path) != replacementPermissions {
		t.Fatal("replacement bytes or permissions changed")
	}
	link := filepath.Join(parent, "journal-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if f, err := CreateTruncate(link); err == nil {
		f.Close()
		t.Fatal("indirect journal accepted for truncation")
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "unrelated-replacement" || snapshotPermissions(t, path) != replacementPermissions {
		t.Fatal("linked target changed")
	}
	if snapshotPermissions(t, parent) != before {
		t.Fatal("parent permissions changed")
	}
}

func TestArchiveTreePreservesOwnerModesAndBytes(t *testing.T) {
	parent := t.TempDir()
	before := snapshotPermissions(t, parent)
	root := filepath.Join(parent, "archive")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0755, 0444} {
		name := "executable"
		if mode == 0444 {
			name = "read-only"
		}
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("preserved-archive-payload"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		original, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := ProtectTreePreservingOwner(root); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "windows" {
			assertPrivate(t, path)
			if info.Mode().Perm() != original.Mode().Perm() {
				t.Fatal("read-only attribute changed")
			}
		} else if info.Mode().Perm() != original.Mode().Perm()&0700 {
			t.Fatalf("owner mode changed: %o", info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "preserved-archive-payload" {
			t.Fatalf("payload bytes changed: %v", err)
		}
	}
	if snapshotPermissions(t, parent) != before {
		t.Fatal("shared ancestor permissions changed")
	}
}
