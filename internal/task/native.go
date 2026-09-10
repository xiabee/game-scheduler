// Native task support (ROADMAP NC6, draft decision D4): tasks whose params
// declare "executor":"native" run the Rust controller through the NC6
// session protocol instead of building an external-adapter command line.
// Params contract (JSON object):
//
//	{
//	  "executor": "native",
//	  "skill": "path/to/skill.json",     // optional, controller --skill
//	  "probes": "path/to/probes.json",   // optional, controller --probes
//	  "window": "@probe",                // optional, default @probe
//	  "dry_run": true,                   // optional, default true
//	  "allow_input": false,              // optional, needs config gate too
//	  "duration_sec": 30,                // optional dry-run budget
//	  "backend": "auto",                 // optional capture backend
//	  "model": "path/to/manifest.json"   // optional model manifest
//	}
//
// Safety: real input synthesis requires dry_run=false AND allow_input=true
// in params AND native_allow_input=true in config; anything less and the
// flag is simply never passed. The controller's SafetyGovernor remains the
// per-action authority regardless.
package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/native"
	"github.com/xiabee/game-scheduler/internal/store"
)

type nativeParams struct {
	Skill       string  `json:"skill"`
	Probes      string  `json:"probes"`
	Window      string  `json:"window"`
	Backend     string  `json:"backend"`
	Model       string  `json:"model"`
	DryRun      *bool   `json:"dry_run"`
	AllowInput  bool    `json:"allow_input"`
	DurationSec float64 `json:"duration_sec"`
}

func decodeNativeParams(t store.Task) (nativeParams, error) {
	var p nativeParams
	if err := json.Unmarshal([]byte(t.Params), &p); err != nil {
		return p, fmt.Errorf("native params: %w", err)
	}
	return p, nil
}

// validate checks the params contract against the filesystem. The config
// gate (controller path configured, allow-input enabled) is checked here
// too so Preflight and execute() report the same failures.
func validateNativeParams(cfg config.Config, t store.Task, p nativeParams) error {
	if p.Skill == "" && p.Probes == "" {
		return fmt.Errorf("native params need at least one of skill/probes")
	}
	if p.Skill != "" && !fileExists(p.Skill) {
		return fmt.Errorf("native skill file not found: %s", p.Skill)
	}
	if p.Probes != "" && !fileExists(p.Probes) {
		return fmt.Errorf("native probes file not found: %s", p.Probes)
	}
	if p.Model != "" && !fileExists(p.Model) {
		return fmt.Errorf("native model manifest not found: %s", p.Model)
	}
	if cfg.NativeControllerPath == "" {
		return fmt.Errorf("config native_controller_path is not set (native executor disabled)")
	}
	if !executableExists(cfg.NativeControllerPath) {
		return fmt.Errorf("native controller executable not found: %s", cfg.NativeControllerPath)
	}
	return nil
}

// buildNativeSession assembles the controller invocation for one execution.
// args mirror the operator CLI exactly — the protocol has no side channel.
func buildNativeSession(cfg config.Config, execID int64, t store.Task, p nativeParams) (native.SessionConfig, error) {
	if cfg.NativeControllerPath == "" {
		return native.SessionConfig{}, fmt.Errorf("config native_controller_path is not set (native executor disabled)")
	}
	dryRun := true
	if p.DryRun != nil {
		dryRun = *p.DryRun
	}
	args := []string{"--dry-run"}
	window := p.Window
	if window == "" {
		window = "@probe"
	}
	args = append(args, "--window", window)
	backend := p.Backend
	if backend == "" {
		backend = "auto"
	}
	args = append(args, "--backend", backend)
	if p.Skill != "" {
		args = append(args, "--skill", p.Skill)
	}
	if p.Probes != "" {
		args = append(args, "--probes", p.Probes)
	}
	if p.Model != "" {
		args = append(args, "--model-path", p.Model)
	}
	if p.DurationSec > 0 {
		args = append(args, "--duration", fmt.Sprintf("%.1f", p.DurationSec))
	}
	// Real input needs BOTH opt-ins; dry-run observation never sends input
	// anyway, so the flag only ever rides on a session the operator asked
	// for explicitly at both levels.
	allowInput := p.AllowInput && !dryRun && cfg.NativeAllowInput
	if allowInput {
		args = append(args, "--allow-input")
	}
	// Session TSV lands with the other execution logs; the controller
	// creates the file, we create the directory.
	logDir := filepath.Join(cfg.DataDir, "native")
	_ = os.MkdirAll(logDir, 0o755)
	args = append(args, "--session-log", filepath.Join(logDir, fmt.Sprintf("exec-%d.tsv", execID)))

	return native.SessionConfig{
		ControllerPath: cfg.NativeControllerPath,
		Args:           args,
		Timeout:        time.Duration(t.TimeoutSec) * time.Second,
	}, nil
}

