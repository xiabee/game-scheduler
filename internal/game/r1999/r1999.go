// Package r1999 adapts M9A (a MaaFramework project) for Reverse: 1999.
//
// M9A ships MaaPiCli.exe, the MaaFramework "ProjectInterface" CLI. This adapter
// launches MaaPiCli; M9A itself performs 收荒原 / 每日心相 / 常规作战 / 活动刷取
// according to its interface.json and config. We only build the command line.
//
// MaaPiCli is often configured interactively the first time and then re-run
// non-interactively. The exact flags depend on your MaaPiCli/M9A version, so:
//   - set the executable via game.tool_path (MaaPiCli.exe),
//   - set the M9A working directory via game.working_dir (so it finds
//     resource/ and config/),
//   - pass any required flags via params.raw_args.
//
// A few convenience task types build common argument shapes; verify against
// your version and prefer params.raw_args when in doubt.
package r1999

import (
	"fmt"
	"strings"

	"github.com/xiabee/game-scheduler/internal/game/cmdutil"
	"github.com/xiabee/game-scheduler/internal/runner"
	"github.com/xiabee/game-scheduler/internal/store"
)

// Key is the adapter identifier.
const Key = "r1999"

// Adapter implements game.Adapter for M9A's MaaPiCli.
type Adapter struct{}

// New returns a Reverse: 1999 adapter.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) Key() string { return Key }

func (a *Adapter) TaskTypes() []string {
	return []string{"run", "config", "raw"}
}

func (a *Adapter) Validate(g store.Game) error {
	if strings.TrimSpace(g.ToolPath) == "" {
		return fmt.Errorf("r1999: tool_path (MaaPiCli.exe) must be set")
	}
	if strings.TrimSpace(g.WorkingDir) == "" {
		return fmt.Errorf("r1999: working_dir (M9A project dir) must be set so MaaPiCli finds resource/config")
	}
	return nil
}

// BuildCommand maps a task to a MaaPiCli invocation.
//
//   - "run"   : run MaaPiCli with the project's configured tasks. params may set
//     {"config": "<name>"} which is passed as `-c <name>`; otherwise no args.
//   - "config": run a named MaaPiCli config: {"config": "<name>"} => `-c <name>`.
//   - "raw"   : params.raw_args verbatim.
func (a *Adapter) BuildCommand(g store.Game, t store.Task) (runner.Spec, error) {
	params, err := t.ParamsMap()
	if err != nil {
		return runner.Spec{}, fmt.Errorf("r1999: bad params: %w", err)
	}

	if raw, ok := cmdutil.RawArgs(params); ok {
		return cmdutil.BaseSpec(g, t, params, raw), nil
	}

	switch t.Type {
	case "run":
		var args []string
		if cfg := cmdutil.Str(params, "config"); cfg != "" {
			args = []string{"-c", cfg}
		}
		return cmdutil.BaseSpec(g, t, params, args), nil
	case "config":
		cfg := cmdutil.Str(params, "config")
		if cfg == "" {
			return runner.Spec{}, fmt.Errorf("r1999: config requires params.config")
		}
		return cmdutil.BaseSpec(g, t, params, []string{"-c", cfg}), nil
	case "raw":
		return runner.Spec{}, fmt.Errorf("r1999: type 'raw' requires params.raw_args")
	default:
		return runner.Spec{}, fmt.Errorf("r1999: unknown task type %q", t.Type)
	}
}
