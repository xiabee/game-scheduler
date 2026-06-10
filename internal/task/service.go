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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/events"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/runner"
	"github.com/xiabee/game-scheduler/internal/shellcmd"
	"github.com/xiabee/game-scheduler/internal/store"
)

// NotifyFunc is called to alert an operator about an event. event is a stable
// key (e.g. "task_failed"), title/message are human-readable.
type NotifyFunc func(event, title, message string)

// Service runs tasks and records executions.
type Service struct {
	store *store.Store
	reg   *game.Registry
	cfg   config.Config
	log   *slog.Logger
	bus   *events.Bus

	sem chan struct{} // bounded run slots; cap == cfg.MaxConcurrent

	notify NotifyFunc // optional operator alert hook

	mu      sync.Mutex
	running map[int64]context.CancelFunc // execID -> cancel
	active  map[int64]int                // taskID -> count of pending/running execs
}

// SetNotify installs an operator-alert hook, called when a task fails.
func (s *Service) SetNotify(fn NotifyFunc) { s.notify = fn }

func (s *Service) alert(event, title, message string) {
	if s.notify != nil {
		s.notify(event, title, message)
	}
}

// NewService constructs a task service. bus may be nil (no live notifications).
func NewService(s *store.Store, reg *game.Registry, cfg config.Config, bus *events.Bus, log *slog.Logger) *Service {
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
		bus:     bus,
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
	s.bus.Notify()
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
	s.bus.Notify()
}

// Preflight reports what a task would run and whether the environment is ready,
// without launching anything. It is the quickest way to confirm a game is wired
// up correctly after installing its tool.
type Preflight struct {
	TaskID           int64            `json:"task_id"`
	TaskName         string           `json:"task_name"`
	GameID           string           `json:"game_id"`
	Adapter          string           `json:"adapter"`
	Command          string           `json:"command"`
	Executable       string           `json:"executable"`
	ExecutableExists bool             `json:"executable_exists"`
	WorkingDir       string           `json:"working_dir"`
	WorkingDirExists bool             `json:"working_dir_exists"`
	Checks           []PreflightCheck `json:"checks"`
	Missing          []string         `json:"missing"`
	ValidationError  string           `json:"validation_error,omitempty"`
	BuildError       string           `json:"build_error,omitempty"`
	Ready            bool             `json:"ready"`
}

