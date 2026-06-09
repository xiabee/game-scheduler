//go:build windows

package task

import (
	"os/exec"
	"syscall"
)

// shellCommand builds a command that runs an arbitrary shell command line.
//
// On Windows we must bypass Go's automatic argument quoting: the screenshot
// command line typically contains its own quotes (e.g. a PowerShell one-liner,
// or a quoted destination path), and `exec.Command("cmd","/C",line)` would wrap
// it in another layer of quotes that cmd.exe mis-parses. Setting CmdLine
// directly with `cmd /S /C "<line>"` makes cmd strip exactly the outer quotes
// and run the rest verbatim.
func shellCommand(line string) *exec.Cmd {
	c := exec.Command("cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /S /C "` + line + `"`}
	return c
}
