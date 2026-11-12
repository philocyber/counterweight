package subscription

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type packet struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
type rpc struct {
	cmd      *exec.Cmd
	in       io.WriteCloser
	messages chan packet
	done     chan struct{}
	scanErr  error
	stderr   *stderrTail
	queue    []packet
	seq      int
	once     sync.Once
	cancel   context.CancelFunc
}

func runtimeEnv(dir string) []string {
	env := []string{}
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "CODEX_") || strings.HasPrefix(key, "OPENAI_") || strings.HasPrefix(key, "CHATGPT_") {
			continue
		}
		env = append(env, e)
	}
	return append(env, "CODEX_HOME="+dir)
}
func (m *Manager) launch(ctx context.Context, dir string) (*rpc, error) {
	ctx, cancel := context.WithCancel(ctx)
	binary := os.Getenv("COUNTERWEIGHT_CODEX_BINARY")
	if binary == "" {
		binary = "codex"
	}
	cmd := exec.CommandContext(ctx, binary, "app-server", "--listen", "stdio://")
	if m.command != nil {
		cmd = m.command(ctx)
	}
	cmd.Env = append(runtimeEnv(dir), cmd.Env...)
	cmd.Dir = dir
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = errWrite
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		errRead.Close()
		errWrite.Close()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		errRead.Close()
		errWrite.Close()
		return nil, err
	}
	err = cmd.Start()
	errWrite.Close()
	if err != nil {
		cancel()
		errRead.Close()
		return nil, errors.New("Could not start Codex. Install the Codex CLI on the backend host, or set COUNTERWEIGHT_CODEX_BINARY")
	}
	tail := &stderrTail{done: make(chan struct{})}
	go func() {
		defer close(tail.done)
		defer errRead.Close()
		_, _ = io.Copy(tail, errRead)
	}()
	c := &rpc{cmd: cmd, in: in, messages: make(chan packet, 256), done: make(chan struct{}), cancel: cancel, stderr: tail}
	go func() {
		defer close(c.done)
		defer close(c.messages)
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), 8<<20)
		for scanner.Scan() {
			var p packet
			if json.Unmarshal(scanner.Bytes(), &p) != nil {
				continue
			}
			select {
			case c.messages <- p:
			case <-ctx.Done():
				return
			}
		}
		c.scanErr = scanner.Err()
	}()
	if _, err = c.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "counterweight", "title": "Counterweight", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}}); err != nil {
		c.close()
		return nil, err
	}
	if err = c.send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}
func (c *rpc) send(v any) error { return json.NewEncoder(c.in).Encode(v) }
func (c *rpc) read(ctx context.Context) (packet, error) {
	select {
	case <-ctx.Done():
		return packet{}, ctx.Err()
	case p, ok := <-c.messages:
		if !ok {
			if c.scanErr != nil {
				return packet{}, c.scanErr
			}
			msg := "Codex runtime stopped before completing the request"
			if detail := c.stderr.last(); detail != "" {
				msg += ": " + detail
			}
			return packet{}, errors.New(msg)
		}
		return p, nil
	}
}

// stderrTail keeps the end of Codex stderr so startup failures are explainable.
type stderrTail struct {
	mu   sync.Mutex
	buf  []byte
	done chan struct{}
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

// last returns the final non-empty stderr lines, waiting briefly for the
// process to flush them after stdout closed.
func (t *stderrTail) last() string {
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := []string{}
	for _, line := range strings.Split(string(t.buf), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	out := strings.Join(lines, " | ")
	if len(out) > 600 {
		out = out[len(out)-600:]
	}
	return out
}
func (c *rpc) next(ctx context.Context) (packet, error) {
	if len(c.queue) > 0 {
		p := c.queue[0]
		c.queue = c.queue[1:]
		return p, nil
	}
	return c.read(ctx)
}
func (c *rpc) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.seq++
	id := c.seq
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		p, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		if p.Method == "" && string(p.ID) == fmt.Sprint(id) {
			if p.Error != nil {
				return nil, fmt.Errorf("Codex %s: %s", method, p.Error.Message)
			}
			return p.Result, nil
		}
		if p.Method != "" && len(p.ID) > 0 && p.Method != "item/tool/call" {
			c.deny(p)
			continue
		}
		c.queue = append(c.queue, p)
		if len(c.queue) > 4096 {
			return nil, errors.New("Codex emitted too many events before its RPC response")
		}
	}
}
func (c *rpc) deny(p packet) {
	_ = c.send(map[string]any{"id": p.ID, "error": map[string]any{"code": -32601, "message": "This action must be executed through Counterweight tools"}})
}
func (c *rpc) close() {
	c.once.Do(func() { c.cancel(); _ = c.in.Close(); _ = c.cmd.Process.Kill(); _ = c.cmd.Wait() })
}
