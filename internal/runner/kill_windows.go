//go:build windows

package runner

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

// killProcessTree terminates the process and all of its descendants. Go's
// default cancel only kills the direct child; the automation tools fork helper
// processes, so we use taskkill /T to take down the whole tree.
func killProcessTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run(); err != nil {
		// The process may have exited on its own in the race window before the
		// kill fired (e.g. the task finishing exactly at its timeout);
		// taskkill then reports a dead PID. Treat an already-dead process as
		// success so a natural exit is not misclassified as a kill failure.
		if kerr := p.Kill(); kerr == nil || errors.Is(kerr, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	return nil
}
