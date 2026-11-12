package execution

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type dockerFake struct {
	mu    sync.Mutex
	calls [][]string
	run   func(context.Context, []string, io.Writer) error
}

func (f *dockerFake) Run(ctx context.Context, a []string, _ io.Reader, w io.Writer) error {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{}, a...))
	f.mu.Unlock()
	if f.run != nil {
		return f.run(ctx, a, w)
	}
	return nil
}
func (f *dockerFake) has(fragment string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.calls {
		if strings.Contains(strings.Join(a, "|"), fragment) {
			return true
		}
	}
	return false
}
func readyDocker(t *testing.T, f *dockerFake) (*DockerBackend, Command) {
	t.Helper()
	w := t.TempDir()
	d := NewDockerBackend(DockerOptions{Runner: f, Scope: w, Prefix: "cw-sbx-test-", Image: "local-fixture"})
	d.prepareRoots = func(Roots) error { return nil }
	d.setStatus(BackendStatus{Backend: Container, Status: "ready", Image: "local-fixture", ImageDigest: "sha256:fixture"})
	in := Command{TaskID: "123", Workspace: w, Dir: w, Roots: Roots{Read: []string{w}, Write: []string{w}}, Shell: "printf hello", Timeout: time.Second}
	return d, in
}

func TestContainerHardeningAndMountMapping(t *testing.T) {
	f := &dockerFake{}
	d, in := readyDocker(t, f)
	ro := t.TempDir()
	rw := t.TempDir()
	in.Roots.Read = append(in.Roots.Read, ro, rw)
	in.Roots.Write = append(in.Roots.Write, rw)
	binds, err := taskBinds(in)
	if err != nil {
		t.Fatal(err)
	}
	args := d.createArgs("cw-sbx-test-fixture", in.TaskID, "sha256:fixture", binds)
	joined := strings.Join(args, "|")
	for _, s := range []string{"--user|65532:65532", "--cap-drop|ALL", "--security-opt|no-new-privileges", "--pids-limit|256", "--memory|1g", "--cpus|2", "--read-only", "--network|bridge", "--ipc|private", "--tmpfs|/tmp:rw,noexec,nosuid,nodev,size=128m,mode=1777", "--entrypoint|/usr/bin/env", "io.counterweight.sandbox.owner=", "io.counterweight.sandbox.task=123", "sha256:fixture|-i|PATH="} {
		if !strings.Contains(joined, s) {
			t.Errorf("missing %s: %s", s, joined)
		}
	}
	for _, s := range []string{"--privileged", "docker.sock", "--network|host", "--env-file", "--publish", "--cap-add"} {
		if strings.Contains(joined, s) {
			t.Errorf("unsafe argument %s", s)
		}
	}
	for _, b := range binds {
		if b.Source == ro && !b.ReadOnly {
			t.Fatal("read-only clone became writable")
		}
		if b.Source == rw && b.ReadOnly {
			t.Fatal("fix worktree lost write authority")
		}
	}
	got, err := mapDirectory(binds, filepath.Join(in.Workspace, "i9"))
	if err != nil || got != WorkspacePath+"/i9" {
		t.Fatalf("cwd: %s %v", got, err)
	}
	if _, err = mapDirectory(binds, t.TempDir()); err == nil {
		t.Fatal("outside cwd admitted")
	}
	in.Roots.CommandRead = []string{in.Workspace, ro}
	in.Roots.CommandWrite = []string{in.Workspace}
	binds, err = taskBinds(in)
	if err != nil || len(binds) != 2 {
		t.Fatalf("non-command root mounted: %+v %v", binds, err)
	}
}

func TestContainerEnvironmentIsAllowlisted(t *testing.T) {
	t.Setenv("CW_SBX_SERVER_SECRET", "must-not-leak")
	env := ContainerEnvironment([]string{"HTTP_PROXY=http://server", "SSL_CERT_FILE=/server/ca", "OPENAI_API_KEY=secret", "PATH=/evil", "HOME=/server", "LANG=bad", "TOOL_INPUT=safe", "UNKNOWN_SECRET=secret"})
	want := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + WorkspacePath + "/.home", "LANG=C.UTF-8", "TERM=dumb", "TOOL_INPUT=safe"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("environment: %v", env)
	}
}

func TestContainerIncompleteIntegrationCannotBeSelected(t *testing.T) {
	f := &dockerFake{}
	d, _ := readyDocker(t, f)
	c := NewController()
	c.docker = d
	c.BlockContainer("integration incomplete")
	s := c.ModeStatus(Container)
	if s.Status != "not-available" || s.Reason != "integration incomplete" {
		t.Fatalf("incomplete integration exposed: %+v", s)
	}
	if c.ModeStatus(ConfinedFiles).Status != "ready" {
		t.Fatal("existing file mode changed")
	}
}

