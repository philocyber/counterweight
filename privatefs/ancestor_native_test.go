//go:build windows || linux

package privatefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateOperationsRejectLinkedAncestors(t *testing.T) {
	for _, operation := range []string{"read", "create", "truncate", "protect-file", "protect-directory"} {
		t.Run(operation, func(t *testing.T) {
			outside := t.TempDir()
			path := filepath.Join(outside, "secret")
			child := filepath.Join(outside, "child")
			if err := os.Mkdir(child, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("outside-preserved"), 0644); err != nil {
				t.Fatal(err)
			}
			fileBefore, dirBefore := snapshotPermissions(t, path), snapshotPermissions(t, outside)
			childBefore := snapshotPermissions(t, child)
			link := filepath.Join(t.TempDir(), "linked-parent")
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(link, "secret")
			var err error
			switch operation {
			case "read":
				var data []byte
				data, err = ReadFile(target)
				if len(data) != 0 {
					t.Error("read returned bytes through a linked ancestor")
				}
			case "create", "truncate":
				var f *os.File
				if operation == "create" {
					f, err = Create(filepath.Join(link, "new-secret"))
				} else {
					f, err = CreateTruncate(target)
				}
				if f != nil {
					f.Close()
				}
			case "protect-file":
				err = ProtectFile(target)
			case "protect-directory":
				err = ProtectDirectory(filepath.Join(link, "child"))
			}
			if err == nil {
				t.Error("private operation followed a linked ancestor")
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "outside-preserved" {
				t.Error("outside bytes changed")
			}
			if snapshotPermissions(t, path) != fileBefore || snapshotPermissions(t, outside) != dirBefore || snapshotPermissions(t, child) != childBefore {
				t.Error("outside permissions changed")
			}
			if _, err := os.Stat(filepath.Join(outside, "new-secret")); !os.IsNotExist(err) {
				t.Error("outside file created")
			}
		})
	}
}

func TestPrivateDirectoryCreationRejectsLinkedParentsBeforeMutation(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		protect func(string) error
	}{{"directory", ProtectDirectory}, {"tree", ProtectTree}, {"owner-tree", ProtectTreePreservingOwner}} {
		t.Run(fixture.name, func(t *testing.T) {
			outside := t.TempDir()
			before := snapshotPermissions(t, outside)
			link := filepath.Join(t.TempDir(), "link")
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			if err := fixture.protect(filepath.Join(link, "missing", "deep")); err == nil {
				t.Error("linked directory creation accepted")
			}
			if _, err := os.Stat(filepath.Join(outside, "missing")); !os.IsNotExist(err) {
				t.Error("rejected operation created outside directories")
			}
			if snapshotPermissions(t, outside) != before {
				t.Error("outside parent permissions changed")
			}
		})
	}
}

func TestPrivateNestedDirectoryCreationPreservesExistingAncestors(t *testing.T) {
	parent := t.TempDir()
	before := snapshotPermissions(t, parent)
	first := filepath.Join(parent, "new")
	second := filepath.Join(first, "nested")
	target := filepath.Join(second, "store")
	for attempt := 0; attempt < 2; attempt++ {
		if err := ProtectDirectory(target); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{first, second, target} {
			assertPrivate(t, dir)
		}
		if snapshotPermissions(t, parent) != before {
			t.Fatal("existing ancestor permissions changed")
		}
	}
}