// PreflightCheck is one filesystem prerequisite checked before a task is run.
type PreflightCheck struct {
	Key    string `json:"key"`
	Kind   string `json:"kind"` // executable | directory | file
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// Preflight resolves the command for taskID and checks its prerequisites.
func (s *Service) Preflight(taskID int64) (Preflight, error) {
	t, err := s.store.GetTask(taskID)
	if err != nil {
		return Preflight{}, err
	}
	g, err := s.store.GetGame(t.GameID)
	if err != nil {
		return Preflight{}, err
	}
	pf := Preflight{TaskID: t.ID, TaskName: t.Name, GameID: g.ID, Adapter: g.Adapter}

	adapter, err := s.reg.Get(g.Adapter)
	if err != nil {
		pf.ValidationError = err.Error()
		return pf, nil
	}
	if verr := adapter.Validate(g); verr != nil {
		pf.ValidationError = verr.Error()
	}
	spec, berr := adapter.BuildCommand(g, t)
	if berr != nil {
		pf.BuildError = berr.Error()
		return pf, nil
	}
	pf.Command = spec.CommandLine()
	pf.Executable = spec.Path
	pf.ExecutableExists = executableExists(spec.Path)
	pf.WorkingDir = spec.Dir
	pf.WorkingDirExists = spec.Dir == "" || dirExists(spec.Dir)
	executableKey := "executable"
	if strings.TrimSpace(g.ToolPath) != "" && spec.Path == g.ToolPath {
		executableKey = "tool_path"
	}
	pf.addExecutableCheck(executableKey, spec.Path)
	if spec.Dir != "" {
		pf.addDirCheck("working_dir", spec.Dir)
	}
	pf.addExtraConfigDirChecks(g)
	pf.addPythonEntryChecks(g, t)
	pf.Ready = pf.ValidationError == "" && pf.BuildError == "" && len(pf.Missing) == 0
	return pf, nil
}

func (pf *Preflight) addExecutableCheck(key, path string) {
	ok := executableExists(path)
	pf.Checks = append(pf.Checks, PreflightCheck{Key: key, Kind: "executable", Path: path, Exists: ok})
	if !ok {
		pf.Missing = append(pf.Missing, fmt.Sprintf("%s executable not found: %s", key, path))
	}
}

func (pf *Preflight) addDirCheck(key, path string) {
	ok := dirExists(path)
	pf.Checks = append(pf.Checks, PreflightCheck{Key: key, Kind: "directory", Path: path, Exists: ok})
	if !ok {
		pf.Missing = append(pf.Missing, fmt.Sprintf("%s directory not found: %s", key, path))
	}
}

func (pf *Preflight) addFileCheck(key, path string) {
	ok := fileExists(path)
	pf.Checks = append(pf.Checks, PreflightCheck{Key: key, Kind: "file", Path: path, Exists: ok})
	if !ok {
		pf.Missing = append(pf.Missing, fmt.Sprintf("%s file not found: %s", key, path))
	}
}

func (pf *Preflight) addExtraConfigDirChecks(g store.Game) {
	ec, err := g.ExtraConfigMap()
	if err != nil {
		return
	}
	keys := make([]string, 0, len(ec))
	for k := range ec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.HasSuffix(k, "_dir") {
			continue
		}
		path, ok := ec[k].(string)
		if !ok || strings.TrimSpace(path) == "" {
			continue
		}
		pf.addDirCheck("extra_config."+k, strings.TrimSpace(path))
	}
}

func (pf *Preflight) addPythonEntryChecks(g store.Game, t store.Task) {
	if g.Adapter != "hsr" {
		return
	}
	ec, err := g.ExtraConfigMap()
	if err != nil {
		return
	}
	var dirKey, entryKey, defEntry string
	switch t.Type {
	case "march7th_daily":
		dirKey, entryKey, defEntry = "march7th_dir", "march7th_entry", "main.py"
	case "fhoe_route":
		dirKey, entryKey, defEntry = "fhoe_dir", "fhoe_entry", "main.py"
	default:
		return
	}
	dir := strings.TrimSpace(stringValue(ec[dirKey]))
	entry := strings.TrimSpace(stringValue(ec[entryKey]))
	if entry == "" {
		entry = defEntry
	}
	if dir == "" || entry == "" {
		return
	}
	path := entry
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, entry)
	}
	pf.addFileCheck("extra_config."+entryKey, path)
}

// executableExists is true if path is an existing file or resolvable on PATH.
func executableExists(path string) bool {
	if path == "" {
		return false
	}
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return true
	}
	if _, err := exec.LookPath(path); err == nil {
		return true
	}
	return false
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
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
	s.bus.Notify()

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
	s.recordRouteRun(t, exec)
	s.bus.Notify()
	if exec.Status == store.StatusFailed {
		s.alert("task_failed", "任务失败:"+t.Name, exec.ErrorMsg)
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
	s.alert("task_failed", fmt.Sprintf("任务启动失败 #%d", exec.TaskID), cause.Error())
	err := s.store.UpdateExecution(exec)
	if t, loadErr := s.store.GetTask(exec.TaskID); loadErr == nil {
		s.recordRouteRun(t, exec)
	}
	return err
}

func (s *Service) recordRouteRun(t store.Task, exec store.Execution) {
	if t.RouteID == nil || exec.EndTime == nil {
		return
	}
	success := exec.Status == store.StatusSuccess
	if err := s.store.RecordRouteRun(*t.RouteID, success, *exec.EndTime); err != nil {
		s.log.Warn("record route run", "task_id", t.ID, "route_id", *t.RouteID, "err", err)
	}
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
	// The configured command is a full shell command line; shellcmd.Command runs
	// it via the platform shell with correct quoting.
	cmd := shellcmd.Command(rendered)
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
