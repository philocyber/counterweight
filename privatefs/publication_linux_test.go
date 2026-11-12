package privatefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxPublicationPinsSourceAndDestination(t *testing.T) {
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
		t.Fatal(err)
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
