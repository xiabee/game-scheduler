package hsr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/game-scheduler/internal/store"
)

func TestBuildCommandJoinsRelativeEntry(t *testing.T) {
	a := New()
	g := store.Game{ID: "hsr", Adapter: "hsr", ExtraConfig: `{"march7th_dir": "C:/tools/March7thAssistant"}`}
	tk := store.Task{GameID: "hsr", Type: "march7th_daily", Params: "{}"}

	spec, err := a.BuildCommand(g, tk)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Args) == 0 {
		t.Fatal("no args built")
	}
	// A relative entry is resolved inside the tool dir (joined, not replaced).
	if !strings.EqualFold(spec.Args[0], filepath.Join("C:/tools/March7thAssistant", "main.py")) {
		t.Errorf("relative entry not joined with dir: %q", spec.Args[0])
	}
	if spec.Dir != "C:/tools/March7thAssistant" {
		t.Errorf("dir = %q", spec.Dir)
	}
}

// A preflight-ready absolute entry (see task/service.go addPythonEntryChecks)
// must reach the runner verbatim: joining it onto the tool dir used to produce
// `dir\C:\elsewhere\main.py`, which always failed to start.
func TestBuildCommandKeepsAbsoluteEntry(t *testing.T) {
	entry := filepath.Join(os.TempDir(), "hsr_entry_probe.py")
	if !filepath.IsAbs(entry) {
		t.Skip("temp dir is not absolute on this platform")
	}
	a := New()
	g := store.Game{ID: "hsr", Adapter: "hsr", ExtraConfig: `{"march7th_dir": "C:/tools/March7thAssistant", "march7th_entry": "` + jsonPath(entry) + `"}`}
	tk := store.Task{GameID: "hsr", Type: "march7th_daily", Params: "{}"}

	spec, err := a.BuildCommand(g, tk)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Args[0] != entry {
		t.Errorf("absolute entry was mangled: got %q, want %q", spec.Args[0], entry)
	}
	// The tool dir still provides the child's working directory.
	if spec.Dir != "C:/tools/March7thAssistant" {
		t.Errorf("dir = %q", spec.Dir)
	}
}

func TestBuildCommandFhoeRoute(t *testing.T) {
	a := New()
	g := store.Game{ID: "hsr", Adapter: "hsr", ExtraConfig: `{"fhoe_dir": "C:/tools/Fhoe-Rail"}`}
	tk := store.Task{GameID: "hsr", Type: "fhoe_route", Params: `{"route": "skiff-1"}`}

	spec, err := a.BuildCommand(g, tk)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "--route skiff-1") {
		t.Errorf("route arg missing: %q", joined)
	}
}

func TestValidateRequiresADir(t *testing.T) {
	a := New()
	if err := a.Validate(store.Game{Adapter: "hsr", ExtraConfig: `{}`}); err == nil {
		t.Error("expected validation failure without any tool dir")
	}
	if err := a.Validate(store.Game{Adapter: "hsr", ExtraConfig: `{"fhoe_dir": "C:/x"}`}); err != nil {
		t.Errorf("fhoe_dir alone should validate: %v", err)
	}
}

// jsonPath quotes a path for embedding in a JSON string literal.
func jsonPath(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(p)
}
