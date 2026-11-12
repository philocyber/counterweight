//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so we can signal the
// whole group — `go run` spawns the compiled binary as a grandchild, and killing
// only the `go` process would orphan it.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate signals the child's entire process group.
func terminate(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}
