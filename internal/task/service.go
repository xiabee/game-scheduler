// Package task orchestrates execution of a stored Task: it resolves the game's
// adapter, builds the command, runs it through runner, applies retries, and
// records a full execution log (stdout/stderr/exit code/timestamps, plus error,
// screenshot path and retry count on failure).
//
// Executions are serialized through a bounded run queue (see Service.sem).
// Because every supported tool drives the shared mouse/keyboard and foreground
// window, the default concurrency is 1: a second run waits for the first to
// finish rather than fighting over the screen.
package task

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sync"
	"text/template"
	"time"

	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/runner"
	"github.com/xiabee/game-scheduler/internal/store"
)

// Service runs tasks and records executions.
type Service struct {
	store *store.Store
	reg   *game.Registry
	cfg   config.Config
	log   *slog.Logger

	sem chan struct{} // bounded run slots; cap == cfg.MaxConcurrent

	mu      sync.Mutex
	running map[int64]context.CancelFunc // execID -> cancel
	active  map[int64]int                // taskID -> count of pending/running execs
}

// NewService constructs a task service.
func NewService(s *store.Store, reg *game.Registry, cfg config.Config, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	n := cfg.MaxConcurrent
	if n < 1 {
		n = 1
	}
	return &Service{
		store:   s,
		reg:     reg,
		cfg:     cfg,
		log:     log,
		sem:     make(chan struct{}, n),
		running: map[int64]context.CancelFunc{},
		active:  map[int64]int{},
	}
}

// Enqueue creates a pending execution and schedules it on the run queue,
// returning immediately. If skipIfActive is true and the task already has a
// pending/running execution, no row is created and (zero, true, nil) is
// returned — this is how scheduled fires avoid stacking on top of a run that is
// still going.
func (s *Service) Enqueue(taskID int64, trigger string, planID *int64, skipIfActive bool) (exec store.Execution, skipped bool, err error) {
	if _, err = s.store.GetTask(taskID); err != nil {
		return store.Execution{}, false, err
	}

	s.mu.Lock()
	if skipIfActive && s.active[taskID] > 0 {
		s.mu.Unlock()
		return store.Execution{}, true, nil
	}
	exec, err = s.store.CreateExecution(store.Execution{
		TaskID: taskID, PlanID: planID, Trigger: trigger, Status: store.StatusPending,
	})
	if err != nil {
		s.mu.Unlock()
		return store.Execution{}, false, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.active[taskID]++
	s.running[exec.ID] = cancel
	s.mu.Unlock()

	go s.worker(ctx, exec.ID, taskID)
	return exec, false, nil
}

// worker waits for a run slot, then executes. It owns the execution's context
// lifecycle and the bookkeeping maps.
func (s *Service) worker(ctx context.Context, execID, taskID int64) {
	defer func() {
		s.mu.Lock()
		if cancel, ok := s.running[execID]; ok {
			cancel()
			delete(s.running, execID)
		}
		if s.active[taskID] > 0 {
			s.active[taskID]--
			if s.active[taskID] == 0 {
				delete(s.active, taskID)
			}
		}
		s.mu.Unlock()
	}()

	// Acquire a run slot, or abort if cancelled while still queued.
	select {
	case <-ctx.Done():
		s.markCancelledBeforeStart(execID)
		return
	case s.sem <- struct{}{}:
	}
	defer func() { <-s.sem }()

	if err := s.execute(ctx, execID); err != nil {
		s.log.Error("execution failed to complete", "exec_id", execID, "err", err)
	}
}

// Cancel attempts to stop a queued or running execution.
func (s *Service) Cancel(execID int64) error {
	s.mu.Lock()
	cancel, ok := s.running[execID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("task: execution %d is not active", execID)
	}
	cancel()
	return nil
}

// markCancelledBeforeStart records cancellation of a run that never left the
// queue.
func (s *Service) markCancelledBeforeStart(execID int64) {
	exec, err := s.store.GetExecution(execID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	if exec.StartTime == nil {
		exec.StartTime = &now
	}
	exec.EndTime = &now
	exec.Status = store.StatusCancelled
	exec.ErrorMsg = "cancelled before start (was queued)"
	if err := s.store.UpdateExecution(exec); err != nil {
		s.log.Warn("persist queued-cancel", "exec_id", execID, "err", err)
	}
}

// execute performs the full lifecycle for an existing pending execution row.
// The caller (worker) owns ctx and the running/active bookkeeping.
func (s *Service) execute(ctx context.Context, execID int64) error {
	exec, err := s.store.GetExecution(execID)
	if err != nil {
		return err
	}
	t, err := s.store.GetTask(exec.TaskID)
	if err != nil {
		return s.finishWithError(exec, fmt.Errorf("load task: %w", err))
	}
	g, err := s.store.GetGame(t.GameID)
	if err != nil {
		return s.finishWithError(exec, fmt.Errorf("load game: %w", err))
	}
	adapter, err := s.reg.Get(g.Adapter)
	if err != nil {
		return s.finishWithError(exec, err)
	}
	if err := adapter.Validate(g); err != nil {
		return s.finishWithError(exec, err)
	}
	spec, err := adapter.BuildCommand(g, t)
	if err != nil {
		return s.finishWithError(exec, fmt.Errorf("build command: %w", err))
	}

	exec.Command = spec.CommandLine()
	exec.Status = store.StatusRunning
	start := time.Now().UTC()
	exec.StartTime = &start
	if err := s.store.UpdateExecution(exec); err != nil {
		s.log.Warn("persist running status", "exec_id", execID, "err", err)
	}

	attempts := t.MaxRetries + 1
	var res runner.Result
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			exec.RetryCount = attempt
			_ = s.store.UpdateExecution(exec)
			if t.RetryDelaySec > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(time.Duration(t.RetryDelaySec) * time.Second):
				}
			}
		}
		s.log.Info("running task", "exec_id", execID, "task", t.Name, "attempt", attempt+1, "cmd", spec.CommandLine())
		res = runner.Run(ctx, spec)
		if res.Err == nil {
			break
		}
		// Don't retry a user cancellation.
		if ctx.Err() == context.Canceled {
			break
		}
	}

	end := res.EndTime.UTC()
	exec.EndTime = &end
	exec.Stdout = res.Stdout
	exec.Stderr = res.Stderr
	code := res.ExitCode
	exec.ExitCode = &code

	switch {
	case ctx.Err() == context.Canceled:
		exec.Status = store.StatusCancelled
		exec.ErrorMsg = "cancelled by operator"
	case res.Err != nil:
		exec.Status = store.StatusFailed
		exec.ErrorMsg = res.Err.Error()
		exec.ScreenshotPath = s.captureScreenshot(execID)
	default:
		exec.Status = store.StatusSuccess
	}

	if err := s.store.UpdateExecution(exec); err != nil {
		return err
	}
	s.log.Info("task finished", "exec_id", execID, "status", exec.Status, "exit", code, "retries", exec.RetryCount)
	return nil
}

