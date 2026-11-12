//go:build !windows

package traffic

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func makePublicTrafficTestPath(t *testing.T, path string) {
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0644)
	if info.IsDir() {
		mode = 0755
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
}

func assertTrafficPrivateArtifact(t *testing.T, path string, directory, exactMode bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() != directory || !directory && !info.Mode().IsRegular() {
		t.Fatalf("unexpected artifact type: %s", path)
	}
	if exactMode {
		want := os.FileMode(0600)
		if directory {
			want = 0700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("artifact mode: %s: %o != %o", path, info.Mode().Perm(), want)
		}
	}
}

func assertTrafficUnrelatedRead(t *testing.T, path string, readable bool) {
	t.Helper()
	if os.Getenv("COUNTERWEIGHT_PRIVATE_READER_TEST") != "1" || os.Geteuid() != 0 {
		return // Actual alternate-UID checks require the explicit disposable fixture.
	}
	cmd := exec.Command("su", "-s", "/bin/sh", "nobody", "-c", `exec cat "$COUNTERWEIGHT_TEST_PRIVATE_FILE"`)
	cmd.Env = append(os.Environ(), "COUNTERWEIGHT_TEST_PRIVATE_FILE="+path)
	output, err := cmd.CombinedOutput()
	if readable && (err != nil || string(output) != "synthetic legacy evidence") {
		t.Fatalf("unrelated-reader baseline invalid: %v %s", err, output)
	}
	if !readable && err == nil {
		t.Fatalf("unrelated UID read protected artifact: %s", path)
	}
	t.Logf("alternate-UID read checked: %s: permitted=%t", path, readable)
}
