//go:build windows

package runner

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// spawnTree starts a helper process that spawns its own long-lived grandchild
// (cmd.exe running ping), prints "GCPID=<pid>" on stdout, and either exits
// right away (dead root, hold=false) or holds the tree alive (hold=true).
func spawnTree(t *testing.T, hold bool) (*exec.Cmd, int) {
	t.Helper()
	mode := "spawn_exit"
	if hold {
		mode = "spawn_hold"
	}
	helper := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", mode, "60s")
	helper.Env = append(os.Environ(), "GS_WANT_HELPER=1")
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if pid, ok := strings.CutPrefix(sc.Text(), "GCPID="); ok {
			n, err := strconv.Atoi(strings.TrimSpace(pid))
			if err != nil {
				t.Fatalf("bad GCPID %q: %v", pid, err)
			}
			return helper, n
		}
	}
	t.Fatal("helper did not report a grandchild PID")
	return nil, 0
}

func pidExists(pid int) bool {
	ok, err := process.PidExists(int32(pid))
	return err == nil && ok
}

func waitPid(t *testing.T, pid int, wantAlive bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if pidExists(pid) == wantAlive {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("pid %d: want alive=%v not reached within %s", pid, wantAlive, within)
}

// A launcher that exits on its own must not leave its grandchildren running:
// killProcessTree on an already-dead root sweeps the surviving tree by PPID.
// taskkill alone cannot do this — it fails on the dead PID and the old fallback
// treated the dead handle as success.
func TestKillProcessTreeSweepsGrandchildrenWhenRootDead(t *testing.T) {
	helper, gcPID := spawnTree(t, false)
	if err := helper.Wait(); err != nil {
		t.Fatalf("helper exit: %v", err)
	}
	waitPid(t, gcPID, true, 5*time.Second)

	if err := killProcessTree(helper.Process); err != nil {
		t.Fatalf("killProcessTree: %v", err)
	}
	waitPid(t, gcPID, false, 10*time.Second)
}

// The regular cancel/timeout path: a live root dies together with its tree.
func TestKillProcessTreeKillsLiveTree(t *testing.T) {
	helper, gcPID := spawnTree(t, true)
	waitPid(t, gcPID, true, 5*time.Second)

	if err := killProcessTree(helper.Process); err != nil {
		t.Fatalf("killProcessTree: %v", err)
	}
	waitPid(t, helper.Process.Pid, false, 10*time.Second)
	waitPid(t, gcPID, false, 10*time.Second)
}
