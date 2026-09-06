//go:build windows

// Package shellcmd builds a command that runs an arbitrary shell command line,
// used for operator-configured hooks (failure screenshots, notifications).
package shellcmd

import (
	"context"
	"os/exec"
	"syscall"
)

// Command runs the given command line via the platform shell.
//
// On Windows we bypass Go's automatic argument quoting: hook command lines
// usually contain their own quotes (a PowerShell one-liner, a quoted path),
// and exec.Command("cmd","/C",line) would wrap them in another layer that
// cmd.exe mis-parses. Setting CmdLine directly with `cmd /S /C "<line>"` makes
// cmd strip exactly the outer quotes and run the rest verbatim.
func Command(line string) *exec.Cmd {
	c := exec.Command("cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /S /C "` + line + `"`}
	return c
}

// CommandContext is Command with a context, so a slow hook (a webhook that
// never answers) can be abandoned by the caller.
func CommandContext(ctx context.Context, line string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /S /C "` + line + `"`}
	return c
}
