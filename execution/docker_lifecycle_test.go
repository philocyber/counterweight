package execution

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// skipInContainer skips a test of the native container backend when the test
// binary itself runs in a container: there the product refuses container mode.
func skipInContainer(t *testing.T) {
	t.Helper()
	if RunningInContainer() {
		t.Skip("container mode is refused when the server itself runs in a container")
	}
}

func TestContainerIdleRemovalFailureMarksBackendFailed(t *testing.T) {
	f := &dockerFake{}
	d, in := readyDocker(t, f)
	if _, err := d.Run(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	f.run = func(_ context.Context, args []string, _ io.Writer) error {
		if args[0] == "rm" {
			return errors.New("removal denied")
		}
		return nil
	}
	if err := d.RemoveTask(in.TaskID); err == nil || d.Status().Status != "failed" {
		t.Fatalf("failed removal hidden: %v %+v", err, d.Status())
	}
}

func TestContainerShutdownWaitsForInstallationRemoval(t *testing.T) {
	skipInContainer(t)
	entered, release := make(chan struct{}), make(chan struct{})
	f := &dockerFake{run: func(_ context.Context, args []string, out io.Writer) error {
		if args[0] == "ps" {
			_, _ = io.WriteString(out, "installation-orphan")
		}
		if args[0] == "rm" {
			close(entered)
			<-release
		}
		return nil
	}}
	d, _ := readyDocker(t, f)
	done := make(chan error, 1)
	go func() { done <- d.Close() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("removal not reached")
	}
	select {
	case <-done:
		t.Fatal("shutdown returned before removal")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !f.has("rm|-f|-v|installation-orphan") {
		t.Fatal("installation orphan retained")
	}
}

func TestContainerRemovalCancelsSetupBeforeExec(t *testing.T) {
	entered := make(chan struct{})
	f := &dockerFake{run: func(ctx context.Context, args []string, _ io.Writer) error {
		if args[0] == "create" {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	d, in := readyDocker(t, f)
	done := make(chan error, 1)
	go func() { _, err := d.Run(context.Background(), in); done <- err }()
	<-entered
	if err := d.RemoveTask(in.TaskID); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("setup survived cancellation")
	}
	if f.has("exec|") {
		t.Fatal("exec started after removal requested")
	}
}

func TestContainerMissingImageBuildStep(t *testing.T) {
	skipInContainer(t)
	f := &dockerFake{run: func(_ context.Context, args []string, out io.Writer) error {
		if args[0] == "version" {
			_, _ = io.WriteString(out, "linux")
		}
		if args[0] == "image" {
			return errors.New("image missing")
		}
		return nil
	}}
	d, _ := readyDocker(t, f)
	s := d.Recheck(t.Context())
	if s.Status != "not-available" || s.Fix != "docker build -t counterweight:local ." {
		t.Fatalf("missing-image diagnostic: %+v", s)
	}
}

func TestContainerShutdownAttemptsEveryOwnedContainerAfterFailure(t *testing.T) {
	skipInContainer(t)
	f := &dockerFake{run: func(_ context.Context, args []string, out io.Writer) error {
		if args[0] == "ps" {
			_, _ = io.WriteString(out, "owned1\nowned2")
		}
		if args[0] == "rm" && args[len(args)-1] == "owned1" {
			return errors.New("removal denied")
		}
		return nil
	}}
	d, _ := readyDocker(t, f)
	if err := d.Close(); err == nil {
		t.Fatal("shutdown hid removal failure")
	}
	if !f.has("rm|-f|-v|owned2") || d.Status().Status != "failed" {
		t.Fatal("remaining installation containers were not attempted or failure was hidden")
	}
}
