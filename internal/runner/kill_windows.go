//go:build windows

package runner

import (
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
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run()
}
