//go:build !windows

package shellcmd

import (
	"context"
	"os/exec"
)

// Command runs the given command line via /bin/sh. (The supported tools target
// Windows; this keeps the project building and testable elsewhere.)
func Command(line string) *exec.Cmd {
	return exec.Command("sh", "-c", line)
}

// CommandContext is Command with a context, so a slow hook can be abandoned by
// the caller.
func CommandContext(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", line)
}
