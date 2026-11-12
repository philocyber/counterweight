package server

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTKeyDuplicateCleanupPreservesSessions(t *testing.T) {
	keyDir, dataDir := t.TempDir(), t.TempDir()
	key := []byte(strings.Repeat("k", 32))
	path, legacy := filepath.Join(keyDir, jwtKeyFilename), filepath.Join(dataDir, jwtKeyFilename)
	if err := os.WriteFile(path, key, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, append(append([]byte("\n"), key...), '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	token, err := signJWT(key)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		loaded, err := loadOrCreateJWTKey(keyDir, dataDir)
		if err != nil || !bytes.Equal(loaded, key) {
			t.Fatalf("active key changed: %v", err)
		}
		parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return loaded, nil }, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !parsed.Valid {
			t.Fatalf("existing session invalidated: %v", err)
		}
	}
	if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
		t.Fatalf("browsable duplicate retained: %v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, key) {
		t.Fatalf("active key bytes changed: %v", err)
	}
}

func TestJWTKeyConflictPreservesBothAndDoesNotDiscloseKeys(t *testing.T) {
	keyDir, dataDir := t.TempDir(), t.TempDir()
	keys := map[string][]byte{filepath.Join(keyDir, jwtKeyFilename): []byte(strings.Repeat("ACTIVE_SECRET_", 4)), filepath.Join(dataDir, jwtKeyFilename): []byte(strings.Repeat("LEGACY_SECRET_", 4))}
	for path, key := range keys {
		if err := os.WriteFile(path, key, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if key, err := loadOrCreateJWTKey(keyDir, dataDir); err == nil || key != nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("conflicting keys did not fail safely: %v", err)
	}
	for path, key := range keys {
		stored, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(stored, key) {
			t.Fatalf("conflicting key discarded: %v", err)
		}
		assertArchivePrivate(t, path)
	}
}

func TestJWTKeyConcurrentDuplicateCleanup(t *testing.T) {
	for _, existing := range []bool{false, true} {
		keyDir, dataDir := t.TempDir(), t.TempDir()
		key := []byte(strings.Repeat("k", 32))
		if err := os.WriteFile(filepath.Join(dataDir, jwtKeyFilename), key, 0644); err != nil {
			t.Fatal(err)
		}
		if existing {
			if err := os.WriteFile(filepath.Join(keyDir, jwtKeyFilename), key, 0644); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				loaded, err := loadOrCreateJWTKey(keyDir, dataDir)
				if err != nil || !bytes.Equal(loaded, key) {
					t.Errorf("concurrent cleanup changed key: %v", err)
				}
			}()
		}
		close(start)
		wg.Wait()
		if _, err := os.Stat(filepath.Join(dataDir, jwtKeyFilename)); !os.IsNotExist(err) {
			t.Fatalf("duplicate retained: %v", err)
		}
	}
}

func TestJWTKeySameDirectoryAliasDoesNotDeleteActiveKey(t *testing.T) {
	parent := t.TempDir()
	keyDir := filepath.Join(parent, "keys")
	if err := os.Mkdir(keyDir, 0700); err != nil {
		t.Fatal(err)
	}
	key, err := loadOrCreateJWTKey(keyDir, keyDir)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(keyDir, alias); err != nil {
		t.Fatal(err)
	}
	got, err := loadOrCreateJWTKey(keyDir, alias)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("directory alias changed active key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(keyDir, jwtKeyFilename)); err != nil {
		t.Fatal("alias cleanup deleted active key")
	}
}

func TestJWTKeyHardLinkedDuplicateRemovesOnlyLegacyName(t *testing.T) {
	keyDir, dataDir := t.TempDir(), t.TempDir()
	key := []byte(strings.Repeat("k", 32))
	path, legacy := filepath.Join(keyDir, jwtKeyFilename), filepath.Join(dataDir, jwtKeyFilename)
	if err := os.WriteFile(path, key, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, legacy); err != nil {
		t.Fatal(err)
	}
	got, err := loadOrCreateJWTKey(keyDir, dataDir)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("hard-link cleanup changed key: %v", err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, key) {
		t.Fatalf("active hard-link name deleted: %v", err)
	}
	if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy hard-link name retained: %v", err)
	}
}
