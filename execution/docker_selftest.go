package execution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func quoteShell(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// selfTest exercises the same mount, user, environment and cancellation path as
// real task commands. No successful daemon probe substitutes for these checks.
func (d *DockerBackend) selfTest(ctx context.Context) error {
	parent := d.opts.DataDir
	base, err := os.MkdirTemp(parent, ".cw-sbx-test-")
	if err != nil {
		return errors.New("Container self-test could not create its fixture in the data directory")
	}
	defer os.RemoveAll(base)
	workspace := filepath.Join(base, "workspace")
	if err = os.Mkdir(workspace, 0700); err != nil {
		return err
	}
	if err = prepareContainerRoots(Roots{Read: []string{workspace}, Write: []string{workspace}}); err != nil {
		return err
	}
	marker := "cw-sbx-marker-" + randomID()
	outside := filepath.Join(base, "outside.txt")
	if err = os.WriteFile(outside, []byte(marker), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(workspace, "server.txt"), []byte(marker), 0644); err != nil {
		return err
	}
	key := "CW_SBX_PLANTED_" + strings.ToUpper(randomID())
	if err = os.Setenv(key, marker); err != nil {
		return err
	}
	defer os.Unsetenv(key)
	id := "selftest-" + randomID()
	defer d.RemoveTask(id)
	in := Command{TaskID: id, Workspace: workspace, Dir: workspace, Roots: Roots{Read: []string{workspace}, Write: []string{workspace}}, Timeout: 15 * time.Second, OutputLimit: 8192}
	run := func(label, command string) error {
		in.Shell = command
		res, e := d.Run(ctx, in)
		if e != nil || res.ExitCode != 0 {
			return fmt.Errorf("Container self-test %s failed: %s (%v)", label, res.Output, e)
		}
		return nil
	}
	checks := []struct{ label, command string }{
		{"host file exclusion", "test ! -e " + quoteShell(outside) + " && test ! -e /var/run/docker.sock"},
		{"environment exclusion", "test -z \"${" + key + "+present}\" && test -z \"${COUNTERWEIGHT_PG_DSN+present}\""},
		{"read-only runtime", "if (echo escape > /etc/cw-sbx-outside) 2>/dev/null; then exit 1; fi"},
		{"unprivileged process", "test \"$(id -u):$(id -g)\" = 65532:65532 && grep -q '^CapEff:[[:space:]]*0000000000000000$' /proc/self/status && if mount -t tmpfs tmpfs /mnt 2>/dev/null; then exit 1; fi"},
		{"shared workspace", "test \"$(cat server.txt)\" = " + quoteShell(marker) + " && printf '%s' " + quoteShell(marker) + " > container.txt"},
	}
	for _, c := range checks {
		if err = run(c.label, c.command); err != nil {
			return err
		}
	}
	b, err := os.ReadFile(filepath.Join(workspace, "container.txt"))
	if err != nil || string(b) != marker {
		return errors.New("Container self-test shared workspace write was not visible to the server")
	}
	// Both the parent and a detached child would write after timeout. Container
	// removal must eliminate them, not merely the docker exec client.
	in.Timeout = 500 * time.Millisecond
	in.Shell = "(sleep 2; echo survived > child-survived) & echo started > timeout-started; sleep 30; echo survived > parent-survived"
	res, err := d.Run(ctx, in)
	if !res.TimedOut || !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("Container self-test timeout was not enforced: %v", err)
	}
	if _, err = os.Stat(filepath.Join(workspace, "timeout-started")); err != nil {
		return errors.New("Container self-test timeout fixture never started")
	}
	// Successful docker rm confirms removal of the PID namespace. Delayed markers
	// provide a second observation of descendant termination.
	t, _ := d.task(id)
	t.mu.Lock()
	name := t.name
	t.mu.Unlock()
	if name != "" {
		return errors.New("Container self-test timed-out container remained registered")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2200 * time.Millisecond):
	}
	for _, p := range []string{"parent-survived", "child-survived"} {
		if _, err = os.Stat(filepath.Join(workspace, p)); !os.IsNotExist(err) {
			return errors.New("Container self-test descendant survived cancellation")
		}
	}
	return nil
}
