package server

import (
	"os/exec"
	"syscall"
)

func hideTemplateParserWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
