//go:build !windows

package server

import "os/exec"

func hideTemplateParserWindow(cmd *exec.Cmd) {}
