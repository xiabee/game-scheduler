//go:build !windows

package runner

import (
	"errors"
	"os"
)

// killProcessTree falls back to killing the direct process on non-Windows
// platforms. (The supported tools target Windows; this keeps cross-compilation
// and tests working elsewhere.) A process that already exited on its own is
// treated as success so a natural exit at the cancel/timeout boundary is not
// misclassified as a kill failure.
// assignJob is a no-op off Windows: the kill-on-close job safety net does not
// exist there, orphan control is left to the platform (POSIX process groups
// could fill this role later).
func assignJob(*os.Process) (func(), error) { return func() {}, nil }

func killProcessTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	// Snapshot the descendants while the parent is alive: once it dies its
	// children are reparented and a PPID walk can no longer find them.
	descendants := descendantPIDs(int32(p.Pid))
	if err := p.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	killProcesses(descendants)
	return nil
}
