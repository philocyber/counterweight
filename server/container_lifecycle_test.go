package server

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/execution"
	"github.com/Philocyber/counterweight/report"
)

// This runner simulates the self-test's filesystem exchange, not containment.
type lifecycleDockerRunner struct {
	mu        sync.Mutex
	workspace string
	removed   int
	remove    func(context.Context) error
}

func (f *lifecycleDockerRunner) Run(ctx context.Context, args []string, _ io.Reader, out io.Writer) error {
	switch args[0] {
	case "version":
		_, _ = io.WriteString(out, "linux")
	case "image":
		value := "sha256:fixture"
		if strings.Contains(strings.Join(args, " "), "Volumes") {
			value = "{}"
		}
		_, _ = io.WriteString(out, value)
	case "create":
		for _, arg := range args {
			if strings.HasPrefix(arg, "type=bind,source=") && strings.Contains(arg, "target=/app/data") {
				f.workspace = strings.Split(strings.TrimPrefix(arg, "type=bind,source="), ",")[0]
			}
		}
	case "exec":
		command := args[len(args)-1]
		if strings.Contains(command, "cat server.txt") {
			b, err := os.ReadFile(filepath.Join(f.workspace, "server.txt"))
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(f.workspace, "container.txt"), b, 0600)
		}
		if strings.Contains(command, "timeout-started") {
			if err := os.WriteFile(filepath.Join(f.workspace, "timeout-started"), nil, 0600); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}
	case "rm":
		f.mu.Lock()
		f.removed++
		fn := f.remove
		f.mu.Unlock()
		if fn != nil {
			return fn(ctx)
		}
	}
	return nil
}

func lifecycleContainerFixture(t *testing.T) (*Engine, *lifecycleDockerRunner, execution.Command) {
	t.Helper()
	// The product refuses container mode when it runs in a container itself,
	// so the fake backend below never becomes ready there.
	if execution.RunningInContainer() {
		t.Skip("container mode is refused when the server itself runs in a container")
	}
	e := NewEngine(nil)
	f := &lifecycleDockerRunner{}
	w := t.TempDir()
	e.execution.ConfigureDocker(execution.DockerOptions{Runner: f, Scope: w, DataDir: w})
	if s := e.execution.Docker().Status(); s.Status != "ready" {
		t.Fatalf("fake readiness: %+v", s)
	}
	in := execution.Command{TaskID: "42", Workspace: w, Dir: w, Roots: execution.Roots{Read: []string{w}, Write: []string{w}}, Shell: "printf success"}
	if _, err := e.execution.Docker().Run(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	f.removed = 0
	t.Cleanup(func() { _ = e.execution.Docker().Close() })
	return e, f, in
}

func TestContainerPauseAfterSuccessfulCommandRemovesIdleContainer(t *testing.T) {
	e, f, in := lifecycleContainerFixture(t)
	f.remove = func(context.Context) error {
		if !e.IsPaused(in.TaskID) {
			t.Error("cleanup preceded pause barrier")
		}
		return nil
	}
	e.Pause(in.TaskID, agent.AbortPausedByUser)
	if f.removed != 1 {
		t.Fatalf("idle removals: %d", f.removed)
	}
}

func TestContainerDeleteAndArchiveTeardown(t *testing.T) {
	for _, path := range []string{"delete", "archive"} {
		t.Run(path, func(t *testing.T) {
			m, engine := executionFixture(t)
			task := executionTask(t, m, db.AssessmentGeneral)
			e, f, in := lifecycleContainerFixture(t)
			engine.execution = e.execution
			m.registerContainerExecution(e.execution)
			in.TaskID = task.ID
			if _, err := e.execution.Docker().Run(t.Context(), in); err != nil {
				t.Fatal(err)
			}
			f.removed = 0
			s := &Server{m: m, engine: engine, ctx: context.Background()}
			f.remove = func(context.Context) error {
				if !engine.IsDeleting(in.TaskID) {
					t.Error("cleanup preceded delete barrier")
				}
				return nil
			}
			if path == "delete" {
				r := httptest.NewRequest("DELETE", "/api/tasks/"+task.ID, strings.NewReader(`{}`))
				r.SetPathValue("id", task.ID)
				w := httptest.NewRecorder()
				s.pgDeleteTask(w, r)
				if w.Code != 200 {
					t.Fatalf("delete: %d %s", w.Code, w.Body.String())
				}
			} else {
				if err := m.SetTaskPaused(task.ID, true); err != nil {
					t.Fatal(err)
				}
				id, _ := strconv.ParseInt(task.ID, 10, 64)
				_, err := m.pg.QueueTaskArchive(id)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.runOneTaskArchiveJob(); err != nil {
					t.Fatal(err)
				}
			}
			if f.removed != 1 {
				t.Fatalf("task removals: %d", f.removed)
			}
			f.remove = nil
		})
	}
}

func TestContainerDeleteBarrierAndArchiveStopRemoveIdleContainer(t *testing.T) {
	for _, beginDelete := range []bool{true, false} {
		t.Run(strconv.FormatBool(beginDelete), func(t *testing.T) {
			e, f, in := lifecycleContainerFixture(t)
			f.remove = func(context.Context) error {
				if !e.IsDeleting(in.TaskID) {
					t.Error("cleanup preceded teardown barrier")
				}
				return nil
			}
			if beginDelete {
				if !e.BeginDelete(in.TaskID) {
					t.Fatal("barrier refused")
				}
			} else {
				e.StopTask(in.TaskID)
			}
			if f.removed != 1 {
				t.Fatalf("idle removals: %d", f.removed)
			}
		})
	}
}

func TestContainerDeleteRemovalFailureDoesNotHoldBarrierForever(t *testing.T) {
	e, f, in := lifecycleContainerFixture(t)
	f.remove = func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("removal has no bounded deadline")
		}
		return errors.New("Docker removal denied")
	}
	if !e.BeginDelete(in.TaskID) {
		t.Fatal("removal failure prevented deletion")
	}
	if e.execution.Docker().Status().Status != "failed" {
		t.Fatal("backend remained ready")
	}
}

func TestContainerManagerShutdownWaitsForRemoval(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	e, f, _ := lifecycleContainerFixture(t)
	m.registerContainerExecution(e.execution)
	entered, release := make(chan struct{}), make(chan struct{})
	f.remove = func(context.Context) error { close(entered); <-release; return nil }
	done := make(chan error, 1)
	go func() { done <- m.Close() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not remove container")
	}
	select {
	case <-done:
		t.Fatal("manager closed before removal")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestContainerRegisteredShutdownWaitsForRemoval(t *testing.T) {
	m := &Manager{}
	e, f, _ := lifecycleContainerFixture(t)
	m.registerContainerExecution(e.execution)
	entered, release := make(chan struct{}), make(chan struct{})
	f.remove = func(context.Context) error { close(entered); <-release; return nil }
	done := make(chan error, 1)
	go func() { done <- m.closeContainerExecution() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not remove container")
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
}

func TestContainerProductionWiringAllowsReadyNativeBackend(t *testing.T) {
	e, _, _ := lifecycleContainerFixture(t)
	prior := report.ExecutionLookup
	t.Cleanup(func() { report.ExecutionLookup = prior })
	// wireFileAccess only needs a nonnil database handle to install its hooks.
	m := &Manager{pg: &db.DB{}, dir: t.TempDir()}
	e.m = m
	e.wireFileAccess()
	t.Cleanup(func() { containerLifecycles.Delete(m) })
	if s := e.execution.ModeStatus(execution.Container); s.Status != "ready" {
		t.Fatalf("production wiring blocked ready native backend: %+v", s)
	}
}
