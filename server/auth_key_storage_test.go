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

func TestJWTKeyPrivateStoragePreservesSessions(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "restart"
		if legacy {
			name = "migration"
		}
		t.Run(name, func(t *testing.T) {
			keyDir, dataDir := t.TempDir(), t.TempDir()
			key := []byte(strings.Repeat("k", 32))
			path := filepath.Join(keyDir, jwtKeyFilename)
			original := path
			if legacy {
				original = filepath.Join(dataDir, jwtKeyFilename)
			}
			if err := os.WriteFile(original, key, 0644); err != nil {
				t.Fatal(err)
			}
			token, err := signJWT(key)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				loaded, err := loadOrCreateJWTKey(keyDir, dataDir)
				if err != nil || !bytes.Equal(loaded, key) {
					t.Fatalf("key changed: %v", err)
				}
				parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return loaded, nil }, jwt.WithValidMethods([]string{"HS256"}))
				if err != nil || !parsed.Valid {
					t.Fatalf("existing session invalidated: %v", err)
				}
			}
			if legacy {
				if _, err := os.Lstat(original); !os.IsNotExist(err) {
					t.Fatalf("legacy key retained: %v", err)
				}
			}
		})
	}
}

func TestJWTKeyCreationAndInvalidKeyFailClosed(t *testing.T) {
	dir := t.TempDir()
	key, err := loadOrCreateJWTKey(dir, dir)
	if err != nil || len(key) != 32 {
		t.Fatalf("creation failed: %v", err)
	}
	reloaded, err := loadOrCreateJWTKey(dir, dir)
	if err != nil || !bytes.Equal(key, reloaded) {
		t.Fatalf("restart changed key: %v", err)
	}
	for _, legacy := range []bool{false, true} {
		keyDir, dataDir := t.TempDir(), t.TempDir()
		path := filepath.Join(keyDir, jwtKeyFilename)
		if legacy {
			path = filepath.Join(dataDir, jwtKeyFilename)
		}
		if err := os.WriteFile(path, []byte("invalid"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadOrCreateJWTKey(keyDir, dataDir); err == nil {
			t.Fatal("invalid key silently replaced")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "invalid" {
			t.Fatalf("invalid key evidence changed: %v", err)
		}
	}
}

func TestJWTKeyConcurrentStartupUsesOneKey(t *testing.T) {
	dir := t.TempDir()
	type result struct {
		key []byte
		err error
	}
	results := make(chan result, 12)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			key, err := loadOrCreateJWTKey(dir, dir)
			results <- result{key, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	stored, err := os.ReadFile(filepath.Join(dir, jwtKeyFilename))
	if err != nil {
		t.Fatal(err)
	}
	for got := range results {
		if got.err != nil || !bytes.Equal(got.key, stored) {
			t.Fatalf("startup used a different key: %v", got.err)
		}
	}
}