// executeNative runs the whole lifecycle for a native-executor task,
// mirroring execute()'s execution-row bookkeeping. Retry policy: only
// process-level faults (SessionError with retries left) are retried;
// done/failed/stopped/cancelled/timeout are terminal verdicts.
func (s *Service) executeNative(ctx context.Context, exec store.Execution, execID int64, t store.Task) error {
	p, err := decodeNativeParams(t)
	if err != nil {
		return s.finishWithError(exec, err)
	}
	if err := validateNativeParams(s.cfg, t, p); err != nil {
		return s.finishWithError(exec, err)
	}
	session, err := buildNativeSession(s.cfg, execID, t, p)
	if err != nil {
		return s.finishWithError(exec, err)
	}

	exec.Command = session.CommandLine()
	exec.Status = store.StatusRunning
	start := time.Now().UTC()
	exec.StartTime = &start
	if err := s.store.UpdateExecution(exec); err != nil {
		s.log.Warn("persist running status", "exec_id", execID, "err", err)
	}
	s.bus.Notify()

	attempts := t.MaxRetries + 1
	var res native.SessionResult
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
		s.log.Info("running native task", "exec_id", execID, "task", t.Name,
			"attempt", attempt+1, "cmd", session.ControllerPath)
		res = native.RunSession(ctx, session, func(msg *native.Message) {
			if msg.Event != nil {
				s.log.Info("native event", "exec_id", execID,
					"cycle", msg.Event.Cycle, "state", msg.Event.State,
					"probes", msg.Event.ProbesFired)
			}
		})
		// A session that ended in-band (RESULT present) or via cancel is
		// final; only process faults justify another attempt.
		if res.Outcome != native.SessionError {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}

	end := time.Now().UTC()
	exec.EndTime = &end
	exec.ExitCode = &res.ExitCode

	switch res.Outcome {
	case native.SessionDone:
		exec.Status = store.StatusSuccess
	case native.SessionCancelled:
		exec.Status = store.StatusCancelled
		exec.ErrorMsg = "cancelled by operator"
	case native.SessionTimeout:
		exec.Status = store.StatusFailed
		exec.ErrorMsg = "native session timed out"
	case native.SessionStopped, native.SessionFailed:
		// governor stop / skill failure: business termination, not a fault
		exec.Status = store.StatusFailed
		exec.ErrorMsg = fmt.Sprintf("native session %s (state %s)", res.Outcome, res.State)
	default: // SessionError and anything unknown
		exec.Status = store.StatusFailed
		exec.ErrorMsg = res.Err.Error()
	}

	s.recordRouteRun(t, exec)
	if err := s.store.UpdateExecution(exec); err != nil {
		return err
	}
	s.bus.Notify()
	if exec.Status == store.StatusFailed {
		s.alert("task_failed", "任务失败:"+t.Name, exec.ErrorMsg)
	}
	s.log.Info("native task finished", "exec_id", execID, "status", exec.Status,
		"outcome", res.Outcome, "exit", res.ExitCode, "retries", exec.RetryCount)
	return nil
}

// nativePreflight builds the Preflight report for a native task: no
// adapter involved, the checks are controller path + declared files.
func (s *Service) nativePreflight(t store.Task) (Preflight, error) {
	g, err := s.store.GetGame(t.GameID)
	if err != nil {
		return Preflight{}, err
	}
	pf := Preflight{TaskID: t.ID, TaskName: t.Name, GameID: g.ID, Adapter: g.Adapter + " → native"}
	p, err := decodeNativeParams(t)
	if err != nil {
		pf.ValidationError = err.Error()
		return pf, nil
	}
	if err := validateNativeParams(s.cfg, t, p); err != nil {
		pf.ValidationError = err.Error()
	}
	pf.Command = fmt.Sprintf("%s --dry-run --window %s --skill %s",
		s.cfg.NativeControllerPath, p.Window, p.Skill)
	pf.Executable = s.cfg.NativeControllerPath
	pf.ExecutableExists = executableExists(s.cfg.NativeControllerPath)
	pf.addExecutableCheck("native_controller", s.cfg.NativeControllerPath)
	if p.Skill != "" {
		pf.addFileCheck("skill", p.Skill)
	}
	if p.Probes != "" {
		pf.addFileCheck("probes", p.Probes)
	}
	if p.Model != "" {
		pf.addFileCheck("model_manifest", p.Model)
	}
	pf.Ready = pf.ValidationError == "" && pf.BuildError == "" && len(pf.Missing) == 0
	return pf, nil
}

// isNativeTask reports whether the task params select the native executor.
func isNativeTask(t store.Task) bool {
	pm, err := t.ParamsMap()
	if err != nil {
		return false
	}
	return stringValue(pm["executor"]) == "native"
}
