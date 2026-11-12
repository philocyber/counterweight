//go:build unix

package server

import (
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	pgdb "github.com/Philocyber/counterweight/db"
)

func TestWorkspaceRefusesFifoAndDeviceLinksWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	s := &Server{m: &Manager{dir: dir}}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(dir, "zero")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, name := range []string{"pipe", "zero"} {
		if code, _ := workspaceRequest(t, s.wsRead, http.MethodGet, "/api/workspace/read?path="+name, nil); code != 400 {
			t.Fatalf("read %s = %d, want 400", name, code)
		}
		if code, _ := workspaceRequest(t, s.wsDownload, http.MethodGet, "/api/workspace/download?path="+name, nil); code != 400 {
			t.Fatalf("download %s = %d, want 400", name, code)
		}
		if code, _ := workspaceRequest(t, s.wsWrite, http.MethodPost, "/api/workspace/write", []byte(`{"path":"`+name+`","content":"x"}`)); code != 400 {
			t.Fatalf("write %s = %d, want 400", name, code)
		}
	}
}

func TestTaskArchivePackageSkipsFifo(t *testing.T) {
	dataDir := t.TempDir()
	payload := filepath.Join(dataDir, "payload")
	if err := os.MkdirAll(payload, archiveDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "keep.txt"), []byte("keep me"), archiveFileMode); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(payload, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	type result struct {
		checksum string
		err      error
	}
	done := make(chan result, 1)
	archivePath := filepath.Join(dataDir, "archive.tar.zst")
	go func() {
		_, _, sum, err := writeTaskArchivePackage(archivePath, payload, &pgdb.TaskArchiveSnapshot{FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: 42})
		done <- result{sum, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("archive writer blocked on a named pipe")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	extracted := filepath.Join(dataDir, "restore")
	if err := extractTaskArchivePackage(archivePath, r.checksum, extracted); err != nil {
		t.Fatalf("package with a skipped pipe must restore: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(extracted, "pipe")); !os.IsNotExist(err) {
		t.Fatalf("pipe was archived: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(extracted, "keep.txt")); err != nil || string(got) != "keep me" {
		t.Fatalf("regular file lost: %q %v", got, err)
	}
}
