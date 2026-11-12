//go:build !windows

package privatefs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func snapshotPermissions(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(info.Mode().Perm())
}
func assertPrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0600)
	if info.IsDir() {
		want = 0700
	}
	if info.Mode().Perm() != want {
		t.Fatalf("private path %s mode=%o want=%o", path, info.Mode().Perm(), want)
	}
}
func TestProtectTreeRejectsSymlinkWithoutChangingTarget(t *testing.T) {
	outside := t.TempDir()
	file := filepath.Join(outside, "unrelated")
	os.WriteFile(file, []byte("outside"), 0644)
	before := snapshotPermissions(t, file)
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := ProtectTree(root); err == nil {
		t.Fatal("private tree followed symlink")
	}
	if snapshotPermissions(t, file) != before {
		t.Fatal("symlink target permissions changed")
	}
}

func TestPrivateTreeDeniesUnrelatedUnixReader(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_PRIVATE_READER_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires explicit root-owned disposable Unix fixture and existing nobody account")
	}
	parent := t.TempDir()
	// Make only this test's enclosing directories traversable so denial cannot
	// be accidentally supplied by Go's already-private temporary ancestors.
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(parent), 0755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "traffic")
	os.Mkdir(root, 0755)
	file := filepath.Join(root, "capture.bin")
	os.WriteFile(file, []byte("synthetic-capture"), 0644)
	os.Chmod(file, 0644)
	read := func() ([]byte, error) {
		if _, err := exec.LookPath("runuser"); err == nil {
			return exec.Command("runuser", "-u", "nobody", "--", "cat", file).CombinedOutput()
		}
		cmd := exec.Command("su", "-s", "/bin/sh", "nobody", "-c", `exec cat "$COUNTERWEIGHT_TEST_PRIVATE_FILE"`)
		cmd.Env = append(os.Environ(), "COUNTERWEIGHT_TEST_PRIVATE_FILE="+file)
		return cmd.CombinedOutput()
	}
	if body, err := read(); err != nil || string(body) != "synthetic-capture" {
		t.Fatalf("fixture was not readable before protection: %v %s", err, body)
	}
	if err := ProtectTree(root); err != nil {
		t.Fatal(err)
	}
	if body, err := read(); err == nil || string(body) == "synthetic-capture" {
		t.Fatal("unrelated Unix user read protected evidence")
	}
	if body, err := os.ReadFile(file); err != nil || string(body) != "synthetic-capture" {
		t.Fatal("owner access or evidence was lost")
	}
}
