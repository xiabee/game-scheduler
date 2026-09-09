package wuwa

import (
	"strings"
	"testing"

	"github.com/xiabee/game-scheduler/internal/store"
)

func TestBuildCommandTaskWithExit(t *testing.T) {
	a := New()
	g := store.Game{ID: "wuwa", Adapter: "wuwa", ToolPath: "C:/tools/ok-ww/ok-ww.exe"}
	tk := store.Task{GameID: "wuwa", Type: "task", Params: `{"task_index": 2, "exit": true}`}

	spec, err := a.BuildCommand(g, tk)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "-t 2") || !strings.Contains(joined, "-e") {
		t.Errorf("task args missing: %q", joined)
	}
}

func TestBuildCommandFarmCarriesRoute(t *testing.T) {
	a := New()
	g := store.Game{ID: "wuwa", Adapter: "wuwa", ToolPath: "ok-ww.exe"}
	tk := store.Task{GameID: "wuwa", Type: "farm", Params: `{"task_index": 3, "route": "bell-1"}`}

	spec, err := a.BuildCommand(g, tk)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "-r bell-1") {
		t.Errorf("farm route arg missing: %q", joined)
	}
}

func TestBuildCommandTaskRequiresIndex(t *testing.T) {
	a := New()
	g := store.Game{ID: "wuwa", Adapter: "wuwa", ToolPath: "ok-ww.exe"}
	if _, err := a.BuildCommand(g, store.Task{GameID: "wuwa", Type: "task", Params: "{}"}); err == nil {
		t.Error("task without task_index must fail")
	}
}

func TestValidateRequiresToolPath(t *testing.T) {
	a := New()
	if err := a.Validate(store.Game{Adapter: "wuwa"}); err == nil {
		t.Error("expected validation failure without tool_path")
	}
}
