//go:build windows

package runner

import (
	"os"
	"os/exec"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/shirou/gopsutil/v4/process"
)

// assignJob puts the (just started) child process into a fresh kill-on-close
// job and returns the release function, which Run must call once the child is
// done: on a natural exit the job is empty and the close is a no-op; if
// processes are still dying the KILL_ON_JOB_CLOSE flag finishes them. One job
// per child (concurrent runs each get their own). It is best-effort: on
// failure the runner still works, only the hard-exit safety net is missing.
// Nested jobs are supported since Windows 8, so assigning a child whose
// parent is already in a job is fine.
func assignJob(p *os.Process) (release func(), err error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(h)
		return func() {}, err
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		_ = windows.CloseHandle(h)
		return func() {}, err
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(h, ph); err != nil {
		_ = windows.CloseHandle(h)
		return func() {}, err
	}
	return func() { _ = windows.CloseHandle(h) }, nil
}

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
