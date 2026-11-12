//go:build !windows

package evidence

import (
	"fmt"
	"os"
	"testing"
)

func makeEvidenceParentPublic(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
}
func makeEvidenceFilePublic(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
}
func evidencePermissions(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%o", info.Mode().Perm())
}
func assertEvidencePrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0600)
	if info.IsDir() {
		want = 0700
	}
	if info.Mode().Perm() != want {
		t.Fatalf("evidence mode %o want %o", info.Mode().Perm(), want)
	}
}
