package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/agentcore"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/execution"
)

func spillFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestContainerToolsNeverSpillOutput(t *testing.T) {
	spill := t.TempDir()
	big := strings.Repeat("x", 500)
	tc := func() *actool.ToolContext {
		return &actool.ToolContext{OutputDir: spill, MaxOutputChars: 100}
	}
	producer := actool.Build(actool.Spec{Name: "producer", Schema: map[string]any{"type": "object"}, Run: func(_ context.Context, _ json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
		return actool.Text(actool.Capture(tc, big)), nil
	}})

	if _, err := producer.Call(t.Context(), nil, tc()); err != nil {
		t.Fatal(err)
	}
	if spillFiles(t, spill) != 1 {
		t.Fatal("control: an unwrapped tool should spill")
	}
	clearDir(t, spill)

	res, err := (&containerCatalogTool{producer}).Call(t.Context(), nil, tc())
	if err != nil || strings.Contains(res.Flatten(), "persisted-output") {
		t.Fatalf("catalog tool spilled: %v %s", err, res.Flatten())
	}
	if n := spillFiles(t, spill); n != 0 {
		t.Fatalf("catalog tool created %d spill file(s)", n)
	}

	b := &containerBackendFixture{result: execution.CommandResult{Output: big}}
	bash := &containerBash{CoreTool: actool.NewBash(), roots: execution.Roots{Backend: b, TaskID: "t"}}
	res, err = bash.Call(t.Context(), json.RawMessage(`{"command":"printf x"}`), tc())
	if err != nil || strings.Contains(res.Flatten(), "persisted-output") {
		t.Fatalf("container shell spilled: %v %s", err, res.Flatten())
	}
	if n := spillFiles(t, spill); n != 0 {
		t.Fatalf("container shell created %d spill file(s)", n)
	}

	opts := agentcore.Options{Tools: []actool.CoreTool{&containerCatalogTool{producer}}, ToolOutputDir: spill}
	configureContainerOptions(&opts)
	if opts.ToolOutputDir != "" {
		t.Fatal("session keeps a spill directory in container mode")
	}
	host := agentcore.Options{Tools: []actool.CoreTool{producer}, ToolOutputDir: spill}
	configureContainerOptions(&host)
	if host.ToolOutputDir != spill {
		t.Fatal("spill directory removed outside container mode")
	}
}

func clearDir(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

func TestEnsureRunDirRefusesPlantedLinks(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	dir, err := ensureRunDir(base, 7, 3)
	if fi, statErr := os.Stat(dir); err != nil || statErr != nil || !fi.IsDir() || dir != filepath.Join(base, "tasks", "7", "i3") {
		t.Fatalf("run directory not created: %v %s", err, dir)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if dir, err := ensureRunDir(base, 7, 3); err == nil {
		t.Fatalf("a planted link was accepted as the run directory: %s", dir)
	}
	if n := spillFiles(t, outside); n != 0 {
		t.Fatalf("link target was modified: %d entries", n)
	}
	// A link in an intermediate component is refused before anything is made below it.
	if err := os.RemoveAll(filepath.Join(base, "tasks", "8")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "tasks", "8")); err != nil {
		t.Fatal(err)
	}
	if dir, err := ensureRunDir(base, 8, 5); err == nil {
		t.Fatalf("a run directory below a planted link was accepted: %s", dir)
	}
	if n := spillFiles(t, outside); n != 0 {
		t.Fatalf("link target was modified through an intermediate link: %d entries", n)
	}
}

type lostBackend struct{ err error }

func (b lostBackend) Run(context.Context, execution.Command) (execution.CommandResult, error) {
	return execution.CommandResult{}, b.err
}

func TestContainerBashLostContainerDoesNotHoldTask(t *testing.T) {
	held := 0
	bash := &containerBash{CoreTool: actool.NewBash(), roots: execution.Roots{Backend: lostBackend{execution.ErrTaskContainerLost}, TaskID: "t"}, failed: func(context.Context, error) { held++ }}
	res, err := bash.Call(t.Context(), json.RawMessage(`{"command":"true"}`), nil)
	if err != nil || !res.IsError || held != 0 {
		t.Fatalf("lost container: err=%v held=%d %s", err, held, res.Flatten())
	}
	bash.roots.Backend = lostBackend{os.ErrClosed}
	_, _ = bash.Call(t.Context(), json.RawMessage(`{"command":"true"}`), nil)
	if held != 1 {
		t.Fatal("an unrelated backend failure no longer holds the task")
	}
}
