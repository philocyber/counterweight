package execution

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Philocyber/counterweight/locale"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const ownerLabel = "io.counterweight.sandbox.owner"
const taskLabel = "io.counterweight.sandbox.task"
const WorkspacePath = "/app/data"
const containerUID = "65532:65532"
const ContainerLimitations = "Task commands run in a container; files remain shared with confined server file tools. Network: not restricted. Traffic capture does not record commands run in the container yet."

// ErrTaskContainerLost reports that one task's container stopped while the
// Docker engine and image are still available. The container is forgotten, so
// the task's next command starts a fresh one and no other task is affected.
var ErrTaskContainerLost = errors.New("Task container stopped during command execution; the next command starts a fresh container")

type BackendStatus struct {
	Backend      string    `json:"backend"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason"`
	Fix          string    `json:"fix"`
	Image        string    `json:"image,omitempty"`
	ImageDigest  string    `json:"image_digest,omitempty"`
	LastSelfTest time.Time `json:"last_self_test"`
}

type DockerOptions struct {
	Image  string
	Scope  string
	Prefix string
	// DataDir tests the actual bind-sharing path, not just the system temp path.
	DataDir     string
	Runner      DockerRunner
	InContainer bool
}
type dockerTask struct {
	mu           sync.Mutex
	control      sync.Mutex
	cancel       context.CancelFunc
	removing     bool
	name, mounts string
}
type DockerBackend struct {
	mu           sync.Mutex
	probeMu      sync.Mutex
	opts         DockerOptions
	runner       DockerRunner
	status       BackendStatus
	tasks        map[string]*dockerTask
	closed       bool
	cleaned      bool
	prepareRoots func(Roots) error
}

func NewDockerBackend(o DockerOptions) *DockerBackend {
	if o.Prefix == "" {
		o.Prefix = "cw-sbx-"
	}
	if o.Image == "" {
		o.Image = strings.TrimSpace(os.Getenv("COUNTERWEIGHT_SANDBOX_IMAGE"))
	}
	if o.Image == "" {
		o.Image = "counterweight:local"
	}
	if o.Runner == nil {
		p, _ := exec.LookPath("docker")
		o.Runner = CLIRunner{Path: p}
	}
	return &DockerBackend{opts: o, runner: o.Runner, prepareRoots: prepareContainerRoots, tasks: map[string]*dockerTask{}, status: BackendStatus{Backend: Container, Status: "not-available", Reason: "Container self-test has not passed", Fix: "Run POST /api/execution/recheck", Image: o.Image}}
}

func RunningInContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	b, _ := os.ReadFile("/proc/1/cgroup")
	return strings.Contains(string(b), "docker") || strings.Contains(string(b), "kubepods")
}

func (d *DockerBackend) Status() BackendStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.status
	s.Reason = locale.Text(locale.ServerDefault(), s.Reason)
	s.Fix = locale.Text(locale.ServerDefault(), s.Fix)
	return s
}

// CheckAvailable supplements the cached self-test at each admission. Daemon or
// image loss after a successful probe must not admit a fresh task segment.
func (d *DockerBackend) CheckAvailable(ctx context.Context) error {
	s := d.Status()
	if s.Status != "ready" {
		return errors.New(s.Reason)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := d.cli(ctx, "version", "--format", "{{.Server.Os}}")
	if err == nil && out == "linux" {
		out, err = d.cli(ctx, "image", "inspect", "--format", "{{.Id}}", s.ImageDigest)
		if err == nil && out == s.ImageDigest {
			return nil
		}
	}
	s.Status = "failed"
	s.Reason = "Docker engine or verified image became unavailable"
	s.Fix = "Restore Docker access and the image, then recheck execution isolation"
	d.setStatus(s)
	return errors.New(s.Reason)
}
func (d *DockerBackend) setStatus(s BackendStatus) { d.mu.Lock(); d.status = s; d.mu.Unlock() }
func (d *DockerBackend) cli(ctx context.Context, args ...string) (string, error) {
	b := newLimitedBuffer(64 << 10)
	err := d.runner.Run(ctx, args, nil, b)
	return strings.TrimSpace(b.result().Output), err
}
func randomID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func scopeID(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:12]) }

