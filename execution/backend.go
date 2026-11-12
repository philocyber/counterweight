package execution

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// Command is code-bound to a principal and its persisted roots, never model roots.
type Command struct {
	TaskID      string
	Roots       Roots
	Workspace   string
	Argv        []string
	Shell       string
	Dir         string
	Stdin       io.Reader
	Env         []string
	Timeout     time.Duration
	OutputLimit int
}

type CommandResult struct {
	Output    string
	ExitCode  int
	TimedOut  bool
	Truncated bool
}

type Backend interface {
	Run(context.Context, Command) (CommandResult, error)
}

// HostBackend is the synchronous host path. Production host tools retain the SDK
// implementation, including its existing background-task behavior.
type HostBackend struct{}

func (HostBackend) Run(ctx context.Context, in Command) (CommandResult, error) {
	if in.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, in.Timeout)
		defer cancel()
	}
	argv := in.Argv
	if len(argv) == 0 {
		argv = []string{"bash", "-c", in.Shell}
		if runtime.GOOS == "windows" {
			argv = []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", in.Shell}
		}
	}
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	configureHostProcess(c)
	c.Cancel = func() error { return killHostProcess(c) }
	c.WaitDelay = 2 * time.Second
	c.Dir, c.Stdin, c.Env = in.Dir, in.Stdin, append(os.Environ(), in.Env...)
	out := newLimitedBuffer(in.OutputLimit)
	c.Stdout, c.Stderr = out, out
	err := c.Run()
	res := out.result()
	res.TimedOut = ctx.Err() == context.DeadlineExceeded
	if c.ProcessState != nil {
		res.ExitCode = c.ProcessState.ExitCode()
	}
	return res, err
}

type limitedBuffer struct {
	mu        sync.Mutex
	b         bytes.Buffer
	limit     int
	truncated bool
}

func newLimitedBuffer(n int) *limitedBuffer {
	if n <= 0 {
		n = 1 << 20
	}
	return &limitedBuffer{limit: n}
}
func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := b.limit - b.b.Len()
	if len(p) > left {
		p = p[:left]
		b.truncated = true
	}
	_, _ = b.b.Write(p)
	return n, nil
}
func (b *limitedBuffer) result() CommandResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	return CommandResult{Output: b.b.String(), Truncated: b.truncated}
}

// DockerRunner is injectable so argument and cancellation tests need no daemon.
type DockerRunner interface {
	Run(context.Context, []string, io.Reader, io.Writer) error
}
type CLIRunner struct{ Path string }

func (r CLIRunner) Run(ctx context.Context, args []string, stdin io.Reader, output io.Writer) error {
	c := exec.CommandContext(ctx, r.Path, args...)
	c.Stdin, c.Stdout, c.Stderr = stdin, output, output
	return c.Run()
}
