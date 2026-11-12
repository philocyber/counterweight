//go:build !windows

package config

import (
	"fmt"
	"os"
	"testing"
)

func configPermissions(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(info.Mode().Perm())
}

func assertConfigPrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config mode %o", info.Mode().Perm())
	}
}