// CleanupOrphans touches only containers labeled for this installation. It is
// called once before any new run is admitted, never during a recheck.
func (d *DockerBackend) CleanupOrphans(ctx context.Context) error {
	return d.removeInstallationContainers(ctx)
}

// Recheck never pulls an image. The digest is Docker's immutable image config ID.
func (d *DockerBackend) Recheck(ctx context.Context) BackendStatus {
	d.probeMu.Lock()
	defer d.probeMu.Unlock()
	s := BackendStatus{Backend: Container, Status: "not-available", Image: d.opts.Image, Fix: "Start Docker Engine or Docker Desktop, then recheck execution isolation"}
	if d.opts.InContainer || RunningInContainer() {
		s.Reason = "Container mode needs the sandbox broker, which is not part of this release yet"
		s.Fix = "Run the server natively with Docker Engine or Docker Desktop"
		d.setStatus(s)
		return s
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if out, err := d.cli(ctx, "version", "--format", "{{.Server.Os}}"); err != nil || out != "linux" {
		s.Reason = "Docker Linux engine is unavailable: " + out
		if err != nil {
			s.Reason += " (" + err.Error() + ")"
		}
		d.setStatus(s)
		return s
	}
	if !d.cleaned {
		if err := d.CleanupOrphans(ctx); err != nil {
			s.Reason = err.Error()
			s.Fix = "Restore Docker access, then recheck"
			d.setStatus(s)
			return s
		}
		d.cleaned = true
	}
	out, err := d.cli(ctx, "image", "inspect", "--format", "{{.Id}}", d.opts.Image)
	if err != nil || !strings.HasPrefix(out, "sha256:") {
		s.Reason = "Sandbox image is not available locally"
		s.Fix = "docker build -t counterweight:local ."
		d.setStatus(s)
		return s
	}
	s.ImageDigest = out
	s.Status = "ready"
	s.Fix = ""
	volumes, volumeErr := d.cli(ctx, "image", "inspect", "--format", "{{json .Config.Volumes}}", s.ImageDigest)
	var declared map[string]json.RawMessage
	if volumeErr != nil || json.Unmarshal([]byte(volumes), &declared) != nil {
		s.Status = "failed"
		s.Reason = "Sandbox image volume metadata could not be verified"
		s.Fix = "Use a locally built counterweight:local image"
		d.setStatus(s)
		return s
	}
	for path := range declared {
		// The application image declares this volume; the task bind replaces it.
		// Other declared volumes would become writable anonymous mounts.
		if path != WorkspacePath {
			s.Status = "failed"
			s.Reason = "Sandbox image declares an unsupported writable volume"
			s.Fix = "Use an image whose only declared volume is /app/data (or no declared volumes)"
			d.setStatus(s)
			return s
		}
	}
	probeOptions := d.opts
	probeOptions.Prefix = "cw-sbx-test-"
	probe := NewDockerBackend(probeOptions)
	probe.setStatus(s)
	err = probe.selfTest(ctx)
	err = errors.Join(err, probe.Close())
	s.LastSelfTest = time.Now().UTC()
	if err != nil {
		s.Status = "failed"
		s.Reason = err.Error()
		s.Fix = "Check Docker bind sharing and workspace permissions, and use an image with bash, env and coreutils; then recheck"
	}
	d.setStatus(s)
	return s
}

type bind struct {
	Source, Target string
	ReadOnly       bool
}

func taskBinds(in Command) ([]bind, error) {
	workspace, err := filepath.Abs(in.Workspace)
	if err != nil || in.Workspace == "" {
		return nil, errors.New("Task workspace is unavailable")
	}
	if info, e := os.Lstat(workspace); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Task workspace is unavailable")
	}
	paths := map[string]bool{}
	read, write := in.Roots.CommandRead, in.Roots.CommandWrite
	if read == nil {
		read = in.Roots.Read
		write = in.Roots.Write
	}
	for _, p := range read {
		paths[filepath.Clean(p)] = false
	}
	for _, p := range write {
		paths[filepath.Clean(p)] = true
	}
	writable := paths[workspace]
	result := []bind{{workspace, WorkspacePath, !writable}}
	var others []string
	for p := range paths {
		if p == workspace {
			continue
		}
		rel, e := filepath.Rel(workspace, p)
		if e == nil && filepath.IsLocal(rel) {
			continue
		}
		others = append(others, p)
	}
	sort.Strings(others)
	for n, p := range others {
		if !filepath.IsAbs(p) || strings.ContainsAny(p, ",\r\n") {
			return nil, errors.New("Task mount path is unsupported")
		}
		info, e := os.Lstat(p)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("Task mount root is unavailable")
		}
		result = append(result, bind{p, fmt.Sprintf("/roots/%d", n), !paths[p]})
	}
	if strings.ContainsAny(workspace, ",\r\n") {
		return nil, errors.New("Task mount path is unsupported")
	}
	return result, nil
}
func mapDirectory(binds []bind, dir string) (string, error) {
	for _, b := range binds {
		rel, err := filepath.Rel(b.Source, dir)
		if err == nil && filepath.IsLocal(rel) {
			if rel == "." {
				return b.Target, nil
			}
			return b.Target + "/" + filepath.ToSlash(rel), nil
		}
	}
	return "", errors.New("Command working directory is outside task roots")
}

