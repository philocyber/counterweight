package privatefs

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateReadLinuxRejectsSwappedFIFOAndLink(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "original")
	if err := os.WriteFile(path, []byte("expected"), 0600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(parent, "replacement-fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openPrivateRead(fifo)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if data, err := readOpenedPrivateFile(f, expected); err == nil || len(data) != 0 {
		t.Fatalf("nonregular replacement accepted: %q %v", data, err)
	}
	link := filepath.Join(parent, "replacement-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if f, err := openPrivateRead(link); err == nil {
		f.Close()
		t.Fatal("native Linux open followed replacement link")
	}
}
