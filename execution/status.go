package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/locale"
)

const Host = "host"
const ConfinedFiles = "confined-files"
const Container = "container"

type Capabilities struct {
	FilesConfined       bool `json:"files_confined"`
	ProcessesConfined   bool `json:"processes_confined"`
	NetworkConfined     bool `json:"network_confined"`
	CredentialsIsolated bool `json:"credentials_isolated"`
}

func Caps(mode string) Capabilities {
	return Capabilities{FilesConfined: mode == ConfinedFiles || mode == Container, ProcessesConfined: mode == Container, CredentialsIsolated: mode == Container}
}

type Info struct {
	Mode         string       `json:"mode"`
	Capabilities Capabilities `json:"capabilities"`
	Mixed        bool         `json:"mixed"`
	Assumed      bool         `json:"assumed"`
	ImageDigest  string       `json:"image_digest,omitempty"`
	SourceOnly   bool         `json:"source_only,omitempty"`
}

func (i Info) Description() string {
	if i.Mixed {
		return locale.Text(locale.ServerDefault(), "Mixed execution modes; consult the execution segments for file and process isolation. Network: not restricted.")
	}
	if i.Mode == Container {
		text := locale.Text(locale.ServerDefault(), ContainerLimitations)
		text += " " + locale.Text(locale.ServerDefault(), "Unavailable tool families: background tasks, interactive shells, web fetch, custom command/script tools, stdio/remote MCP, platform management and cross-task orchestration.")
		if i.ImageDigest != "" {
			text += " " + locale.Text(locale.ServerDefault(), "Worker image digest: %s.", i.ImageDigest)
		}
		return text
	}
	if i.SourceOnly && i.Mode == ConfinedFiles {
		return locale.Text(locale.ServerDefault(), "Source-only review: file tools confined to task roots; review agents have no shell. No container command execution is attested.")
	}
	if i.Mode == ConfinedFiles {
		return locale.Text(locale.ServerDefault(), "File tool access confined to task roots; commands, extensions and network access run on the host.")
	}
	if i.Assumed || i.Mode == "" {
		return locale.Text(locale.ServerDefault(), "Host execution assumed (no execution record); files, commands and network access were not isolated by this layer.")
	}
	return locale.Text(locale.ServerDefault(), "Host execution; files, commands and network access are not isolated by this layer.")
}

type Status struct {
	Setting                 bool            `json:"setting"`
	Mode                    string          `json:"mode"`
	Capabilities            Capabilities    `json:"capabilities"`
	Status                  string          `json:"status"`
	Reason                  string          `json:"reason"`
	LastSelfTest            time.Time       `json:"last_self_test"`
	Skipped                 []string        `json:"skipped"`
	Description             string          `json:"description"`
	SelectedMode            string          `json:"selected_mode"`
	Backends                []BackendStatus `json:"backends"`
	UnavailableToolFamilies []string        `json:"unavailable_tool_families"`
	Image                   string          `json:"image,omitempty"`
	ImageDigest             string          `json:"image_digest,omitempty"`
	// Loaded tasks still running under the mode they were admitted with,
	// because the selection changed after they started.
	EarlierModeTasks []string `json:"tasks_on_earlier_mode,omitempty"`
}
type Controller struct {
	mu             sync.RWMutex
	testMu         sync.Mutex
	last           time.Time
	err            error
	skipped        []string
	docker         *DockerBackend
	containerBlock string
}

func NewController() *Controller { c := &Controller{}; c.Recheck(); return c }
func (c *Controller) Recheck() {
	c.testMu.Lock()
	defer c.testMu.Unlock()
	skipped, err := SelfTest(nil)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = time.Now().UTC()
	c.err = err
	c.skipped = skipped
	d := c.docker
	if d != nil {
		c.mu.Unlock()
		d.Recheck(context.Background())
		c.mu.Lock()
	}
}
func (c *Controller) Status(on bool) Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := Status{Setting: on, Mode: Host, Status: "off", LastSelfTest: c.last, Skipped: append([]string{}, c.skipped...)}
	if on {
		s.Mode = ConfinedFiles
		s.Status = "ready"
		if c.last.IsZero() || c.err != nil {
			s.Status = "failed"
			s.Reason = locale.Text(locale.ServerDefault(), "File confinement self-test has not passed")
			if c.err != nil {
				s.Reason = locale.ErrorMessage(locale.ServerDefault(), c.err)
			}
		}
	}
	s.Capabilities = Caps(s.Mode)
	s.SelectedMode = ConfinedFiles
	if !on {
		s.SelectedMode = "off"
	}
	s.Description = (Info{Mode: s.Mode}).Description()
	return s
}

// ParseMode accepts the old persisted boolean and the new global mode names.
func ParseMode(value string) (string, error) {
	switch value {
	case "", "true", ConfinedFiles:
		return ConfinedFiles, nil
	case "false", "off", Host:
		return "off", nil
	case Container:
		return Container, nil
	}
	return "", errors.New("Isolation must be off, confined-files or container")
}

