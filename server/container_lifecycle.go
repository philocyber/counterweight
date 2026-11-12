package server

import (
	"log"
	"sync"

	"github.com/Philocyber/counterweight/execution"
	"github.com/Philocyber/counterweight/locale"
)

// Registrations keep cleanup independent of the task lifetime until Manager.Close.
var containerLifecycles sync.Map // *Manager -> *execution.Controller

func (m *Manager) registerContainerExecution(c *execution.Controller) {
	containerLifecycles.Store(m, c)
}

func (e *Engine) removeTaskContainer(taskID string) {
	if e.execution == nil || e.execution.Docker() == nil {
		return
	}
	if err := e.execution.Docker().RemoveTask(taskID); err != nil {
		log.Printf(locale.Text(locale.ServerDefault(), "Task container cleanup failed for %s: %v"), taskID, err)
	}
}

// releaseExecution ends a task's admitted execution. The cached mode goes with
// the container, so any later run on the task (a chat turn, a retest, a fix)
// is admitted again under the mode selected at that time and records it.
func (e *Engine) releaseExecution(taskID string) {
	e.executionMu.Lock()
	e.executionModes.Delete(taskID)
	e.executionMu.Unlock()
	e.removeTaskContainer(taskID)
}

func (m *Manager) closeContainerExecution() error {
	v, ok := containerLifecycles.LoadAndDelete(m)
	if !ok {
		return nil
	}
	d := v.(*execution.Controller).Docker()
	if d == nil {
		return nil
	}
	err := d.Close()
	if err != nil {
		log.Printf(locale.Text(locale.ServerDefault(), "Container shutdown cleanup failed: %v"), err)
	}
	return err
}
