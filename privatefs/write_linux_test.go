package privatefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateTruncateOwnerModes(t *testing.T) {
	for _, mode := range []os.FileMode{0444, 0200, 0000} {
		path := filepath.Join(t.TempDir(), "legacy-journal")
		if err := os.WriteFile(path, []byte("legacy"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		f, err := CreateTruncate(path)
		if err != nil {
			t.Fatalf("owner mode %o no longer repairable: %v", mode, err)
		}
		assertPrivate(t, path)
		if _, err := f.Write([]byte("new")); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "new" {
			t.Fatalf("mode %o write failed: %q %v", mode, data, err)
		}
	}
}
