package genshin

import (
	"strings"
	"testing"

	"github.com/xiabee/game-scheduler/internal/store"
)

func TestBuildCommandOnedragonGroup(t *testing.T) {
	a := New()
	g := store.Game{ID: "genshin", Adapter: "genshin", ToolPath: "C:/tools/BetterGI/BetterGI.exe"}
	tk := store.Task{GameID: "genshin", Type: "onedragon", Params: `{"group": "daily"}`}

	spec, err := a.BuildCommand(g, tk)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "--startOneDragon") || !strings.Contains(joined, "--group daily") {
		t.Errorf("onedragon args missing: %q", joined)
	}
}

func TestBuildCommandConfigGroupRequiresGroup(t *testing.T) {
	a := New()
	g := store.Game{ID: "genshin", Adapter: "genshin", ToolPath: "BetterGI.exe"}

	if _, err := a.BuildCommand(g, store.Task{GameID: "genshin", Type: "config_group", Params: "{}"}); err == nil {
		t.Error("config_group without params.group must fail")
	}
	spec, err := a.BuildCommand(g, store.Task{GameID: "genshin", Type: "config_group", Params: `{"group": "g1"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(spec.Args, " "), "--startGroup g1") {
		t.Errorf("config_group args missing: %q", spec.Args)
	}
}

func TestBuildCommandRawArgsOverride(t *testing.T) {
	a := New()
	g := store.Game{ID: "genshin", Adapter: "genshin", ToolPath: "BetterGI.exe"}
	tk := store.Task{GameID: "genshin", Type: "onedragon", Params: `{"raw_args": ["--kiosk"], "group": "x"}`}

	spec, err := a.BuildCommand(g, tk)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, " ")
	if strings.Contains(joined, "--startOneDragon") {
		t.Errorf("raw_args must replace the type defaults: %q", joined)
	}
	if !strings.Contains(joined, "--kiosk") {
		t.Errorf("raw_args not used: %q", joined)
	}
}

func TestValidateRequiresToolPath(t *testing.T) {
	a := New()
	if err := a.Validate(store.Game{Adapter: "genshin"}); err == nil {
		t.Error("expected validation failure without tool_path")
	}
	if err := a.Validate(store.Game{Adapter: "genshin", ToolPath: "BetterGI.exe"}); err != nil {
		t.Errorf("tool_path set should validate: %v", err)
	}
}
