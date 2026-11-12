package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSetupTokenRequiresExistingLocalKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadSetupToken(dir); err == nil {
		t.Fatal("setup token was issued without an installation key")
	}
	if _, err := os.Stat(filepath.Join(dir, jwtKeyFilename)); !os.IsNotExist(err) {
		t.Fatal("read-only token command created a signing key")
	}
	key := strings.Repeat("k", 32)
	path := filepath.Join(dir, jwtKeyFilename)
	if err := os.WriteFile(path, []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := ReadSetupToken(dir)
	if err != nil || token != bootstrapToken([]byte(key)) || token == key {
		t.Fatalf("local token is not compatible with enrollment: %v", err)
	}
	if token == bootstrapToken([]byte(strings.Repeat("z", 32))) {
		t.Fatal("different installations share setup credentials")
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != key {
		t.Fatal("token command modified the installation key")
	}
}
