package selfupdate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTrashHelperSleeps(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_TRASH_HELPER") == "" {
		t.Skip("helper process only")
	}
	time.Sleep(30 * time.Second)
}

// A running executable cannot be deleted on Windows; removeOrSetAside must still
// clear the path so the next swap can rename into it, and cleanTrash must leave
// the set-aside file alone until nothing runs it.
func TestRemoveOrSetAsideRunningExecutable(t *testing.T) {
	dir := t.TempDir()
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "counterweight.old.exe")
	if err := os.WriteFile(old, self, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(old, "-test.run=^TestTrashHelperSleeps$")
	cmd.Env = append(os.Environ(), "COUNTERWEIGHT_TRASH_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	time.Sleep(300 * time.Millisecond)

	if err := removeOrSetAside(old); err != nil {
		t.Fatalf("removeOrSetAside: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("%s still exists: %v", old, err)
	}

	p := Paths{Dir: dir, Current: filepath.Join(dir, "counterweight.exe")}
	cleanTrash(p)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	cleanTrash(p)
	if left, _ := filepath.Glob(filepath.Join(dir, "*.trash")); len(left) != 0 {
		t.Fatalf("trash not cleaned after the process exited: %v", left)
	}
}
