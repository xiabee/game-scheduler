package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/events"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/game/genshin"
	"github.com/xiabee/game-scheduler/internal/game/hsr"
	"github.com/xiabee/game-scheduler/internal/game/r1999"
	"github.com/xiabee/game-scheduler/internal/game/wuwa"
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

func TestRunUpdatesRouteStats(t *testing.T) {
	svc, st := newSvc(t, 1)
	rt, err := st.CreateRoute(store.Route{GameID: "stub", Adapter: "stub", RouteType: "daily", Name: "daily"})
	if err != nil {
		st.CreateGame(store.Game{ID: "stub", Name: "stub", Adapter: "stub", ToolPath: os.Args[0], Enabled: true})
		rt, err = st.CreateRoute(store.Route{GameID: "stub", Adapter: "stub", RouteType: "daily", Name: "daily"})
	}
	if err != nil {
		t.Fatal(err)
	}
	okTask, err := st.CreateTask(store.Task{GameID: "stub", RouteID: &rt.ID, Name: "ok", Type: "ok", TimeoutSec: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	e, _, _ := svc.Enqueue(okTask.ID, store.TriggerManual, nil, false)
	waitStatus(t, st, e.ID, store.StatusSuccess, 3*time.Second)
	got, _ := st.GetRoute(rt.ID)
	if got.SuccessCount != 1 || got.FailCount != 0 || got.LastRunAt == nil {
		t.Fatalf("success stats=%+v", got)
	}

	failTask, err := st.CreateTask(store.Task{GameID: "stub", RouteID: &rt.ID, Name: "fail", Type: "fail", TimeoutSec: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	e, _, _ = svc.Enqueue(failTask.ID, store.TriggerManual, nil, false)
	waitStatus(t, st, e.ID, store.StatusFailed, 5*time.Second)
	got, _ = st.GetRoute(rt.ID)
	if got.SuccessCount != 1 || got.FailCount != 1 {
		t.Fatalf("failure stats=%+v", got)
	}
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
	if len(pf.Missing) != 0 {
		t.Errorf("missing=%v", pf.Missing)
	}
}

func TestPreflightReportsMissingAdapterPrereqs(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "tool.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(t.TempDir(), "python.exe")
	if err := os.WriteFile(python, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		game        store.Game
		task        store.Task
		wantMissing []string
	}{
		{
			name: "genshin",
			game: store.Game{ID: "genshin", Name: "genshin", Adapter: "genshin", ToolPath: exe,
				WorkingDir: filepath.Join(t.TempDir(), "missing-work"),
				ExtraConfig: mustJSON(t, map[string]string{
					"scripts_dir": filepath.Join(t.TempDir(), "missing-scripts"),
				}),
				Enabled: true},
			task: store.Task{Name: "one", Type: "onedragon", Params: `{}`, Enabled: true},
			wantMissing: []string{
				"working_dir directory not found",
				"extra_config.scripts_dir directory not found",
			},
		},
		{
			name: "hsr",
			game: store.Game{ID: "hsr", Name: "hsr", Adapter: "hsr",
				ExtraConfig: mustJSON(t, map[string]string{
					"python_path":  python,
					"march7th_dir": filepath.Join(t.TempDir(), "missing-m7"),
				}),
				Enabled: true},
			task: store.Task{Name: "daily", Type: "march7th_daily", Params: `{}`, Enabled: true},
			wantMissing: []string{
				"working_dir directory not found",
				"extra_config.march7th_dir directory not found",
				"extra_config.march7th_entry file not found",
			},
		},
		{
			name: "wuwa",
			game: store.Game{ID: "wuwa", Name: "wuwa", Adapter: "wuwa", ToolPath: exe,
				WorkingDir: filepath.Join(t.TempDir(), "missing-work"), Enabled: true},
			task:        store.Task{Name: "task", Type: "task", Params: `{"task_index":1}`, Enabled: true},
			wantMissing: []string{"working_dir directory not found"},
		},
		{
			name: "r1999",
			game: store.Game{ID: "r1999", Name: "r1999", Adapter: "r1999", ToolPath: exe,
				WorkingDir: filepath.Join(t.TempDir(), "missing-m9a"), Enabled: true},
			task:        store.Task{Name: "run", Type: "run", Params: `{}`, Enabled: true},
			wantMissing: []string{"working_dir directory not found"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			reg := game.NewRegistry(genshin.New(), hsr.New(), wuwa.New(), r1999.New())
			svc := NewService(st, reg, config.Config{MaxConcurrent: 1, DataDir: t.TempDir()}, events.New(), nil)
			if _, err := st.CreateGame(tc.game); err != nil {
				t.Fatal(err)
			}
			tc.task.GameID = tc.game.ID
			tk, err := st.CreateTask(tc.task)
			if err != nil {
				t.Fatal(err)
			}
			pf, err := svc.Preflight(tk.ID)
			if err != nil {
				t.Fatal(err)
			}
			if pf.Command == "" {
				t.Fatalf("expected command in preflight: %+v", pf)
			}
			if pf.Ready {
				t.Fatalf("preflight unexpectedly ready: %+v", pf)
			}
			for _, want := range tc.wantMissing {
				if !containsSubstring(pf.Missing, want) {
					t.Errorf("missing %q in %v", want, pf.Missing)
				}
			}
		})
	}
}

func TestPreflightHSRPythonEntryReady(t *testing.T) {
	root := t.TempDir()
	python := filepath.Join(root, "python.exe")
	if err := os.WriteFile(python, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	m7 := filepath.Join(root, "March7thAssistant")
	if err := os.MkdirAll(m7, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m7, "main.py"), []byte("print('ok')"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reg := game.NewRegistry(hsr.New())
	svc := NewService(st, reg, config.Config{MaxConcurrent: 1, DataDir: t.TempDir()}, events.New(), nil)
	ec := mustJSON(t, map[string]string{"python_path": python, "march7th_dir": m7})
	if _, err := st.CreateGame(store.Game{ID: "hsr", Name: "hsr", Adapter: "hsr", ExtraConfig: ec, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	tk, err := st.CreateTask(store.Task{GameID: "hsr", Name: "daily", Type: "march7th_daily", Params: `{}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pf, err := svc.Preflight(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !pf.Ready {
		t.Fatalf("preflight not ready: %+v", pf)
	}
	if !containsCheck(pf.Checks, "extra_config.march7th_entry", filepath.Join(m7, "main.py"), true) {
		t.Fatalf("entry check missing: %+v", pf.Checks)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func containsSubstring(items []string, want string) bool {
	for _, item := range items {
		if strings.Contains(item, want) {
			return true
		}
	}
	return false
}

func containsCheck(checks []PreflightCheck, key, path string, exists bool) bool {
	for _, c := range checks {
		if c.Key == key && c.Path == path && c.Exists == exists {
			return true
		}
	}
	return false
}
