package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigPrivateReadPreservesCredentialsAndParent(t *testing.T) {
	dir := t.TempDir()
	parentBefore := configPermissions(t, dir)
	path := filepath.Join(dir, "config.json")
	raw := []byte(`{"database":{"dsn":"postgres://user:synthetic-password@localhost/db"},"skill_dir":"custom-skills"}`)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_CONFIG", path)
	t.Setenv("COUNTERWEIGHT_PG_DSN", "")
	for i := 0; i < 2; i++ {
		c := Load()
		if c.SkillDir != "custom-skills" {
			t.Fatal("skill configuration lost")
		}
		dsn, _, err := PostgresDSN()
		if err != nil || dsn != c.Database.DSN {
			t.Fatalf("database configuration changed: %v", err)
		}
		assertConfigPrivate(t, path)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, raw) {
		t.Fatalf("configuration bytes changed: %v", err)
	}
	if configPermissions(t, dir) != parentBefore {
		t.Fatal("parent permissions changed")
	}
}

func TestConfigPrivateReadRejectsInvalidWithoutPartialCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := []byte(`{"database":{"dsn":"SECRET_DSN"},"skill_dir":42}`)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_CONFIG", path)
	t.Setenv("COUNTERWEIGHT_PG_DSN", "")
	if c := Load(); c != (Config{}) {
		t.Fatal("partially decoded configuration accepted")
	}
	if dsn, _, err := PostgresDSN(); err == nil || dsn != "" || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("invalid configuration did not fail safely: %v", err)
	}
	t.Setenv("COUNTERWEIGHT_CONFIG", dir)
	if _, _, err := PostgresDSN(); err == nil {
		t.Fatal("directory accepted as configuration file")
	}
	// An explicit environment DSN continues to bypass optional file loading.
	t.Setenv("COUNTERWEIGHT_PG_DSN", "postgres://environment/db")
	if dsn, _, err := PostgresDSN(); err != nil || dsn != "postgres://environment/db" {
		t.Fatalf("environment precedence changed: %v", err)
	}
}
