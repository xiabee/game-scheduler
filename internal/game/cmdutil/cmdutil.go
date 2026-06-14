// Package cmdutil holds helpers shared by game adapters for turning task
// params into command-line arguments. It deliberately knows nothing about any
// specific game.
package cmdutil

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/xiabee/game-scheduler/internal/runner"
	"github.com/xiabee/game-scheduler/internal/store"
)

// Str returns params[key] as a string, or "" if absent/not a string.
func Str(params map[string]any, key string) string {
	if v, ok := params[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Bool returns params[key] as a bool (default def). Accepts bool or the
// strings "true"/"false".
func Bool(params map[string]any, key string, def bool) bool {
	v, ok := params[key]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1"
	}
	return def
}

// IntStr returns params[key] rendered as an integer string (JSON numbers decode
// to float64). Empty string if absent.
func IntStr(params map[string]any, key string) string {
	switch v := params[key].(type) {
	case float64:
		return fmt.Sprintf("%d", int64(v))
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case string:
		return v
	}
	return ""
}

// RawArgs returns params["raw_args"] as a []string if present. This lets an
// operator bypass adapter defaults entirely and specify the exact CLI for a
// given tool version.
func RawArgs(params map[string]any) ([]string, bool) {
	v, ok := params["raw_args"]
	if !ok {
		return nil, false
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		out = append(out, fmt.Sprint(item))
	}
	return out, true
}

// Exe returns the executable to run: params["exe"] override if set, otherwise
// the game's configured ToolPath.
func Exe(g store.Game, params map[string]any) string {
	if e := Str(params, "exe"); e != "" {
		return e
	}
	return g.ToolPath
}

// Dir returns params["working_dir"] override if set, otherwise the game's
// configured WorkingDir.
func Dir(g store.Game, params map[string]any) string {
	if d := Str(params, "working_dir"); d != "" {
		return d
	}
	return g.WorkingDir
}

// Timeout maps a task's TimeoutSec to a duration (0 => no timeout).
func Timeout(t store.Task) time.Duration {
	if t.TimeoutSec <= 0 {
		return 0
	}
	return time.Duration(t.TimeoutSec) * time.Second
}

// BaseSpec builds a Spec honouring exe/working_dir overrides and the task
// timeout, with the given args. When no working dir is configured and the
// executable is an absolute path, the working dir defaults to the executable's
// own folder — GUI tools like BetterGI / ok-ww resolve their resources and
// config relative to the install directory and fail (e.g. BetterGI exit 553)
// when launched with the scheduler's working directory instead.
func BaseSpec(g store.Game, t store.Task, params map[string]any, args []string) runner.Spec {
	exe := Exe(g, params)
	dir := Dir(g, params)
	if dir == "" && filepath.IsAbs(exe) {
		dir = filepath.Dir(exe)
	}
	return runner.Spec{
		Path:    exe,
		Args:    args,
		Dir:     dir,
		Timeout: Timeout(t),
	}
}
