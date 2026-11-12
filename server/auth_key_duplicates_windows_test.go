package server

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJWTKeyCleanupDoesNotIgnorePersistentRemovalFailure(t *testing.T) {
	keyDir, dataDir := t.TempDir(), t.TempDir()
	key := []byte(strings.Repeat("k", 32))
	path, legacy := filepath.Join(keyDir, jwtKeyFilename), filepath.Join(dataDir, jwtKeyFilename)
	for _, file := range []string{path, legacy} {
		if err := os.WriteFile(file, key, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Go's normal Windows reader denies delete sharing until this handle closes.
	// Unlike a read-only attribute (which os.Remove clears), this is an actual
	// persistent removal failure for the entire bounded retry window.
	held, err := os.Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if got, err := loadOrCreateJWTKey(keyDir, dataDir); err == nil || got != nil {
		t.Fatal("persistent legacy removal failure ignored")
	}
	for _, file := range []string{path, legacy} {
		data, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(data, key) {
			t.Fatalf("key discarded after failed cleanup: %v", err)
		}
	}
}
