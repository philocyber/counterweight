//go:build windows

package main

import "os/exec"

// setProcessGroup is a no-op on Windows; the child is terminated directly.
func setProcessGroup(cmd *exec.Cmd) {}

// terminate kills the child process (best effort).
func terminate(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