func (d *DockerBackend) createArgs(name, task, digest string, binds []bind) []string {
	args := []string{"create", "--name", name, "--label", ownerLabel + "=" + scopeID(d.opts.Scope), "--label", taskLabel + "=" + task,
		"--user", containerUID, "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "256", "--memory", "1g", "--cpus", "2",
		"--read-only", "--init", "--network", "bridge", "--ipc", "private", "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=128m,mode=1777", "--workdir", WorkspacePath,
		"--entrypoint", "/usr/bin/env"}
	for _, b := range binds {
		m := "type=bind,source=" + b.Source + ",target=" + b.Target
		if b.ReadOnly {
			m += ",readonly"
		}
		args = append(args, "--mount", m)
	}
	// Clear image ENV as well as server ENV. Override the image entrypoint/CMD.
	return append(args, digest, "-i", "PATH=/usr/local/bin:/usr/bin:/bin", "HOME="+WorkspacePath+"/.home", "LANG=C.UTF-8", "TERM=dumb", "/bin/sh", "-c", "while :; do sleep 3600; done")
}

// ContainerEnvironment is deliberately an allowlist. Today's task additions are
// proxy/CA variables and credential masks; none are appropriate inside workers.
func ContainerEnvironment(extra []string) []string {
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + WorkspacePath + "/.home", "LANG=C.UTF-8", "TERM=dumb"}
	for _, v := range extra {
		k, _, ok := strings.Cut(v, "=")
		if ok && strings.HasPrefix(k, "TOOL_") && !strings.ContainsAny(k, "\x00\r\n") {
			env = append(env, v)
		}
	}
	return env
}

func (d *DockerBackend) task(id string) (*dockerTask, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("Container backend is closed")
	}
	t := d.tasks[id]
	if t == nil {
		t = &dockerTask{}
		d.tasks[id] = t
	}
	return t, nil
}

