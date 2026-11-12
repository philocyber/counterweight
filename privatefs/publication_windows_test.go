package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPublicationPinsSourceAndDestination(t *testing.T) {
	root := t.TempDir()
	live, retired := filepath.Join(root, "live"), filepath.Join(root, "retired")
	if err := os.Mkdir(live, 0700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(live, "key")
	p, err := preparePrivatePublication(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer p.cleanup()
	if _, err := p.file.Write([]byte("approved-key")); err != nil {
		t.Fatal(err)
	}
	if err := p.file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(live, retired); err != nil {
		// Windows can prohibit renaming a directory with an open child even
		// when that child shares deletion. This safely prevents replacement;
		// do not mistake that OS boundary for a publication failure.
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			t.Fatal(err)
		}
		if err := p.publish(); err != nil {
			t.Fatal(err)
		}
		p.cleanup()
		if data, err := os.ReadFile(destination); err != nil || string(data) != "approved-key" {
			t.Fatalf("publication failed while parent replacement was blocked: %q %v", data, err)
		}
		entries, err := os.ReadDir(live)
		if err != nil || len(entries) != 1 {
			t.Fatalf("staging entry retained: %v %v", entries, err)
		}
		return
	}
	if err := os.Mkdir(live, 0700); err != nil {
		t.Fatal(err)
	}
	// Seed the stale source pathname too: publishing by pathname must not
	// link this replacement file even though its name matches the temp name.
	if err := os.WriteFile(p.file.Name(), []byte("replacement-source"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := p.publish(); err != nil {
		t.Fatal(err)
	}
	p.cleanup()
	if data, err := os.ReadFile(filepath.Join(retired, "key")); err != nil || string(data) != "approved-key" {
		t.Errorf("original publication lost: %q %v", data, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Error("replacement directory received a key")
	}
	if data, err := os.ReadFile(p.file.Name()); err != nil || string(data) != "replacement-source" {
		t.Errorf("replacement source changed/deleted: %q %v", data, err)
	}
}

func TestWindowsPublicationPinsReplacedSourceEntry(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "key")
	p, err := preparePrivatePublication(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer p.cleanup()
	assertPrivate(t, p.file.Name()) // Private before the first secret byte.
	if _, err := p.file.Write([]byte("approved-key")); err != nil {
		t.Fatal(err)
	}
	if err := p.file.Sync(); err != nil {
		t.Fatal(err)
	}
	retired := filepath.Join(dir, "retired-source")
	if err := os.Rename(p.file.Name(), retired); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.file.Name(), []byte("replacement-source"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := p.publish(); err != nil {
		t.Fatal(err)
	}
	p.cleanup()
	if data, err := os.ReadFile(destination); err != nil || string(data) != "approved-key" {
		t.Errorf("replacement source published: %q %v", data, err)
	}
	assertPrivate(t, destination)
	if data, err := os.ReadFile(p.file.Name()); err != nil || string(data) != "replacement-source" {
		t.Errorf("replacement source changed/deleted: %q %v", data, err)
	}
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Errorf("original staging link retained: %v", err)
	}
}
