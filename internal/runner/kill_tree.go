package runner

import (
	"github.com/shirou/gopsutil/v4/process"
)

// descendantPIDs returns the PIDs of all living descendants of root (root
// itself excluded), walking the parent→child snapshot of the current process
// table. On Windows a child's recorded PPID keeps pointing at a dead parent,
// so the walk still finds the tree after the root exited; POSIX systems
// reparent orphans, there the snapshot is only reliable while the root lives.
func descendantPIDs(root int32) []int32 {
	procs, err := process.Processes()
	if err != nil {
		return nil
	}
	children := make(map[int32][]int32, len(procs))
	for _, pr := range procs {
		ppid, err := pr.Ppid()
		if err != nil {
			continue
		}
		children[ppid] = append(children[ppid], pr.Pid)
	}
	var out []int32
	seen := map[int32]bool{root: true}
	queue := []int32{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
				queue = append(queue, c)
			}
		}
	}
	return out
}

// killProcesses terminates each PID, ignoring individual failures: a process
// may have exited on its own between enumeration and kill, or may be
// unkillable — the caller only gets a best-effort sweep.
func killProcesses(pids []int32) {
	for _, pid := range pids {
		if pr, err := process.NewProcess(pid); err == nil {
			_ = pr.Kill()
		}
	}
}
