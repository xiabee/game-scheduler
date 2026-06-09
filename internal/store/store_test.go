package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mkGame(t *testing.T, s *Store, id string) Game {
	t.Helper()
	g, err := s.CreateGame(Game{ID: id, Name: id, Adapter: "genshin", Enabled: true})
	if err != nil {
		t.Fatalf("create game: %v", err)
	}
	return g
}

func TestGameCRUD(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "genshin")

	got, err := s.GetGame("genshin")
	if err != nil || got.Name != "genshin" {
		t.Fatalf("get: %v %+v", err, got)
	}
	got.Name = "原神"
	if _, err := s.UpdateGame(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	if g, _ := s.GetGame("genshin"); g.Name != "原神" {
		t.Errorf("update not persisted: %q", g.Name)
	}
	if _, err := s.GetGame("nope"); err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.DeleteGame("genshin"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteGame("genshin"); err != ErrNotFound {
		t.Errorf("want ErrNotFound on second delete, got %v", err)
	}
}

func TestListsReturnEmptyNotNil(t *testing.T) {
	s := newTestStore(t)
	if g, _ := s.ListGames(); g == nil {
		t.Error("ListGames should return non-nil slice")
	}
	if tk, _ := s.ListTasks(""); tk == nil {
		t.Error("ListTasks should return non-nil slice")
	}
	if p, _ := s.ListPlans(false); p == nil {
		t.Error("ListPlans should return non-nil slice")
	}
	if e, _ := s.ListExecutions(ExecutionFilter{}); e == nil {
		t.Error("ListExecutions should return non-nil slice")
	}
}

func TestCascadeDeleteGame(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "g")
	tk, _ := s.CreateTask(Task{GameID: "g", Name: "t", Type: "raw", Enabled: true})
	if err := s.DeleteGame("g"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTask(tk.ID); err != ErrNotFound {
		t.Errorf("task should cascade-delete, got %v", err)
	}
}

func TestExecutionFilterAndRecover(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "g")
	tk, _ := s.CreateTask(Task{GameID: "g", Name: "t", Type: "raw"})

	for _, st := range []string{StatusSuccess, StatusFailed, StatusRunning, StatusPending} {
		if _, err := s.CreateExecution(Execution{TaskID: tk.ID, Trigger: TriggerManual, Status: st}); err != nil {
			t.Fatal(err)
		}
	}

	failed, _ := s.ListExecutions(ExecutionFilter{Status: StatusFailed})
	if len(failed) != 1 {
		t.Errorf("want 1 failed, got %d", len(failed))
	}

	active, err := s.CountActiveByTask(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active != 2 { // running + pending
		t.Errorf("active=%d want 2", active)
	}

	n, err := s.RecoverOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("recovered=%d want 2", n)
	}
	if a, _ := s.CountActiveByTask(tk.ID); a != 0 {
		t.Errorf("active after recover=%d want 0", a)
	}
	all, _ := s.ListExecutions(ExecutionFilter{Status: StatusFailed})
	if len(all) != 3 { // original failed + 2 recovered
		t.Errorf("failed after recover=%d want 3", len(all))
	}
}

func TestPlanRunTimes(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "g")
	tk, _ := s.CreateTask(Task{GameID: "g", Name: "t", Type: "raw"})
	p, _ := s.CreatePlan(Plan{Name: "p", TaskID: tk.ID, CronExpr: "0 6 * * *", Enabled: true})
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.SetPlanRunTimes(p.ID, &now, &now); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetPlan(p.ID)
	if got.LastRunAt == nil || !got.LastRunAt.Equal(now) {
		t.Errorf("last run not persisted: %v", got.LastRunAt)
	}
}
