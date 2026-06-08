// Package wuwa adapts ok-wuthering-waves (ok-ww.exe) for Wuthering Waves.
//
// ok-ww runs a numbered task and exits: `ok-ww.exe -t N -e`. This adapter
// builds that command line. ok-ww performs the daily routine, background auto
// battle and echo farming itself; here it is only launched as a process.
//
// A RouteFarmTask is reserved for a future ok-ww feature — the "farm" task type
// is wired through now so it can be filled in without schema changes.
package wuwa

import (
	"fmt"
	"strings"

	"github.com/xiabee/game-scheduler/internal/game/cmdutil"
	"github.com/xiabee/game-scheduler/internal/runner"
	"github.com/xiabee/game-scheduler/internal/store"
)

// Key is the adapter identifier.
const Key = "wuwa"

// Adapter implements game.Adapter for ok-ww.exe.
type Adapter struct{}

// New returns a Wuthering Waves adapter.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) Key() string { return Key }

func (a *Adapter) TaskTypes() []string {
	return []string{"task", "farm", "raw"}
}

func (a *Adapter) Validate(g store.Game) error {
	if strings.TrimSpace(g.ToolPath) == "" {
		return fmt.Errorf("wuwa: tool_path (ok-ww.exe) must be set")
	}
	return nil
}

// BuildCommand maps a task to an ok-ww.exe invocation.
//
//   - "task": run task index N. params: {"task_index": N, "exit": true}
//     => ok-ww.exe -t N [-e]
//   - "farm": reserved RouteFarmTask. Until ok-ww exposes it, behaves like
//     "task" and additionally accepts {"route": "<name>"} passed as -r.
//   - "raw" : params.raw_args verbatim.
func (a *Adapter) BuildCommand(g store.Game, t store.Task) (runner.Spec, error) {
	params, err := t.ParamsMap()
	if err != nil {
		return runner.Spec{}, fmt.Errorf("wuwa: bad params: %w", err)
	}

	if raw, ok := cmdutil.RawArgs(params); ok {
		return cmdutil.BaseSpec(g, t, params, raw), nil
	}

	switch t.Type {
	case "task", "farm":
		idx := cmdutil.IntStr(params, "task_index")
		if idx == "" {
			return runner.Spec{}, fmt.Errorf("wuwa: %s requires params.task_index", t.Type)
		}
		args := []string{"-t", idx}
		if t.Type == "farm" {
			if route := cmdutil.Str(params, "route"); route != "" {
				args = append(args, "-r", route)
			}
		}
		if cmdutil.Bool(params, "exit", true) {
			args = append(args, "-e")
		}
		return cmdutil.BaseSpec(g, t, params, args), nil
	case "raw":
		return runner.Spec{}, fmt.Errorf("wuwa: type 'raw' requires params.raw_args")
	default:
		return runner.Spec{}, fmt.Errorf("wuwa: unknown task type %q", t.Type)
	}
}
