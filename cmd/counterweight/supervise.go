package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Philocyber/counterweight/selfupdate"
)

const superviseMaxDelay = 60 * time.Second

// supervise is the restart loop that start.sh and start.bat used to implement
// separately: exit 0 stops, selfupdate.ExitRestart relaunches immediately, and
// any other exit backs off from 1 to 60 seconds. It runs the executable as a
// child with the remaining arguments, so the child's Bootstrap can swap in a
// staged update and the next launch picks up the new file.
func supervise(args []string) int {
	// Resolve once: after an update renames the running file aside, os.Executable
	// would follow it to counterweight.old on Linux, while the new build sits at this path.
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[Counterweight] Locate executable:", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return superviseLoop(exe, args, time.Second)
}

func superviseLoop(exe string, args []string, delay time.Duration) int {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	initial := delay
	for {
		cmd := exec.Command(exe, args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "[Counterweight] Start:", err)
			return 1
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		var waitErr error
		stopping := false
		select {
		case waitErr = <-done:
		case <-sig:
			stopping = true
			// Console Ctrl-C already reached the child; SIGTERM covers kill/docker stop.
			// Windows cannot deliver SIGTERM, so the error is expected there.
			_ = cmd.Process.Signal(syscall.SIGTERM)
			waitErr = <-done
		}
		if stopping {
			fmt.Println("[Counterweight] Stopped")
			return 0
		}

		code := 0
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code = exitErr.ExitCode()
		} else if waitErr != nil {
			fmt.Fprintln(os.Stderr, "[Counterweight] Wait:", waitErr)
			return 1
		}

		switch code {
		case 0:
			fmt.Println("[Counterweight] Exited normally")
			return 0
		case selfupdate.ExitRestart:
			fmt.Println("[Counterweight] Restart requested (applying update)…")
			delay = initial
			continue
		}
		fmt.Fprintf(os.Stderr, "[Counterweight] Exited with code %d; restarting in %s\n", code, delay)
		select {
		case <-time.After(delay):
		case <-sig:
			fmt.Println("[Counterweight] Stopped")
			return 0
		}
		delay = min(delay*2, superviseMaxDelay)
	}
}
