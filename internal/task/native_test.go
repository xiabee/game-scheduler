package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/events"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/runner"
	"github.com/xiabee/game-scheduler/internal/store"
)

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// buildFakeController compiles the protocol-faithful fake controller once
// per test: a real child process over real pipes, no game needed.
func buildFakeController(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; cannot build fake-controller")
	}
	out := filepath.Join(t.TempDir(), "fake-controller.exe")
	cmd := exec.Command("go", "build", "-o", out,
		"github.com/xiabee/game-scheduler/cmd/fake-controller")
	cmd.Env = append(os.Environ(), "GOFLAGS=-p=2", "GOMAXPROCS=2")
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake-controller: %v\n%s", err, outBytes)
	}
	return out
}

func newNativeSvc(t *testing.T, cfg config.Config) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	reg := game.NewRegistry(stubAdapter{})
	svc := NewService(st, reg, cfg, events.New(), nil)
	t.Cleanup(func() {
		ctx, cancel := contextWithTimeout(5 * time.Second)
		defer cancel()
		svc.Shutdown(ctx)
		st.Close()
	})
	return svc, st
}

func nativeTask(t *testing.T, st *store.Store, params string) store.Task {
	t.Helper()
	if _, err := st.GetGame("stub"); err != nil {
		if _, err := st.CreateGame(store.Game{ID: "stub", Name: "stub", Adapter: "stub",
			ToolPath: os.Args[0], Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	tk, err := st.CreateTask(store.Task{GameID: "stub", Name: "native-run", Type: "native",
		Params: params, TimeoutSec: 30, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// The full execute() path with a real child speaking the real protocol:
// params contract -> session -> RESULT done -> Execution success.
func TestNativeTaskHappyPath(t *testing.T) {
	fake := buildFakeController(t)
	dir := t.TempDir()
	skill := filepath.Join(dir, "skill.json")
	if err := os.WriteFile(skill, []byte(`{"name":"fake"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, st := newNativeSvc(t, config.Config{MaxConcurrent: 1, DataDir: dir,
		NativeControllerPath: fake})
	tk := nativeTask(t, st, fmt.Sprintf(`{"executor":"native","skill":%q,"duration_sec":1}`, skill))

	e, skipped, err := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	if err != nil || skipped {
		t.Fatalf("enqueue: %v skipped=%v", err, skipped)
	}
	waitStatus(t, st, e.ID, store.StatusSuccess, 15*time.Second)

	got, err := st.GetExecution(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", got.ExitCode)
	}
	// The dispatch must have pointed the controller at the per-execution
	// session log (the fake ignores args, so assert the command line).
	if !strings.Contains(got.Command, "exec-") || !strings.Contains(got.Command, "--session-log") {
		t.Fatalf("command must carry the session log path: %q", got.Command)
	}
	// The EVENT trail must be surfaced in the execution's stdout field
	// (fake emits two semantic events before RESULT).
	if !strings.Contains(got.Stdout, "state=step_01") {
		t.Fatalf("stdout trail missing events: %q", got.Stdout)
	}
}

func TestNativeTaskFailsFastWithoutControllerConfig(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "skill.json")
	_ = os.WriteFile(skill, []byte(`{}`), 0o644)
	svc, st := newNativeSvc(t, config.Config{MaxConcurrent: 1, DataDir: dir})
	tk := nativeTask(t, st, fmt.Sprintf(`{"executor":"native","skill":%q}`, skill))

	e, _, err := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitStatus(t, st, e.ID, store.StatusFailed, 3*time.Second)
	got, _ := st.GetExecution(e.ID)
	if !strings.Contains(got.ErrorMsg, "native_controller_path") {
		t.Fatalf("err = %q, want native_controller_path hint", got.ErrorMsg)
	}
}

func TestNativeTaskPreflightChecksDeclaredFiles(t *testing.T) {
	fake := buildFakeController(t)
	dir := t.TempDir()
	skill := filepath.Join(dir, "skill.json")
	_ = os.WriteFile(skill, []byte(`{}`), 0o644)
	missing := filepath.Join(dir, "nope.json")
	svc, st := newNativeSvc(t, config.Config{MaxConcurrent: 1, DataDir: dir,
		NativeControllerPath: fake})
	tk := nativeTask(t, st, fmt.Sprintf(`{"executor":"native","skill":%q,"probes":%q}`, skill, missing))

	pf, err := svc.Preflight(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pf.Ready {
		t.Fatalf("preflight must not be ready with a missing probes file: %+v", pf)
	}
	if !strings.Contains(pf.ValidationError, "nope.json") &&
		len(pf.Missing) == 0 {
		t.Fatalf("preflight must report the missing probes file: %+v", pf)
	}
}

// ---- auto executor (NC6): resolve native vs external per execution ---------

// autoTask is nativeTask with an explicit task type: auto tasks may keep an
// adapter-owned type (the external fallback branch builds the adapter
// command from it), which is exactly the contract under test here.
func autoTask(t *testing.T, st *store.Store, taskType, params string) store.Task {
	t.Helper()
	if _, err := st.GetGame("stub"); err != nil {
		if _, err := st.CreateGame(store.Game{ID: "stub", Name: "stub", Adapter: "stub",
			ToolPath: os.Args[0], Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	tk, err := st.CreateTask(store.Task{GameID: "stub", Name: "auto-run", Type: taskType,
		Params: params, TimeoutSec: 30, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestAutoExecutorPrefersNativeWhenViable(t *testing.T) {
	fake := buildFakeController(t)
	dir := t.TempDir()
	skill := filepath.Join(dir, "skill.json")
	if err := os.WriteFile(skill, []byte(`{"name":"fake"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, st := newNativeSvc(t, config.Config{MaxConcurrent: 1, DataDir: dir,
		NativeControllerPath: fake})
	tk := autoTask(t, st, "ok", fmt.Sprintf(`{"executor":"auto","skill":%q,"duration_sec":1}`, skill))

	e, skipped, err := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	if err != nil || skipped {
		t.Fatalf("enqueue: %v skipped=%v", err, skipped)
	}
	waitStatus(t, st, e.ID, store.StatusSuccess, 15*time.Second)

	got, _ := st.GetExecution(e.ID)
	if got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", got.ExitCode)
	}
	if !strings.Contains(got.Command, "fake-controller") {
		t.Fatalf("auto must run the controller when viable: %q", got.Command)
	}
	if !strings.Contains(got.Stdout, "executor=auto resolved=native") {
		t.Fatalf("execution trail must announce the auto resolution: %q", got.Stdout)
	}
}

func TestAutoExecutorFallsBackToExternal(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "skill.json")
	if err := os.WriteFile(skill, []byte(`{"name":"fake"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// No controller configured: auto must fall back to the adapter path and
	// still complete the task successfully (stub "ok" exits 0).
	svc, st := newNativeSvc(t, config.Config{MaxConcurrent: 1, DataDir: dir})
	tk := autoTask(t, st, "ok", fmt.Sprintf(`{"executor":"auto","skill":%q}`, skill))

	e, _, err := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitStatus(t, st, e.ID, store.StatusSuccess, 15*time.Second)

	got, _ := st.GetExecution(e.ID)
	if !strings.Contains(got.Command, "TestTaskHelper") {
		t.Fatalf("fallback must run the adapter command, got: %q", got.Command)
	}
	if strings.Contains(got.Command, "--session-log") {
		t.Fatalf("fallback must not carry native session args: %q", got.Command)
	}
}

func TestAutoExecutorFallsBackWhenDeclaredAssetMissing(t *testing.T) {
	fake := buildFakeController(t)
	dir := t.TempDir()
	// Config and controller exist, but the declared skill file does not:
	// auto degrades to external instead of running the controller without
	// its declared asset.
	svc, st := newNativeSvc(t, config.Config{MaxConcurrent: 1, DataDir: dir,
		NativeControllerPath: fake})
	tk := autoTask(t, st, "ok", `{"executor":"auto","skill":"Z:/definitely/missing/skill.json"}`)

	e, _, err := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitStatus(t, st, e.ID, store.StatusSuccess, 15*time.Second)

	got, _ := st.GetExecution(e.ID)
	if !strings.Contains(got.Command, "TestTaskHelper") {
		t.Fatalf("missing asset must resolve external, got: %q", got.Command)
	}
}

func TestAutoPreflightReportsResolution(t *testing.T) {
	fake := buildFakeController(t)
	dir := t.TempDir()
	skill := filepath.Join(dir, "skill.json")
	if err := os.WriteFile(skill, []byte(`{"name":"fake"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// viable → native branch, ready, resolution announced
	svc, st := newNativeSvc(t, config.Config{MaxConcurrent: 1, DataDir: dir,
		NativeControllerPath: fake})
	tk := autoTask(t, st, "ok", fmt.Sprintf(`{"executor":"auto","skill":%q}`, skill))
	pf, err := svc.Preflight(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !pf.Ready || pf.Resolution != "auto→native" {
		t.Fatalf("viable auto must preflight native+ready, got ready=%v resolution=%q", pf.Ready, pf.Resolution)
	}

	// no controller config → external branch with the reason
	svc2, st2 := newNativeSvc(t, config.Config{MaxConcurrent: 1, DataDir: dir})
	tk2 := autoTask(t, st2, "ok", fmt.Sprintf(`{"executor":"auto","skill":%q}`, skill))
	pf2, err := svc2.Preflight(tk2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !pf2.Ready || !strings.Contains(pf2.Resolution, "auto→external") ||
		!strings.Contains(pf2.Resolution, "native_controller_path") {
		t.Fatalf("unconfigured auto must resolve external with reason, got ready=%v resolution=%q",
			pf2.Ready, pf2.Resolution)
	}
	if len(pf2.Missing) != 0 {
		t.Fatalf("resolved-external branch must carry the EXTERNAL checks only: %+v", pf2.Missing)
	}

	// declared asset missing → external with that reason (controller IS
	// configured here, so the asset is what tips the resolution)
	tk3 := autoTask(t, st, "ok", `{"executor":"auto","skill":"Z:/definitely/missing/skill.json"}`)
	pf3, err := svc.Preflight(tk3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pf3.Resolution, "skill file not found") {
		t.Fatalf("missing asset reason must surface: %q", pf3.Resolution)
	}

	// params JSON that does not parse carries NO executor selector — the
	// task stays on the external path (legacy contract for garbage params;
	// the same dispatch rule protected plain external tasks before auto
	// existed).
	tk4 := autoTask(t, st2, "ok", `{"executor":"auto","skill":`)
	pf4, err := svc2.Preflight(tk4.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pf4.Resolution != "" || len(pf4.Checks) == 0 {
		t.Fatalf("garbage params must fall to the plain external view: %+v", pf4)
	}
}

// ---- buildNativeSession unit tests -----------------------------------------

func paramsFor(t *testing.T, raw string) nativeParams {
	t.Helper()
	tk := store.Task{Params: raw}
	p, err := decodeNativeParams(tk)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return p
}

func TestBuildNativeSessionDefaultsAndFlags(t *testing.T) {
	cfg := config.Config{NativeControllerPath: `C:\x\controller.exe`, NativeAllowInput: true}
	p := paramsFor(t, `{"executor":"native"}`)
	s, err := buildNativeSession(cfg, 7, store.Task{}, p)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(s.Args, " ")
	for _, want := range []string{"--dry-run", "--window", "@probe", "--backend", "auto"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %v", want, s.Args)
		}
	}
	if !strings.Contains(joined, "exec-7.tsv") {
		t.Fatalf("session log must carry the exec id: %v", s.Args)
	}
	if strings.Contains(joined, "--allow-input") {
		t.Fatalf("dry-run default must never pass --allow-input: %v", s.Args)
	}
}

func TestBuildNativeSessionAllowInputNeedsBothGates(t *testing.T) {
	cfg := config.Config{NativeControllerPath: `C:\x\controller.exe`}
	dry := false
	// config gate off, params ask for input: flag must NOT appear
	p := paramsFor(t, `{"executor":"native","dry_run":false,"allow_input":true}`)
	s, err := buildNativeSession(cfg, 1, store.Task{}, p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.Args, " ") == "" || strings.Contains(strings.Join(s.Args, " "), "--allow-input") {
		t.Fatalf("config gate must block --allow-input: %v", s.Args)
	}
	// both gates on: flag appears
	cfg.NativeAllowInput = true
	s, err = buildNativeSession(cfg, 1, store.Task{}, p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(s.Args, " "), "--allow-input") {
		t.Fatalf("both gates on must pass --allow-input: %v", s.Args)
	}
	_ = dry
	// dry_run left true (default) blocks input even with both gates on
	p2 := paramsFor(t, `{"executor":"native","allow_input":true}`)
	s, _ = buildNativeSession(cfg, 1, store.Task{}, p2)
	if strings.Contains(strings.Join(s.Args, " "), "--allow-input") {
		t.Fatalf("dry-run default must block --allow-input: %v", s.Args)
	}
}

func TestDecodeNativeParamsRejectsGarbage(t *testing.T) {
	if _, err := decodeNativeParams(store.Task{Params: `{not json`}); err == nil {
		t.Fatal("garbage params must fail decode")
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{"executor":"native"}`), &raw); err != nil {
		t.Fatal(err)
	}
}

// panicAdapter deterministically panics during command building, driving
// the worker's panic-recovery path (execution must land failed, never
// orphaned as "running").
type panicAdapter struct{}

func (panicAdapter) Key() string               { return "panic" }
func (panicAdapter) TaskTypes() []string       { return []string{"boom"} }
func (panicAdapter) Validate(store.Game) error { return nil }
func (panicAdapter) BuildCommand(store.Game, store.Task) (runner.Spec, error) {
	panic("boom: deterministic test panic")
}

func TestWorkerPanicLandsExecutionAsFailed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	reg := game.NewRegistry(panicAdapter{}, stubAdapter{})
	cfg := config.Config{MaxConcurrent: 1, DataDir: t.TempDir()}
	svc := NewService(st, reg, cfg, events.New(), nil)
	t.Cleanup(func() {
		ctx, cancel := contextWithTimeout(5 * time.Second)
		defer cancel()
		svc.Shutdown(ctx)
		st.Close()
	})

	if _, err := st.CreateGame(store.Game{ID: "pgame", Name: "p", Adapter: "panic",
		ToolPath: os.Args[0], Enabled: true}); err != nil {
		t.Fatal(err)
	}
	tk, err := st.CreateTask(store.Task{GameID: "pgame", Name: "boom", Type: "boom",
		TimeoutSec: 5, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// preflight itself also panics the adapter path — the API-layer
	// preflight does NOT recover; but the worker path must.
	e, _, err := svc.Enqueue(tk.ID, store.TriggerManual, nil, false)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitStatus(t, st, e.ID, store.StatusFailed, 5*time.Second)
	got, _ := st.GetExecution(e.ID)
	if !strings.Contains(got.ErrorMsg, "internal panic") {
		t.Fatalf("error msg = %q, want internal panic marker", got.ErrorMsg)
	}
}
