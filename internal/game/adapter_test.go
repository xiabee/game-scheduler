package game_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/game/genshin"
	"github.com/xiabee/game-scheduler/internal/game/hsr"
	"github.com/xiabee/game-scheduler/internal/game/r1999"
	"github.com/xiabee/game-scheduler/internal/game/wuwa"
	"github.com/xiabee/game-scheduler/internal/store"
)

func reg() *game.Registry {
	return game.NewRegistry(genshin.New(), hsr.New(), wuwa.New(), r1999.New())
}

func TestRegistryGetAndMeta(t *testing.T) {
	r := reg()
	if _, err := r.Get("genshin"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get("nope"); err == nil {
		t.Fatal("expected error for unknown adapter")
	}
	keys := r.Keys()
	if len(keys) != 4 {
		t.Fatalf("keys=%v", keys)
	}
	for _, m := range r.Meta() {
		if len(m.TaskTypes) == 0 {
			t.Errorf("adapter %s exposes no task types", m.Key)
		}
	}
}

// TestSchemaMatchesAdapters guards that the UI schema's task types stay in
// 1:1 sync with what each adapter actually accepts in BuildCommand.
func TestSchemaMatchesAdapters(t *testing.T) {
	for _, a := range []game.Adapter{genshin.New(), hsr.New(), wuwa.New(), r1999.New()} {
		sch := game.Schema(a.Key())
		if sch == nil {
			t.Errorf("%s: no UI schema defined", a.Key())
			continue
		}
		want := map[string]bool{}
		for _, tt := range a.TaskTypes() {
			want[tt] = true
		}
		got := map[string]bool{}
		for _, tt := range sch {
			got[tt.Type] = true
			if !want[tt.Type] {
				t.Errorf("%s: schema type %q not accepted by adapter", a.Key(), tt.Type)
			}
			if tt.Label == "" {
				t.Errorf("%s/%s: schema type missing label", a.Key(), tt.Type)
			}
		}
		for tt := range want {
			if !got[tt] {
				t.Errorf("%s: adapter type %q missing from schema", a.Key(), tt)
			}
		}
	}
}

func TestGenshinBuildCommand(t *testing.T) {
	a := genshin.New()
	g := store.Game{ID: "genshin", Adapter: "genshin", ToolPath: "BetterGI.exe"}

	spec, err := a.BuildCommand(g, store.Task{Type: "config_group", Params: `{"group":"采集"}`})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Path != "BetterGI.exe" || strings.Join(spec.Args, " ") != "--startGroup 采集" {
		t.Errorf("got %s %v", spec.Path, spec.Args)
	}

	// config_group requires a group
	if _, err := a.BuildCommand(g, store.Task{Type: "config_group", Params: `{}`}); err == nil {
		t.Error("expected error without group")
	}
	if _, err := a.BuildCommand(g, store.Task{Type: "script", Params: `{}`}); err == nil {
		t.Error("expected error without script")
	}
	// unknown type
	if _, err := a.BuildCommand(g, store.Task{Type: "bogus"}); err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestRawArgsOverride(t *testing.T) {
	a := genshin.New()
	g := store.Game{Adapter: "genshin", ToolPath: "BetterGI.exe"}
	spec, err := a.BuildCommand(g, store.Task{Type: "config_group",
		Params: `{"exe":"other.exe","raw_args":["--foo","bar"]}`})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Path != "other.exe" || strings.Join(spec.Args, " ") != "--foo bar" {
		t.Errorf("raw_args/exe override failed: %s %v", spec.Path, spec.Args)
	}
}

func TestWuwaBuildCommand(t *testing.T) {
	a := wuwa.New()
	g := store.Game{Adapter: "wuwa", ToolPath: "ok-ww.exe"}
	spec, err := a.BuildCommand(g, store.Task{Type: "task", Params: `{"task_index":3}`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.Args, " ") != "-t 3 -e" {
		t.Errorf("args=%v want -t 3 -e", spec.Args)
	}
	// exit:false drops -e
	spec, _ = a.BuildCommand(g, store.Task{Type: "task", Params: `{"task_index":1,"exit":false}`})
	if strings.Join(spec.Args, " ") != "-t 1" {
		t.Errorf("args=%v want -t 1", spec.Args)
	}
	spec, err = a.BuildCommand(g, store.Task{Type: "farm", Params: `{"task_index":2,"route":"echo","exit":true}`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.Args, " ") != "-t 2 -r echo -e" {
		t.Errorf("args=%v want farm route args", spec.Args)
	}
	spec, err = a.BuildCommand(g, store.Task{Type: "raw", Params: `{"raw_args":["--dry-run"]}`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.Args, " ") != "--dry-run" {
		t.Errorf("raw args=%v", spec.Args)
	}
	if _, err := a.BuildCommand(g, store.Task{Type: "task", Params: `{}`}); err == nil {
		t.Error("expected error without task_index")
	}
	if _, err := a.BuildCommand(g, store.Task{Type: "bogus"}); err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestHSRBuildCommand(t *testing.T) {
	a := hsr.New()
	g := store.Game{Adapter: "hsr", ExtraConfig: `{"python_path":"py","march7th_dir":"C:/M7"}`}
	spec, err := a.BuildCommand(g, store.Task{Type: "march7th_daily", Params: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Path != "py" {
		t.Errorf("python path=%q", spec.Path)
	}
	if len(spec.Args) != 1 || !strings.Contains(spec.Args[0], "main.py") {
		t.Errorf("args=%v", spec.Args)
	}
	g.ExtraConfig = `{"python_path":"py","fhoe_dir":"C:/Fhoe","fhoe_entry":"run.py"}`
	spec, err = a.BuildCommand(g, store.Task{Type: "fhoe_route", Params: `{"route":"daily"}`})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Dir != "C:/Fhoe" || filepath.ToSlash(strings.Join(spec.Args, " ")) != filepath.ToSlash(filepath.Join("C:/Fhoe", "run.py"))+" --route daily" {
		t.Errorf("fhoe spec=%+v", spec)
	}
	spec, err = a.BuildCommand(g, store.Task{Type: "raw", Params: `{"exe":"Fhoe-Rail.exe","working_dir":"C:/Fhoe","raw_args":["--once"]}`})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Path != "Fhoe-Rail.exe" || spec.Dir != "C:/Fhoe" || strings.Join(spec.Args, " ") != "--once" {
		t.Errorf("raw spec=%+v", spec)
	}
	if _, err := a.BuildCommand(store.Game{Adapter: "hsr", ExtraConfig: `{"python_path":"py"}`}, store.Task{Type: "march7th_daily", Params: `{}`}); err == nil {
		t.Error("expected error without march7th_dir")
	}
	if _, err := a.BuildCommand(g, store.Task{Type: "bogus", Params: `{}`}); err == nil {
		t.Error("expected error for unknown type")
	}
	// validate requires a dir
	if err := a.Validate(store.Game{Adapter: "hsr", ExtraConfig: `{}`}); err == nil {
		t.Error("expected validation error without dirs")
	}
}

func TestR1999BuildCommand(t *testing.T) {
	a := r1999.New()
	g := store.Game{Adapter: "r1999", ToolPath: "MaaPiCli.exe", WorkingDir: "C:/M9A"}
	spec, err := a.BuildCommand(g, store.Task{Type: "run", Params: `{"config":"daily"}`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.Args, " ") != "-c daily" {
		t.Errorf("args=%v want -c daily", spec.Args)
	}
	spec, err = a.BuildCommand(g, store.Task{Type: "config", Params: `{"config":"daily"}`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.Args, " ") != "-c daily" {
		t.Errorf("args=%v want -c daily", spec.Args)
	}
	spec, err = a.BuildCommand(g, store.Task{Type: "raw", Params: `{"raw_args":["--foo"]}`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.Args, " ") != "--foo" {
		t.Errorf("raw args=%v", spec.Args)
	}
	if _, err := a.BuildCommand(g, store.Task{Type: "config", Params: `{}`}); err == nil {
		t.Error("expected error without config")
	}
	if _, err := a.BuildCommand(g, store.Task{Type: "bogus"}); err == nil {
		t.Error("expected error for unknown type")
	}
	if err := a.Validate(store.Game{Adapter: "r1999", ToolPath: "MaaPiCli.exe"}); err == nil {
		t.Error("expected error without working_dir")
	}
	if err := a.Validate(store.Game{Adapter: "r1999", ToolPath: "MaaPiCli.exe", WorkingDir: "C:/M9A"}); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}
