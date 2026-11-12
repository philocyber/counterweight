package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

func (d *DockerBackend) removeInstallationContainers(ctx context.Context) error {
	out, err := d.cli(ctx, "ps", "-aq", "--filter", "label="+ownerLabel+"="+scopeID(d.opts.Scope))
	if err != nil {
		return fmt.Errorf("Docker orphan lookup failed: %w", err)
	}
	var result error
	for _, id := range strings.Fields(out) {
		if _, err := d.cli(ctx, "rm", "-f", "-v", id); err != nil {
			result = errors.Join(result, fmt.Errorf("Docker orphan cleanup failed: %w", err))
		}
	}
	return result
}

func (d *DockerBackend) removalFailed() {
	s := d.Status()
	s.Status = "failed"
	s.Reason = "Task container cancellation could not be confirmed"
	s.Fix = "Restore Docker access and recheck execution isolation"
	d.setStatus(s)
}

// RemoveTask bounds both waiting for the command lock and Docker removal.
func (d *DockerBackend) RemoveTask(id string) error {
	d.mu.Lock()
	t := d.tasks[id]
	d.mu.Unlock()
	if t == nil {
		return nil
	}
	t.control.Lock()
	t.removing = true
	if t.cancel != nil {
		t.cancel()
	}
	t.control.Unlock()
	defer func() { t.control.Lock(); t.removing = false; t.control.Unlock() }()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !t.mu.TryLock() {
		select {
		case <-ctx.Done():
			d.removalFailed()
			return ctx.Err()
		case <-tick.C:
		}
	}
	defer t.mu.Unlock()
	err := d.remove(t)
	if err == nil {
		// Identities come and go with conversations; keep no entry for a
		// container that no longer exists.
		d.mu.Lock()
		if d.tasks[id] == t {
			delete(d.tasks, id)
		}
		d.mu.Unlock()
	}
	return err
}

func (d *DockerBackend) Close() error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	// Cancel every command before waiting for any one task's removal.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var result error
	for _, id := range d.TaskIDs() {
		wg.Go(func() {
			err := d.RemoveTask(id)
			mu.Lock()
			result = errors.Join(result, err)
			mu.Unlock()
		})
	}
	wg.Wait()
	// Include installation-labelled containers not retained in the task map.
	if !d.opts.InContainer && !RunningInContainer() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := d.CleanupOrphans(ctx); err != nil {
			d.removalFailed()
			result = errors.Join(result, err)
		}
	}
	return result
}
