package privatefs

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxPinnedParentSurvivesDirectoryReplacement(t *testing.T) {
	root := t.TempDir()
	live, retired := filepath.Join(root, "live"), filepath.Join(root, "retired")
	if err := os.Mkdir(live, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(live, "secret")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	parentBefore := snapshotPermissions(t, live)
	parent, name, err := openLinuxParent(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(parent)
	if err := os.Rename(live, retired); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(live, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0644); err != nil {
		t.Fatal(err)
	}
	replacementBefore := snapshotPermissions(t, path)
	flags := unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|flags, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(fd), path)
	data, readErr := io.ReadAll(f)
	f.Close()
	if readErr != nil || string(data) != "original" {
		t.Fatalf("read followed replacement: %q %v", data, readErr)
	}
	fd, err = unix.Openat(parent, name, unix.O_WRONLY|flags, 0)
	if err != nil {
		t.Fatal(err)
	}
	f = os.NewFile(uintptr(fd), path)
	if err := protectAndTruncateOpenedFile(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	_, writeErr := f.Write([]byte("protected-original"))
	f.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	fd, err = unix.Openat(parent, "created", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|flags, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f = os.NewFile(uintptr(fd), filepath.Join(live, "created"))
	assertPrivate(t, filepath.Join(retired, "created"))
	_, writeErr = f.Write([]byte("new-private"))
	f.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if data, err := os.ReadFile(filepath.Join(retired, "secret")); err != nil || string(data) != "protected-original" {
		t.Fatalf("original lost: %q %v", data, err)
	}
	assertPrivate(t, filepath.Join(retired, "secret"))
	if data, err := os.ReadFile(path); err != nil || string(data) != "replacement" || snapshotPermissions(t, path) != replacementBefore {
		t.Fatal("replacement file changed")
	}
	if snapshotPermissions(t, retired) != parentBefore {
		t.Fatal("parent permissions changed")
	}
	if _, err := os.Stat(filepath.Join(live, "created")); !os.IsNotExist(err) {
		t.Fatal("private file appeared in replacement directory")
	}
}

func TestLinuxPrivateLiteralParentComponents(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "secret"), []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"/./secret", "/child/../secret"} {
		if data, err := ReadFile(root + suffix); err != nil || string(data) != "preserved" {
			t.Fatalf("ordinary components lost: %q %v", data, err)
		}
	}
	for _, suffix := range []string{"/link/../secret", "/secret/"} {
		if data, err := ReadFile(root + suffix); err == nil || len(data) != 0 {
			t.Fatal("indirect/wrong-type component accepted")
		}
	}
	if err := ProtectDirectory(root + "/child/"); err != nil {
		t.Fatal("directory trailing slash lost")
	}
}
