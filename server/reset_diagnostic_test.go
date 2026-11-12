package server

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResetHelperDoesNotEchoDSNOrDriverErrors(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if runtime.GOOS == "windows" {
		bash = `C:\Program Files\Git\bin\bash.exe`
		_, err = os.Stat(bash)
	}
	if err != nil {
		t.Skip("requires Bash")
	}
	source, err := os.ReadFile(filepath.Join("..", "reset-password.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, exit := range []string{"0", "1"} {
		t.Run("backend-exit-"+exit, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "reset-password.sh")
			if err := os.WriteFile(script, bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n")), 0600); err != nil {
				t.Fatal(err)
			}
			// Only the backend process is synthetic. Execute the real reset helper and
			// inject a driver diagnostic containing credentials before success/failure.
			stub := "#!/usr/bin/env bash\n[[ $# == 1 && $1 == -reset-password ]] || exit 90\n[[ $COUNTERWEIGHT_RESET_DSN == 'postgres://SECRET_USER:SECRET_PASSWORD@fixture.invalid/db' ]] || exit 91\nprintf '%s\\n' 'SECRET_DRIVER_DIAGNOSTIC' >&2\nexit " + exit + "\n"
			if err := os.WriteFile(filepath.Join(dir, "backend"), []byte(stub), 0755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bash, "--noprofile", "--norc", filepath.ToSlash(script), "--backend", filepath.ToSlash(filepath.Join(dir, "backend")), "-m", "local", "--dsn", "postgres://SECRET_USER:SECRET_PASSWORD@fixture.invalid/db", "-p", "synthetic password", "-y")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if (err == nil) != (exit == "0") {
				t.Fatalf("reset outcome changed: %v %s", err, out)
			}
			if strings.Contains(string(out), "SECRET") {
				t.Fatalf("helper printed credentials: %s", out)
			}
			if !strings.Contains(string(out), "credentials hidden") {
				t.Fatalf("safe connection label missing: %s", out)
			}
		})
	}
}
