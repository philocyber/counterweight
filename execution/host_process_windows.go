package execution

import (
	"os/exec"
	"strconv"
)

func configureHostProcess(*exec.Cmd) {}
func killHostProcess(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return exec.Command("taskkill", "/PID", strconv.Itoa(c.Process.Pid), "/T", "/F").Run()
}
