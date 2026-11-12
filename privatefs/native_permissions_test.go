//go:build windows || linux

package privatefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativePermissionRepairRejectsIndirectAndWrongTypes(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "outside")
	if err := os.WriteFile(path, []byte("preserved"), 0644); err != nil {
		t.Fatal(err)
	}
	before := snapshotPermissions(t, path)
	for _, fixture := range []struct {
		target    string
		directory bool
	}{{path, false}, {parent, true}} {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(fixture.target, link); err != nil {
			t.Fatal(err)
		}
		if err := protect(link, fixture.directory); err == nil {
			t.Fatal("native permission repair followed a link")
		}
	}
	if err := protect(path, true); err == nil {
		t.Fatal("file accepted for directory permission repair")
	}
	if err := protect(parent, false); err == nil {
		t.Fatal("directory accepted for file permission repair")
	}
	if snapshotPermissions(t, path) != before {
		t.Fatal("outside permissions changed")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserved" {
		t.Fatal("outside bytes changed")
	}
}

func TestNativePermissionRepairPinsRenamedFile(t *testing.T) {
	parent := t.TempDir()
	parentBefore := snapshotPermissions(t, parent)
	path := filepath.Join(parent, "legacy")
	if err := os.WriteFile(path, []byte("legacy-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	f, _, err := openPermissionObject(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	retired := filepath.Join(parent, "retired")
	if err := os.Rename(path, retired); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	replacementBefore := snapshotPermissions(t, path)
	if err := protectOpenedFile(f); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, retired)
	if snapshotPermissions(t, path) != replacementBefore || snapshotPermissions(t, parent) != parentBefore {
		t.Fatal("replacement or parent permissions changed")
	}
	for file, want := range map[string]string{path: "replacement-bytes", retired: "legacy-bytes"} {
		data, err := os.ReadFile(file)
		if err != nil || string(data) != want {
			t.Fatal("permission repair changed bytes")
		}
	}
}
