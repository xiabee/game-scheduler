package cmdutil

import (
	"path/filepath"
	"testing"

	"github.com/xiabee/game-scheduler/internal/store"
)

func TestBaseSpecDefaultsDirToExeFolder(t *testing.T) {
	// t.TempDir() is absolute on every OS, so filepath.IsAbs is true here on both
	// Windows and Linux (the production path D:/... is absolute on Windows).
	dir := t.TempDir()
	exe := filepath.Join(dir, "BetterGI.exe")
	g := store.Game{ToolPath: exe}
	// No working_dir configured: default to the executable's own folder so GUI
	// tools find their resources (regression: BetterGI exit 553).
	spec := BaseSpec(g, store.Task{}, map[string]any{}, []string{"--startOneDragon"})
	if spec.Dir != dir {
		t.Fatalf("Dir=%q want %q", spec.Dir, dir)
	}

	// Explicit working_dir wins.
	g.WorkingDir = filepath.Join(dir, "custom")
	if spec := BaseSpec(g, store.Task{}, map[string]any{}, nil); spec.Dir != g.WorkingDir {
		t.Fatalf("explicit working_dir not honoured: %q", spec.Dir)
	}

	// params override wins over game.WorkingDir.
	pdir := filepath.Join(dir, "p")
	if spec := BaseSpec(g, store.Task{}, map[string]any{"working_dir": pdir}, nil); spec.Dir != pdir {
		t.Fatalf("params working_dir not honoured: %q", spec.Dir)
	}

	// Non-absolute exe (PATH-resolvable) leaves Dir empty rather than ".".
	if spec := BaseSpec(store.Game{ToolPath: "ok-ww.exe"}, store.Task{}, map[string]any{}, nil); spec.Dir != "" {
		t.Fatalf("relative exe should not set a dir, got %q", spec.Dir)
	}
}
