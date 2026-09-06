package cmdutil

import (
	"path/filepath"
	"testing"
	"time"

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

func TestRawArgsVariants(t *testing.T) {
	if _, ok := RawArgs(map[string]any{}); ok {
		t.Fatal("absent key should not be ok")
	}
	if _, ok := RawArgs(map[string]any{"raw_args": "x"}); ok {
		t.Fatal("non-array raw_args should be rejected")
	}
	args, ok := RawArgs(map[string]any{"raw_args": []any{"--a", 2, true}})
	if !ok || len(args) != 3 || args[0] != "--a" || args[1] != "2" || args[2] != "true" {
		t.Fatalf("args=%v ok=%v", args, ok)
	}
}

func TestExeAndDirOverrides(t *testing.T) {
	g := store.Game{ToolPath: "C:/tools/tool.exe", WorkingDir: "C:/tools"}
	params := map[string]any{}
	if Exe(g, params) != g.ToolPath || Dir(g, params) != g.WorkingDir {
		t.Fatal("defaults ignored")
	}
	params["exe"] = "D:/custom.exe"
	params["working_dir"] = "D:/data"
	if Exe(g, params) != "D:/custom.exe" || Dir(g, params) != "D:/data" {
		t.Fatal("overrides ignored")
	}
}

func TestTimeout(t *testing.T) {
	if Timeout(store.Task{TimeoutSec: 0}) != 0 {
		t.Fatal("0 must mean no timeout")
	}
	if Timeout(store.Task{TimeoutSec: -3}) != 0 {
		t.Fatal("negative must mean no timeout")
	}
	if Timeout(store.Task{TimeoutSec: 90}) != 90*time.Second {
		t.Fatal("wrong duration")
	}
}

// The exe-folder default: no working_dir configured + absolute executable
// path => working dir becomes the executable's folder (BetterGI 553 fix).
func TestBaseSpecDefaultsWorkingDirToExeFolder(t *testing.T) {
	g := store.Game{ToolPath: "C:/tools/BetterGI.exe"}
	spec := BaseSpec(g, store.Task{}, map[string]any{}, []string{"--x"})
	if spec.Dir != `C:\tools` && spec.Dir != "C:/tools" {
		t.Fatalf("dir=%q want the exe folder", spec.Dir)
	}
	// explicit overrides still win
	spec = BaseSpec(g, store.Task{TimeoutSec: 5}, map[string]any{"working_dir": "D:/wd", "exe": "E:/e.exe"}, nil)
	if spec.Dir != "D:/wd" || spec.Path != "E:/e.exe" || spec.Timeout != 5*time.Second {
		t.Fatalf("spec=%+v", spec)
	}
}
