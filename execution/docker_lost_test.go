package execution

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

// exitStatusError returns a real *exec.ExitError, as the docker CLI runner does
// when a command inside the container fails.
func exitStatusError(t *testing.T) error {
	t.Helper()
	err := exec.Command(os.Args[0], "-test.no-such-flag").Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("expected an exit error, got %v", err)
	}
	return err
}

func TestContainerLostToOneTaskKeepsBackendReady(t *testing.T) {
	exitErr := exitStatusError(t)
	var lost atomic.Bool
	lost.Store(true)
	f := &dockerFake{run: func(_ context.Context, a []string, w io.Writer) error {
		switch a[0] {
		case "exec":
			if lost.Load() {
				return exitErr
			}
		case "inspect":
			_, _ = io.WriteString(w, "false")
		case "version":
			_, _ = io.WriteString(w, "linux")
		case "image":
			_, _ = io.WriteString(w, "sha256:fixture")
		}
		return nil
	}}
	d, in := readyDocker(t, f)
	if _, err := d.Run(context.Background(), in); !errors.Is(err, ErrTaskContainerLost) {
		t.Fatalf("err=%v", err)
	}
	if d.Status().Status != "ready" {
		t.Fatalf("one lost container failed the backend: %+v", d.Status())
	}
	if !f.has("rm|-f|-v|cw-sbx-test-") {
		t.Fatal("lost container was not removed")
	}
	lost.Store(false)
	if _, err := d.Run(context.Background(), in); err != nil {
		t.Fatalf("next command: %v", err)
	}
	creates := 0
	f.mu.Lock()
	for _, a := range f.calls {
		if a[0] == "create" {
			creates++
		}
	}
	f.mu.Unlock()
	if creates != 2 {
		t.Fatalf("expected a fresh container for the next command, creates=%d", creates)
	}
	other := in
	other.TaskID = "456"
	if _, err := d.Run(context.Background(), other); err != nil {
		t.Fatalf("another task: %v", err)
	}
}

func TestContainerLostWithEngineGoneFailsBackend(t *testing.T) {
	exitErr := exitStatusError(t)
	f := &dockerFake{run: func(_ context.Context, a []string, w io.Writer) error {
		switch a[0] {
		case "exec":
			return exitErr
		case "inspect", "version":
			return errors.New("engine unavailable")
		}
		return nil
	}}
	d, in := readyDocker(t, f)
	_, err := d.Run(context.Background(), in)
	if err == nil || errors.Is(err, ErrTaskContainerLost) || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("err=%v", err)
	}
	if d.Status().Status != "failed" {
		t.Fatal("backend stayed ready with the engine gone")
	}
}
