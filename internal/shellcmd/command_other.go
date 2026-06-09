//go:build !windows

package shellcmd

import "os/exec"

// Command runs the given command line via /bin/sh. (The supported tools target
// Windows; this keeps the project building and testable elsewhere.)
func Command(line string) *exec.Cmd {
	return exec.Command("sh", "-c", line)
}
