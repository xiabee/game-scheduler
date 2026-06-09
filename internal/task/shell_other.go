//go:build !windows

package task

import "os/exec"

// shellCommand runs an arbitrary shell command line via /bin/sh. (The supported
// tools target Windows; this keeps the package building and testable elsewhere.)
func shellCommand(line string) *exec.Cmd {
	return exec.Command("sh", "-c", line)
}