func (c *Controller) ConfigureDocker(o DockerOptions) {
	c.testMu.Lock()
	defer c.testMu.Unlock()
	c.mu.Lock()
	if c.docker != nil {
		c.mu.Unlock()
		return
	}
	d := NewDockerBackend(o)
	c.docker = d
	c.mu.Unlock()
	if o.InContainer || RunningInContainer() {
		d.Recheck(context.Background())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.CleanupOrphans(ctx); err != nil {
		s := d.Status()
		s.Reason = err.Error()
		s.Fix = "Restore Docker engine access, then restart the server"
		d.setStatus(s)
		return
	}
	d.cleaned = true
	d.Recheck(context.Background())
}
func (c *Controller) Docker() *DockerBackend { c.mu.RLock(); defer c.mu.RUnlock(); return c.docker }

// BlockContainer keeps incomplete application integration unselectable even if
// the standalone Docker backend's containment self-test passes.
func (c *Controller) BlockContainer(reason string) {
	c.mu.Lock()
	c.containerBlock = reason
	c.mu.Unlock()
}
func (c *Controller) ModeStatus(mode string) Status {
	s := c.Status(mode != "off")
	s.SelectedMode = mode
	s.UnavailableToolFamilies = []string{}
	s.Backends = []BackendStatus{{Backend: Host, Status: "ready"}, {Backend: ConfinedFiles, Status: c.Status(true).Status, Reason: c.Status(true).Reason}}
	d := c.Docker()
	ds := BackendStatus{Backend: Container, Status: "not-available", Reason: "Container backend has not been initialized", Fix: "Restart the server"}
	if d != nil {
		ds = d.Status()
	}
	c.mu.RLock()
	block := c.containerBlock
	c.mu.RUnlock()
	if block != "" {
		ds.Status = "not-available"
		ds.Reason = block
		ds.Fix = "Complete the SDK session and synchronous task/shutdown cleanup integration before enabling container mode"
	}
	s.Backends = append(s.Backends, ds)
	if mode == Container {
		s.Mode = Container
		s.Status = ds.Status
		s.Reason = ds.Reason
		s.LastSelfTest = ds.LastSelfTest
		s.Image = ds.Image
		s.ImageDigest = ds.ImageDigest
		s.Capabilities = Caps(Container)
		s.Description = (Info{Mode: Container}).Description() + " " + locale.Text(locale.ServerDefault(), "A chat the operator opens, not started from a task, keeps the platform-management and cross-task tools; its commands run in that conversation's own container.")
		s.UnavailableToolFamilies = []string{"background-tasks", "interactive-shells", "web-fetch", "custom-command-tools", "custom-script-tools", "stdio-mcp", "remote-mcp", "platform-management", "cross-task-orchestration"}
		if fs := c.Status(true); fs.Status != "ready" {
			s.Status = "failed"
			s.Reason = fs.Reason
		}
	}
	return s
}

// SelfTest exercises the production tools. substitute is only for negative tests
// proving that an unconfined tool fails the probe.
func SelfTest(substitute func(actool.CoreTool) actool.CoreTool) ([]string, error) {
	base, err := os.MkdirTemp("", "counterweight-file-test-*")
	if err != nil {
		return nil, errors.New("File confinement self-test could not create its fixture")
	}
	defer os.RemoveAll(base)
	workspace := filepath.Join(base, "workspace")
	if err = os.Mkdir(workspace, 0700); err != nil {
		return nil, errors.New("File confinement self-test could not create its workspace")
	}
	outside := filepath.Join(base, "planted.txt")
	marker := "counterweight-boundary-marker"
	if err = os.WriteFile(outside, []byte(marker), 0600); err != nil {
		return nil, errors.New("File confinement self-test could not plant its fixture")
	}
	files, err := Open(Roots{Read: []string{workspace}, Write: []string{workspace}})
	if err != nil {
		return nil, err
	}
	defer files.Close()
	read, write := files.Tool(actool.NewRead()), files.Tool(actool.NewWrite())
	if substitute != nil {
		read = substitute(read)
		write = substitute(write)
	}
	tc := &actool.ToolContext{WorkingDir: workspace}
	call := func(t actool.CoreTool, p string) actool.Result {
		raw, _ := json.Marshal(map[string]any{"file_path": p, "content": "written"})
		res, e := t.Call(context.Background(), raw, tc)
		if e != nil {
			return actool.Errorf("tool call failed")
		}
		return res
	}
	paths := []string{outside, filepath.Join("..", "planted.txt")}
	skipped := []string{}
	if err = os.Symlink(base, filepath.Join(workspace, "escape")); err != nil {
		skipped = append(skipped, locale.Text(locale.ServerDefault(), "Symbolic link read/write probes skipped: platform could not create a symbolic link"))
	} else {
		paths = append(paths, filepath.Join("escape", "planted.txt"))
	}
	for n, p := range paths {
		r := call(read, p)
		if !r.IsError || strings.Contains(r.Flatten(), marker) {
			return skipped, fmt.Errorf("File confinement self-test failed: read escape route %d", n+1)
		}
		w := call(write, p)
		if !w.IsError {
			return skipped, fmt.Errorf("File confinement self-test failed: write escape route %d", n+1)
		}
		b, e := os.ReadFile(outside)
		if e != nil || string(b) != marker {
			return skipped, errors.New("File confinement self-test failed: outside marker changed")
		}
	}
	if r := call(write, "inside.txt"); r.IsError {
		return skipped, errors.New("File confinement self-test failed: inside write")
	}
	if r := call(read, "inside.txt"); r.IsError || !strings.Contains(r.Flatten(), "written") {
		return skipped, errors.New("File confinement self-test failed: inside read")
	}
	return skipped, nil
}