func TestContainerExecCancellationRemovesWholeContainer(t *testing.T) {
	f := &dockerFake{run: func(ctx context.Context, a []string, w io.Writer) error {
		if a[0] == "exec" {
			_, _ = io.WriteString(w, "started")
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	d, in := readyDocker(t, f)
	in.Timeout = 10 * time.Millisecond
	res, err := d.Run(context.Background(), in)
	if !errors.Is(err, context.DeadlineExceeded) || !res.TimedOut || res.Output != "started" {
		t.Fatalf("%+v %v", res, err)
	}
	if !f.has("rm|-f|-v|cw-sbx-test-") {
		t.Fatal("only the client was killed")
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestContainerFailedKillFailsClosed(t *testing.T) {
	f := &dockerFake{run: func(ctx context.Context, a []string, w io.Writer) error {
		switch a[0] {
		case "exec":
			<-ctx.Done()
			return ctx.Err()
		case "rm":
			return errors.New("engine unavailable")
		}
		return nil
	}}
	d, in := readyDocker(t, f)
	in.Timeout = time.Millisecond
	if _, err := d.Run(context.Background(), in); err == nil {
		t.Fatal("kill failure hidden")
	}
	if d.Status().Status != "failed" {
		t.Fatal("backend remained ready")
	}
	if _, err := d.Run(context.Background(), in); err == nil {
		t.Fatal("failed backend executed")
	}
}

func TestContainerOrphansAreScoped(t *testing.T) {
	f := &dockerFake{run: func(_ context.Context, a []string, w io.Writer) error {
		if a[0] == "ps" {
			_, _ = io.WriteString(w, "owned1\nowned2\n")
		}
		return nil
	}}
	d, _ := readyDocker(t, f)
	if err := d.CleanupOrphans(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.has("ps|-aq|--filter|label="+ownerLabel+"="+scopeID(d.opts.Scope)) || !f.has("rm|-f|-v|owned1") || !f.has("rm|-f|-v|owned2") {
		t.Fatalf("cleanup: %v", f.calls)
	}
}

func TestContainerUnavailableNeverPulls(t *testing.T) {
	f := &dockerFake{run: func(_ context.Context, a []string, w io.Writer) error {
		if a[0] == "version" {
			_, _ = io.WriteString(w, "linux")
			return nil
		}
		return errors.New("absent")
	}}
	d, _ := readyDocker(t, f)
	s := d.Recheck(context.Background())
	if s.Status != "not-available" || f.has("pull") {
		t.Fatalf("%+v", s)
	}
	d.opts.InContainer = true
	s = d.Recheck(context.Background())
	if !strings.Contains(s.Reason, "broker") || s.Status != "not-available" {
		t.Fatalf("%+v", s)
	}
}

func TestContainerOutputIsBounded(t *testing.T) {
	b := newLimitedBuffer(4)
	n, err := b.Write([]byte("12345678"))
	if n != 8 || err != nil || b.result().Output != "1234" || !b.result().Truncated {
		t.Fatal(b.result())
	}
}

func TestContainerCloseCancelsActiveExec(t *testing.T) {
	started := make(chan struct{})
	f := &dockerFake{run: func(ctx context.Context, a []string, w io.Writer) error {
		if a[0] == "exec" {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	d, in := readyDocker(t, f)
	in.Timeout = time.Minute
	done := make(chan error, 1)
	go func() { _, err := d.Run(context.Background(), in); done <- err }()
	<-started
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("active run survived close: %v", err)
	}
	if !f.has("rm|-f|-v|cw-sbx-test-") {
		t.Fatal("close did not remove container")
	}
	if _, err := d.Run(context.Background(), in); err == nil {
		t.Fatal("closed backend admitted command")
	}
}

// TestContainerIntegration is deliberately real and never pulls or builds. Set
// CW_SANDBOX_TEST_IMAGE to a locally present image containing bash/coreutils.
func TestContainerIntegration(t *testing.T) {
	if os.Getenv("CW_SANDBOX_SKIP_INTEGRATION") == "1" {
		t.Skip("Real Docker integration explicitly disabled for this test run")
	}
	p, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker CLI unavailable: " + err.Error())
	}
	r := CLIRunner{Path: p}
	b := newLimitedBuffer(8192)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err = r.Run(ctx, []string{"version", "--format", "{{.Server.Version}}"}, nil, b); err != nil {
		t.Skipf("Docker engine unavailable: %s (%v)", b.result().Output, err)
	}
	images := []string{os.Getenv("CW_SANDBOX_TEST_IMAGE"), "counterweight:local", "python:3.12-slim-bookworm", "debian:bookworm-slim"}
	image := ""
	for _, candidate := range images {
		if candidate == "" {
			continue
		}
		b = newLimitedBuffer(8192)
		if r.Run(ctx, []string{"image", "inspect", "--format", "{{.Id}}", candidate}, nil, b) == nil {
			image = candidate
			break
		}
	}
	if image == "" {
		t.Skip("No local test image with bash/coreutils; no image was pulled. Build counterweight:local or set CW_SANDBOX_TEST_IMAGE")
	}
	d := NewDockerBackend(DockerOptions{Runner: r, Image: image, Scope: t.TempDir(), Prefix: "cw-sbx-test-", DataDir: t.TempDir()})
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("container cleanup: %v", err)
		}
	})
	s := d.Recheck(ctx)
	if s.Status != "ready" {
		t.Fatalf("real container self-test: %+v", s)
	}
	t.Logf("image=%s digest=%s; PASS (a) host marker inaccessible (b) parent environment absent (c) runtime read-only (d) UID:GID 65532:65532, zero effective capabilities, mount denied (e) timeout removed container and descendants (f) shared files visible both ways", s.Image, s.ImageDigest)
}
