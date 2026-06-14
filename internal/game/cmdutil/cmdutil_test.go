package cmdutil

import (
	"path/filepath"
	"testing"

	"github.com/xiabee/game-scheduler/internal/store"
)

func TestBaseSpecDefaultsDirToExeFolder(t *testing.T) {
	exe := filepath.Join("D:\\", "Program Files", "BetterGI", "BetterGI.exe")
	g := store.Game{ToolPath: exe}
	// No working_dir configured: default to the executable's own folder so GUI
	// tools find their resources (regression: BetterGI exit 553).
	spec := BaseSpec(g, store.Task{}, map[string]any{}, []string{"--startOneDragon"})
	if want := filepath.Dir(exe); spec.Dir != want {
		t.Fatalf("Dir=%q want %q", spec.Dir, want)
	}

	// Explicit working_dir wins.
	g.WorkingDir = "C:\\custom"
	if spec := BaseSpec(g, store.Task{}, map[string]any{}, nil); spec.Dir != "C:\\custom" {
		t.Fatalf("explicit working_dir not honoured: %q", spec.Dir)
	}

	// params override wins over game.WorkingDir.
	if spec := BaseSpec(g, store.Task{}, map[string]any{"working_dir": "C:\\p"}, nil); spec.Dir != "C:\\p" {
		t.Fatalf("params working_dir not honoured: %q", spec.Dir)
	}

	// Non-absolute exe (PATH-resolvable) leaves Dir empty rather than ".".
	if spec := BaseSpec(store.Game{ToolPath: "ok-ww.exe"}, store.Task{}, map[string]any{}, nil); spec.Dir != "" {
		t.Fatalf("relative exe should not set a dir, got %q", spec.Dir)
	}
}
