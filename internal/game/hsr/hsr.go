// Package hsr adapts March7thAssistant and Fhoe-Rail for Honkai: Star Rail.
//
// Both tools are Python projects, so this adapter builds a `python <script>`
// invocation. Configure them via the game's extra_config JSON:
//
//	{
//	  "python_path": "python",               // python interpreter (default "python")
//	  "march7th_dir": "C:/.../March7thAssistant",
//	  "march7th_entry": "main.py",           // default "main.py"
//	  "fhoe_dir": "C:/.../Fhoe-Rail",
//	  "fhoe_entry": "main.py"                // default "main.py"
//	}
//
// March7thAssistant handles 锄大地 dailies / combat; Fhoe-Rail runs pre-recorded
// routes and picks up loot. This adapter only launches them as processes.
//
// CLI specifics vary by version; override per task with params.raw_args.
package hsr

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xiabee/game-scheduler/internal/game/cmdutil"
	"github.com/xiabee/game-scheduler/internal/runner"
	"github.com/xiabee/game-scheduler/internal/store"
)

// Key is the adapter identifier.
const Key = "hsr"

// Adapter implements game.Adapter.
type Adapter struct{}

// New returns an HSR adapter.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) Key() string { return Key }

func (a *Adapter) TaskTypes() []string {
	return []string{"march7th_daily", "fhoe_route", "raw"}
}

func (a *Adapter) Validate(g store.Game) error {
	ec, err := g.ExtraConfigMap()
	if err != nil {
		return fmt.Errorf("hsr: bad extra_config: %w", err)
	}
	if str(ec, "march7th_dir") == "" && str(ec, "fhoe_dir") == "" {
		return fmt.Errorf("hsr: extra_config must set at least one of march7th_dir / fhoe_dir")
	}
	return nil
}

// BuildCommand maps a task to a python invocation.
//
//   - "march7th_daily": run March7thAssistant entry. params optional.
//   - "fhoe_route"    : run Fhoe-Rail entry. params: {"route": "<file or name>"}.
//   - "raw"           : params.raw_args verbatim (exe defaults to python_path).
func (a *Adapter) BuildCommand(g store.Game, t store.Task) (runner.Spec, error) {
	params, err := t.ParamsMap()
	if err != nil {
		return runner.Spec{}, fmt.Errorf("hsr: bad params: %w", err)
	}
	ec, err := g.ExtraConfigMap()
	if err != nil {
		return runner.Spec{}, fmt.Errorf("hsr: bad extra_config: %w", err)
	}

	python := str(ec, "python_path")
	if python == "" {
		python = "python"
	}

	// Allow params.exe to override the interpreter; else use python_path.
	exe := cmdutil.Str(params, "exe")
	if exe == "" {
		exe = python
	}

	if raw, ok := cmdutil.RawArgs(params); ok {
		dir := cmdutil.Dir(g, params)
		return runner.Spec{Path: exe, Args: raw, Dir: dir, Timeout: cmdutil.Timeout(t)}, nil
	}

	var entry, dir string
	var extra []string
	switch t.Type {
	case "march7th_daily":
		dir = str(ec, "march7th_dir")
		if dir == "" {
			return runner.Spec{}, fmt.Errorf("hsr: march7th_dir not set in extra_config")
		}
		entry = strOr(ec, "march7th_entry", "main.py")
	case "fhoe_route":
		dir = str(ec, "fhoe_dir")
		if dir == "" {
			return runner.Spec{}, fmt.Errorf("hsr: fhoe_dir not set in extra_config")
		}
		entry = strOr(ec, "fhoe_entry", "main.py")
		if route := cmdutil.Str(params, "route"); route != "" {
			extra = []string{"--route", route}
		}
	case "raw":
		return runner.Spec{}, fmt.Errorf("hsr: type 'raw' requires params.raw_args")
	default:
		return runner.Spec{}, fmt.Errorf("hsr: unknown task type %q", t.Type)
	}

	// Override working dir if the task asks; otherwise run inside the project.
	if d := cmdutil.Str(params, "working_dir"); d != "" {
		dir = d
	}
	args := append([]string{filepath.Join(dir, entry)}, extra...)
	return runner.Spec{Path: exe, Args: args, Dir: dir, Timeout: cmdutil.Timeout(t)}, nil
}

func str(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func strOr(m map[string]any, k, def string) string {
	if v := str(m, k); v != "" {
		return v
	}
	return def
}