func (d *DockerBackend) remove(t *dockerTask) error {
	if t.name == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := d.cli(ctx, "rm", "-f", "-v", t.name)
	if err == nil {
		t.name = ""
		t.mounts = ""
	}
	if err != nil {
		d.removalFailed()
	}
	return err
}
func (d *DockerBackend) TaskIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := []string{}
	for id := range d.tasks {
		ids = append(ids, id)
	}
	return ids
}
func (d *DockerBackend) Run(ctx context.Context, in Command) (CommandResult, error) {
	s := d.Status()
	if s.Status != "ready" {
		return CommandResult{}, errors.New(s.Reason)
	}
	if in.TaskID == "" {
		return CommandResult{}, errors.New("Task execution identity is unavailable")
	}
	binds, err := taskBinds(in)
	if err != nil {
		return CommandResult{}, err
	}
	dir, err := mapDirectory(binds, in.Dir)
	if err != nil {
		return CommandResult{}, err
	}
	signature, _ := json.Marshal(binds)
	t, err := d.task(in.TaskID)
	if err != nil {
		return CommandResult{}, err
	}
	// Serial execution prevents a timeout reset from killing another foreground call.
	t.mu.Lock()
	for {
		// A removal can drop the entry while this call waited for it; a
		// container started on the dropped entry would never be removed.
		d.mu.Lock()
		current := d.tasks[in.TaskID]
		d.mu.Unlock()
		if current == t {
			break
		}
		t.mu.Unlock()
		if t, err = d.task(in.TaskID); err != nil {
			return CommandResult{}, err
		}
		t.mu.Lock()
	}
	defer t.mu.Unlock()
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return CommandResult{}, errors.New("Container backend is closed")
	}
	if err = ctx.Err(); err != nil {
		return CommandResult{}, err
	}
	ctx, cancelTask := context.WithCancel(ctx)
	t.control.Lock()
	if t.removing {
		t.control.Unlock()
		cancelTask()
		return CommandResult{}, errors.New("Task container is being removed")
	}
	t.cancel = cancelTask
	t.control.Unlock()
	defer cancelTask()
	defer func() { t.control.Lock(); t.cancel = nil; t.control.Unlock() }()
	if t.name != "" && t.mounts != string(signature) {
		if err = d.remove(t); err != nil {
			return CommandResult{}, err
		}
	}
	if t.name == "" {
		access := Roots{}
		for _, b := range binds {
			access.Read = append(access.Read, b.Source)
			if !b.ReadOnly {
				access.Write = append(access.Write, b.Source)
			}
		}
		if err = d.prepareRoots(access); err != nil {
			return CommandResult{}, err
		}
		t.name = d.opts.Prefix + randomID()
		setup, cancel := context.WithTimeout(ctx, 30*time.Second)
		out, e := d.cli(setup, d.createArgs(t.name, in.TaskID, s.ImageDigest, binds)...)
		if e == nil {
			out, e = d.cli(setup, "start", t.name)
		}
		cancel()
		if e != nil {
			cleanupErr := d.remove(t)
			return CommandResult{}, errors.Join(fmt.Errorf("Task container could not start; check Docker bind sharing and workspace access: %s (%w)", out, e), cleanupErr)
		}
		t.mounts = string(signature)
	}
	argv := in.Argv
	if len(argv) == 0 {
		argv = []string{"/bin/bash", "-c", in.Shell}
	}
	args := []string{"exec", "--workdir", dir}
	if in.Stdin != nil {
		args = append(args, "-i")
	}
	args = append(args, t.name, "/usr/bin/env", "-i")
	args = append(args, ContainerEnvironment(in.Env)...)
	args = append(args, argv...)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if in.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, in.Timeout)
		defer cancel()
	}
	out := newLimitedBuffer(in.OutputLimit)
	err = d.runner.Run(runCtx, args, in.Stdin, out)
	res := out.result()
	res.TimedOut = runCtx.Err() == context.DeadlineExceeded
	if runCtx.Err() != nil {
		// Killing docker exec only kills its client. Removing the entire task
		// container kills ALL descendants, even double-forked/new-session children.
		if cleanupErr := d.remove(t); cleanupErr != nil {
			s.Status = "failed"
			s.Reason = "Task container cancellation could not be confirmed"
			s.Fix = "Restore Docker access and recheck execution isolation"
			d.setStatus(s)
			return res, errors.Join(runCtx.Err(), cleanupErr)
		}
		return res, runCtx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			check, cancelCheck := context.WithTimeout(context.Background(), 10*time.Second)
			running, checkErr := d.cli(check, "inspect", "--format", "{{.State.Running}}", t.name)
			cancelCheck()
			if checkErr != nil || running != "true" {
				// Code inside the container can end its keep-alive process (or be
				// OOM-killed), so a stopped container alone says nothing about the
				// backend. Only an unavailable engine or image fails every task.
				availCtx, cancelAvail := context.WithTimeout(context.Background(), 15*time.Second)
				availErr := d.CheckAvailable(availCtx)
				cancelAvail()
				if availErr != nil {
					return res, availErr
				}
				if err := d.remove(t); err != nil {
					return res, err
				}
				return res, ErrTaskContainerLost
			}
			res.ExitCode = exit.ExitCode()
			return res, nil
		}
		return res, err
	}
	return res, nil
}

var _ Backend = (*DockerBackend)(nil)
var _ io.Writer = (*limitedBuffer)(nil)
