package r1999

import (
	"strings"
	"testing"

	"github.com/xiabee/game-scheduler/internal/store"
)

func TestValidateRequiresToolPathAndWorkingDir(t *testing.T) {
	a := New()
	if err := a.Validate(store.Game{Adapter: "r1999", ToolPath: "MaaPiCli.exe"}); err == nil {
		t.Error("missing working_dir must fail: MaaPiCli needs the M9A resource dir")
	}
	g := store.Game{Adapter: "r1999", ToolPath: "MaaPiCli.exe", WorkingDir: "C:/tools/M9A"}
	if err := a.Validate(g); err != nil {
		t.Errorf("complete config should validate: %v", err)
	}
}

func TestBuildCommandRunWithAndWithoutConfig(t *testing.T) {
	a := New()
	g := store.Game{ID: "r1999", Adapter: "r1999", ToolPath: "MaaPiCli.exe", WorkingDir: "C:/tools/M9A"}

	spec, err := a.BuildCommand(g, store.Task{GameID: "r1999", Type: "run", Params: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Args) != 0 {
		t.Errorf("run without config builds no args, got %q", spec.Args)
	}

	spec, err = a.BuildCommand(g, store.Task{GameID: "r1999", Type: "run", Params: `{"config": ".daily"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(spec.Args, " "), "-c .daily") {
		t.Errorf("config arg missing: %q", spec.Args)
	}
}

func TestBuildCommandConfigRequiresName(t *testing.T) {
	a := New()
	g := store.Game{ID: "r1999", Adapter: "r1999", ToolPath: "MaaPiCli.exe", WorkingDir: "C:/tools/M9A"}
	if _, err := a.BuildCommand(g, store.Task{GameID: "r1999", Type: "config", Params: "{}"}); err == nil {
		t.Error("config without params.config must fail")
	}
}
