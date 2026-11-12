package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/norma/agentcore"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/execution"
	"github.com/Philocyber/counterweight/locale"
)

// ContainerExecution is evaluated BEFORE extension startup as well as after tool
// resolution. Errors suppress executable extensions while admission fails closed.
func ContainerExecution(ctx context.Context, key string) bool {
	rootsFor := FileRoots
	if a, ok := ctx.Value(fileAccessKey{}).(*FileAccess); ok {
		rootsFor = a.roots
	}
	if rootsFor == nil {
		return false
	}
	r, _, err := rootsFor(ctx, key)
	return r.Mode == execution.Container || err != nil
}

type containerBash struct {
	actool.CoreTool
	roots  execution.Roots
	failed func(context.Context, error)
}

func (t *containerBash) Call(ctx context.Context, raw json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var in struct {
		Command    string `json:"command"`
		TimeoutMS  int    `json:"timeout_ms"`
		Background bool   `json:"run_in_background"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return actool.Result{}, err
	}
	if strings.TrimSpace(in.Command) == "" {
		return actool.Errorf("Error: command is required"), nil
	}
	if in.Background {
		return actool.Errorf(locale.Text(locale.FromContext(ctx), "Background commands are unavailable in container mode")), nil
	}
	timeout := 120 * time.Second
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
		if timeout > 600*time.Second {
			timeout = 600 * time.Second
		}
	}
	dir := t.roots.WorkingDir
	var env []string
	if tc != nil {
		if tc.WorkingDir != "" {
			dir = tc.WorkingDir
		}
		env = tc.Env
	}
	if t.roots.Backend == nil {
		return actool.Result{}, errors.New("Container execution backend is unavailable")
	}
	res, err := t.roots.Backend.Run(ctx, execution.Command{TaskID: t.roots.TaskID, Workspace: t.roots.Workspace, Roots: t.roots, Dir: dir, Shell: in.Command, Env: env, Timeout: timeout, OutputLimit: 1 << 20})
	out := res.Output
	if res.Truncated {
		out += "\n[output truncated at 1048576 bytes]"
	}
	out = actool.Capture(withoutSpill(tc), out)
	if res.TimedOut {
		return actool.Errorf(out + fmt.Sprintf("\n\n[command timed out after %s]", timeout)), nil
	}
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, execution.ErrTaskContainerLost) && t.failed != nil {
			t.failed(ctx, err)
		}
		return actool.Errorf(fmt.Sprintf("%s\n\n[failed to run: %v]", out, err)), nil
	}
	if res.ExitCode != 0 {
		if out == "" {
			out = "(no output)"
		}
		return actool.Errorf(fmt.Sprintf("%s\n\n[exit code %d]", out, res.ExitCode)), nil
	}
	if strings.TrimSpace(out) == "" {
		out = "(command produced no output)"
	}
	return actool.Text(out), nil
}

func containerProcessTool(name string) bool {
	return name == "TaskOutput" || name == "TaskStop" || name == "TaskList" || name == "Monitor" || strings.HasPrefix(name, "shell_") || strings.HasPrefix(name, "mcp__")
}

// configureContainerOptions is called immediately before SDK construction so the
// SDK cannot reintroduce the background manager after catalog filtering.
func configureContainerOptions(opts *agentcore.Options) {
	for _, t := range opts.Tools {
		if isContainerTool(t) {
			opts.DisableBackgroundTasks = true
			// The spill directory sits in a mount the container can write; the SDK
			// would otherwise create overflow files there as the server.
			opts.ToolOutputDir = ""
			// WebFetch runs in the server process and on the host's network, so it
			// reaches what the container cannot, such as services bound to the
			// host's loopback. The container has curl for the same use.
			opts.EnableWebFetch = false
			return
		}
	}
}
func isContainerTool(t actool.CoreTool) bool {
	for t != nil {
		if _, ok := t.(*containerCatalogTool); ok {
			return true
		}
		if _, ok := t.(*containerBash); ok {
			return true
		}
		if u, ok := t.(toolUnwrapper); ok {
			t = u.UnwrapTool()
			continue
		}
		if g, ok := t.(*guardedTool); ok {
			t = g.CoreTool
			continue
		}
		return false
	}
	return false
}

type containerCatalogTool struct{ actool.CoreTool }

// withoutSpill returns a copy of tc that has no output spill directory. In
// container mode that directory lies inside a mount the container can write, so
// the server must not create files there: oversized output is truncated in the
// result instead.
func withoutSpill(tc *actool.ToolContext) *actool.ToolContext {
	if tc == nil {
		return nil
	}
	c := *tc
	c.OutputDir = ""
	return &c
}

// Call hands every tool kept in container mode a context without a spill
// directory, so no server-side handler writes through a path the container
// can replace with a link.
func (t *containerCatalogTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	return t.CoreTool.Call(ctx, in, withoutSpill(tc))
}

func (t *containerCatalogTool) UnwrapTool() actool.CoreTool { return t.CoreTool }

type containerServerTool struct{ actool.CoreTool }

func (t *containerServerTool) UnwrapTool() actool.CoreTool { return t.CoreTool }

// ContainerServerTool marks an explicitly classified structured server handler.
// Executable extensions must never receive this marker.
func ContainerServerTool(t actool.CoreTool) actool.CoreTool { return &containerServerTool{t} }
func containerAllowed(t actool.CoreTool) bool {
	switch t.Name() {
	case "Read", "Write", "Edit", "MultiEdit", "LS", "Glob", "Grep", "Skill", "web_search", "TodoWrite", "AskUserQuestion":
		return true
	case "Bash":
		return isContainerTool(t)
	}
	for _, seed := range BuiltinToolSeeds() {
		if seed.Key == t.Name() {
			return true
		}
	}
	for t != nil {
		if _, ok := t.(*containerServerTool); ok {
			return true
		}
		if u, ok := t.(toolUnwrapper); ok {
			t = u.UnwrapTool()
		} else {
			break
		}
	}
	return false
}