// finishWithError records a pre-run setup failure on the execution.
func (s *Service) finishWithError(exec store.Execution, cause error) error {
	now := time.Now().UTC()
	if exec.StartTime == nil {
		exec.StartTime = &now
	}
	exec.EndTime = &now
	exec.Status = store.StatusFailed
	exec.ErrorMsg = cause.Error()
	exec.ScreenshotPath = s.captureScreenshot(exec.ID)
	s.log.Error("task setup failed", "exec_id", exec.ID, "err", cause)
	return s.store.UpdateExecution(exec)
}

// captureScreenshot computes the destination path and, if a screenshot command
// is configured, best-effort runs it. The path is always recorded so an
// operator knows where to look even if capture is disabled. This is purely an
// observability aid; it never touches the game.
func (s *Service) captureScreenshot(execID int64) string {
	path := filepath.Join(s.cfg.ScreenshotDir(),
		fmt.Sprintf("exec_%d_%s.png", execID, time.Now().Format("20060102_150405")))
	if s.cfg.ScreenshotCmd == "" {
		return path
	}
	rendered, err := renderTemplate(s.cfg.ScreenshotCmd, map[string]string{"Path": path})
	if err != nil {
		s.log.Warn("screenshot template", "err", err)
		return path
	}
	// The configured command is a full shell command line; run it via cmd /C
	// on Windows so quoting in the template behaves as the operator expects.
	cmd := exec.Command("cmd", "/C", rendered)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		s.log.Warn("screenshot capture failed", "exec_id", execID, "err", err, "stderr", stderr.String())
	}
	return path
}

func renderTemplate(tpl string, data any) (string, error) {
	t, err := template.New("s").Parse(tpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
