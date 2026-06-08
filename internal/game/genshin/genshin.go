// Package genshin adapts BetterGI (+ bettergi-scripts-list) for Genshin Impact.
//
// BetterGI is driven as an external executable. This adapter only constructs
// its command line — map tracing, auto-pickup, gathering, mining and "锄地"
// are all performed by BetterGI itself.
//
// IMPORTANT: BetterGI's command-line flags differ between versions. The
// defaults below reflect the common "调度器/一条龙" startup switches, but you
// should verify them against your installed version. Any task can override the
// arguments completely via params {"raw_args": ["...", "..."]} (and the
// executable via {"exe": "..."}).
package genshin

import (
	"fmt"
	"strings"

	"github.com/xiabee/game-scheduler/internal/game/cmdutil"
	"github.com/xiabee/game-scheduler/internal/runner"
	"github.com/xiabee/game-scheduler/internal/store"
)

// Key is the adapter identifier.
const Key = "genshin"

// Adapter implements game.Adapter for BetterGI.
type Adapter struct{}

// New returns a Genshin adapter.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) Key() string { return Key }

func (a *Adapter) TaskTypes() []string {
	return []string{"onedragon", "config_group", "script", "raw"}
}

func (a *Adapter) Validate(g store.Game) error {
	if strings.TrimSpace(g.ToolPath) == "" {
		return fmt.Errorf("genshin: tool_path (BetterGI.exe) must be set")
	}
	return nil
}

// BuildCommand maps a task to a BetterGI invocation.
//
// Supported task types and the params they read:
//   - "onedragon"    : run a configured 一条龙 group. params: {"group": "<name>"}
//   - "config_group" : run a 调度器 config group.   params: {"group": "<name>"}
//   - "script"       : run a JS pathing/script.      params: {"script": "<name or path>"}
//   - "raw"          : use params["raw_args"] verbatim.
func (a *Adapter) BuildCommand(g store.Game, t store.Task) (runner.Spec, error) {
	params, err := t.ParamsMap()
	if err != nil {
		return runner.Spec{}, fmt.Errorf("genshin: bad params: %w", err)
	}

	// Explicit override always wins.
	if raw, ok := cmdutil.RawArgs(params); ok {
		return cmdutil.BaseSpec(g, t, params, raw), nil
	}

	var args []string
	switch t.Type {
	case "onedragon":
		args = []string{"--startOneDragon"}
		if grp := cmdutil.Str(params, "group"); grp != "" {
			args = append(args, "--group", grp)
		}
	case "config_group":
		grp := cmdutil.Str(params, "group")
		if grp == "" {
			return runner.Spec{}, fmt.Errorf("genshin: config_group requires params.group")
		}
		args = []string{"--startGroup", grp}
	case "script":
		name := cmdutil.Str(params, "script")
		if name == "" {
			return runner.Spec{}, fmt.Errorf("genshin: script requires params.script")
		}
		args = []string{"--script", name}
	case "raw":
		return runner.Spec{}, fmt.Errorf("genshin: type 'raw' requires params.raw_args")
	default:
		return runner.Spec{}, fmt.Errorf("genshin: unknown task type %q", t.Type)
	}
	return cmdutil.BaseSpec(g, t, params, args), nil
}
