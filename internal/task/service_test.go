package task

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/events"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/runner"
	"github.com/xiabee/game-scheduler/internal/store"
)

// TestTaskHelper is re-executed as a child process to simulate an external tool.
func TestTaskHelper(t *testing.T) {
	if os.Getenv("GS_WANT_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	switch args[0] {
	case "exit":
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "sleep":
		d, _ := time.ParseDuration(args[1])
		time.Sleep(d)
	}
	os.Exit(0)
}

// stubAdapter maps task.Type to a helper-process invocation.
type stubAdapter struct{}

func (stubAdapter) Key() string               { return "stub" }
func (stubAdapter) TaskTypes() []string       { return []string{"ok", "fail", "sleep"} }
func (stubAdapter) Validate(store.Game) error { return nil }
func (stubAdapter) BuildCommand(g store.Game, t store.Task) (runner.Spec, error) {
	base := []string{"-test.run=TestTaskHelper", "--"}
	var rest []string
	switch t.Type {
	case "ok":
		rest = []string{"exit", "0"}
	case "fail":
		rest = []string{"exit", "5"}
	case "sleep":
		rest = []string{"sleep", "600ms"}
	}
	to := time.Duration(0)
	if t.TimeoutSec > 0 {
		to = time.Duration(t.TimeoutSec) * time.Second
	}
	return runner.Spec{Path: os.Args[0], Args: append(base, rest...),
		Env: []string{"GS_WANT_HELPER=1"}, Timeout: to}, nil
}

func newSvc(t *testing.T, maxConc int) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reg := game.NewRegistry(stubAdapter{})
	cfg := config.Config{MaxConcurrent: maxConc, DataDir: t.TempDir()}
	return NewService(st, reg, cfg, events.New(), nil), st
}

func mkTask(t *testing.T, st *store.Store, typ string, retries int) store.Task {
	t.Helper()
	if _, err := st.GetGame("stub"); err != nil {
		st.CreateGame(store.Game{ID: "stub", Name: "stub", Adapter: "stub", ToolPath: os.Args[0], Enabled: true})
	}
	tk, err := st.CreateTask(store.Task{GameID: "stub", Name: typ, Type: typ, MaxRetries: retries, TimeoutSec: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func waitStatus(t *testing.T, st *store.Store, id int64, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e, err := st.GetExecution(id)
		if err == nil && e.Status == want {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	e, _ := st.GetExecution(id)
	t.Fatalf("exec %d: want status %q, got %q", id, want, e.Status)
}

func TestRunSuccess(t *testing.T) {
	svc, st := newSvc(t, 1)
	tk := mkTask(t, st, "ok", 0)
	e, skipped, err := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	if err != nil || skipped {
		t.Fatalf("enqueue: %v skipped=%v", err, skipped)
	}
	waitStatus(t, st, e.ID, store.StatusSuccess, 3*time.Second)
}

func TestRunFailureWithRetryAndScreenshot(t *testing.T) {
	svc, st := newSvc(t, 1)
	tk := mkTask(t, st, "fail", 1) // 1 retry => 2 attempts
	e, _, _ := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	waitStatus(t, st, e.ID, store.StatusFailed, 5*time.Second)
	got, _ := st.GetExecution(e.ID)
	if got.RetryCount != 1 {
		t.Errorf("retry_count=%d want 1", got.RetryCount)
	}
	if got.ExitCode == nil || *got.ExitCode != 5 {
		t.Errorf("exit_code=%v want 5", got.ExitCode)
	}
	if got.ScreenshotPath == "" {
		t.Error("expected screenshot_path recorded on failure")
	}
	if got.ErrorMsg == "" {
		t.Error("expected error message")
	}
}

func TestNotifyOnFailure(t *testing.T) {
	svc, st := newSvc(t, 1)
	var mu sync.Mutex
	var events []string
	svc.SetNotify(func(event, title, message string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	tk := mkTask(t, st, "fail", 0)
	e, _, _ := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	waitStatus(t, st, e.ID, store.StatusFailed, 5*time.Second)
	// Give the alert (fired right before the final log) a moment.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0] != "task_failed" {
		t.Fatalf("expected one task_failed alert, got %v", events)
	}
}

func TestSerialization(t *testing.T) {
	svc, st := newSvc(t, 1)
	tk := mkTask(t, st, "sleep", 0)
	e1, _, _ := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	e2, _, _ := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)

	waitStatus(t, st, e1.ID, store.StatusRunning, 2*time.Second)
	// While e1 runs, e2 must still be queued (concurrency 1).
	if g2, _ := st.GetExecution(e2.ID); g2.Status != store.StatusPending {
		t.Fatalf("e2 status=%q want pending while e1 runs", g2.Status)
	}
	waitStatus(t, st, e1.ID, store.StatusSuccess, 3*time.Second)
	waitStatus(t, st, e2.ID, store.StatusSuccess, 3*time.Second)
}

func TestSkipIfActive(t *testing.T) {
	svc, st := newSvc(t, 1)
	tk := mkTask(t, st, "sleep", 0)
	e1, _, _ := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	waitStatus(t, st, e1.ID, store.StatusRunning, 2*time.Second)

	_, skipped, err := svc.Enqueue(tk.ID, store.TriggerSchedule, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !skipped {
		t.Error("expected scheduled fire to be skipped while task active")
	}
}

func TestCancel(t *testing.T) {
	svc, st := newSvc(t, 1)
	tk := mkTask(t, st, "sleep", 0)
	tk.TimeoutSec = 30
	st.UpdateTask(tk)
	e, _, _ := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	waitStatus(t, st, e.ID, store.StatusRunning, 2*time.Second)
	if err := svc.Cancel(e.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitStatus(t, st, e.ID, store.StatusCancelled, 3*time.Second)
}

func TestPreflight(t *testing.T) {
	svc, st := newSvc(t, 1)
	tk := mkTask(t, st, "ok", 0)
	pf, err := svc.Preflight(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !pf.Ready { // os.Args[0] exists, stub validates
		t.Errorf("preflight not ready: %+v", pf)
	}
	if pf.Command == "" {
		t.Error("expected a command")
	}
}
