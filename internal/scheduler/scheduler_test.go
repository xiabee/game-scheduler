package scheduler

import (
	"path/filepath"
	"testing"

	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/events"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/game/genshin"
	"github.com/xiabee/game-scheduler/internal/store"
	"github.com/xiabee/game-scheduler/internal/task"
)

// newFixture builds a store + task service + scheduler wired to a temp dir.
func newFixture(t *testing.T) (*store.Store, *Scheduler) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "g", Adapter: "genshin", ToolPath: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	reg := game.NewRegistry(genshin.New())
	svc := task.NewService(st, reg, config.Config{DataDir: t.TempDir(), MaxConcurrent: 1}, events.New(), nil)
	return st, New(st, svc, nil)
}

// fireAndCount creates a plan for the task, invokes fire directly (bypassing
// the cron engine), and returns how many executions exist afterwards.
func fireAndCount(t *testing.T, s *Scheduler, st *store.Store, taskID int64) int {
	t.Helper()
	plan, err := st.CreatePlan(store.Plan{Name: "p", TaskID: taskID, CronExpr: "* * * * *", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s.fire(plan)
	execs, err := st.ListExecutions(store.ExecutionFilter{TaskID: taskID})
	if err != nil {
		t.Fatal(err)
	}
	return len(execs)
}

// A plan that stays enabled must not keep firing a task that was disabled:
// firing it would run something the operator explicitly turned off.
func TestFireSkipsDisabledTask(t *testing.T) {
	st, sched := newFixture(t)

	taskOn, err := st.CreateTask(store.Task{GameID: "genshin", Name: "on", Type: "onedragon", Params: "{}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	taskOff, err := st.CreateTask(store.Task{GameID: "genshin", Name: "off", Type: "onedragon", Params: "{}", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}

	if n := fireAndCount(t, sched, st, taskOff.ID); n != 0 {
		t.Fatalf("disabled task was fired: %d executions", n)
	}
	if n := fireAndCount(t, sched, st, taskOn.ID); n != 1 {
		t.Fatalf("enabled task not fired: %d executions", n)
	}

	// Disabling the task afterwards stops future fires.
	taskOn.Enabled = false
	if _, err := st.UpdateTask(taskOn); err != nil {
		t.Fatal(err)
	}
	if n := fireAndCount(t, sched, st, taskOn.ID); n != 1 {
		t.Fatalf("task fired after being disabled: %d executions", n)
	}
}

// Disabling the game also holds scheduled fires, same as disabling the task.
func TestFireSkipsDisabledGame(t *testing.T) {
	st, sched := newFixture(t)

	taskRow, err := st.CreateTask(store.Task{GameID: "genshin", Name: "t", Type: "onedragon", Params: "{}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := fireAndCount(t, sched, st, taskRow.ID); n != 1 {
		t.Fatalf("enabled task not fired: %d executions", n)
	}

	g, err := st.GetGame("genshin")
	if err != nil {
		t.Fatal(err)
	}
	g.Enabled = false
	if _, err := st.UpdateGame(g); err != nil {
		t.Fatal(err)
	}
	if n := fireAndCount(t, sched, st, taskRow.ID); n != 1 {
		t.Fatalf("task fired while its game was disabled: %d executions", n)
	}
}

func TestValidateCron(t *testing.T) {
	for _, expr := range []string{"0 9 * * *", "@daily", "*/5 * * * *"} {
		if err := ValidateCron(expr); err != nil {
			t.Errorf("ValidateCron(%q)=%v", expr, err)
		}
	}
	for _, expr := range []string{"nope", "61 * * * *", "* * *"} {
		if err := ValidateCron(expr); err == nil {
			t.Errorf("ValidateCron(%q) should fail", expr)
		}
	}
}

// Reload computes the next run time for enabled plans.
func TestReloadRecordsNextRun(t *testing.T) {
	st, sched := newFixture(t)

	taskRow, err := st.CreateTask(store.Task{GameID: "genshin", Name: "t", Type: "onedragon", Params: "{}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePlan(store.Plan{Name: "daily", TaskID: taskRow.ID, CronExpr: "0 9 * * *", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := sched.Reload(); err != nil {
		t.Fatal(err)
	}
	plans, err := st.ListPlans(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans=%d", len(plans))
	}
	if plans[0].NextRunAt == nil {
		t.Fatal("Reload did not record next run time")
	}
}
