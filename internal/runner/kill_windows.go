//go:build windows

package runner

import (
	"os"
	"os/exec"
	"strconv"

	"github.com/shirou/gopsutil/v4/process"
)

// killProcessTree terminates the process and all of its descendants. Go's
// default cancel only kills the direct child; the automation tools fork helper
// processes, so we use taskkill /T to take down the whole tree.
//
// taskkill fails when the root PID is already gone, but a launcher that exited
// on its own leaves its grandchildren alive (e.g. python still driving the
// game) — in that case the tree is swept by walking PPIDs instead, which works
// on Windows because a child's recorded PPID keeps pointing at its dead
// parent. The direct child is killed by PID too (covers a missing taskkill on
// PATH). Failure is reported only when the root still verifiably lives: a
// natural exit in the race window before the kill fired is success, and the
// exited process's own handle is not relied on (after Wait, Kill on it fails
// with a non-ErrProcessDone "invalid argument").
func killProcessTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	pid := int32(p.Pid)
	err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(int(pid))).Run()
	if err == nil {
		return nil
	}
	killProcesses(descendantPIDs(pid))
	killProcesses([]int32{pid})
	if alive, perr := process.PidExists(pid); perr == nil && alive {
		return err
	}
	return nil
}
