package store

import (
	"context"
	"testing"
	"time"
)

func mkFeedbackRec(t *testing.T, s *Store) FarmingRecommendation {
	t.Helper()
	mkGame(t, s, "genshin")
	ch, err := s.CreateCharacter(Character{GameID: "genshin", Name: "香菱"})
	if err != nil {
		t.Fatalf("create character: %v", err)
	}
	goal, err := s.CreateCharacterGoal(CharacterGoal{CharacterID: ch.ID, Name: "突破90"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	mat, err := s.CreateMaterialItem(MaterialItem{GameID: "genshin", Name: "绝云椒椒", Category: "collect"})
	if err != nil {
		t.Fatalf("create material: %v", err)
	}
	rec, err := s.CreateFarmingRecommendation(FarmingRecommendation{
		GoalID:        goal.ID,
		GameID:        "genshin",
		MaterialID:    mat.ID,
		Title:         "刷绝云椒椒",
		Reason:        "缺口 8",
		EstimatedRuns: 4,
	})
	if err != nil {
		t.Fatalf("create recommendation: %v", err)
	}
	return rec
}

func TestRecommendationFeedbackUnknownRec(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.RecommendationFeedback(424242); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRecommendationFeedbackNoTask(t *testing.T) {
	s := newTestStore(t)
	rec := mkFeedbackRec(t, s)

	fb, err := s.RecommendationFeedback(rec.ID)
	if err != nil {
		t.Fatalf("feedback: %v", err)
	}
	if fb.TaskID != nil || fb.TaskMissing || fb.TotalExecutions != 0 || fb.LastExecution != nil {
		t.Fatalf("no-task feedback=%+v", fb)
	}
	if fb.EstimatedRuns != 4 {
		t.Fatalf("estimated runs=%d", fb.EstimatedRuns)
	}
}

func TestRecommendationFeedbackAggregates(t *testing.T) {
	s := newTestStore(t)
	rec := mkFeedbackRec(t, s)

	task, err := s.CreateTask(Task{GameID: "genshin", Name: "rec task", Type: "script", Params: "{}", Enabled: true})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := s.SetFarmingRecommendationTask(rec.ID, task.ID); err != nil {
		t.Fatalf("link task: %v", err)
	}
	// Snapshot after the setup writes: the read-only claim below is about
	// RecommendationFeedback itself, not about the linking.
	rec, err = s.GetFarmingRecommendation(rec.ID)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}

	end := time.Now().UTC()
	mk := func(status string, offset time.Duration) {
		t.Helper()
		st := end.Add(offset)
		en := st
		if status == StatusSuccess || status == StatusFailed {
			en = st.Add(time.Minute)
		}
		if _, err := s.CreateExecution(Execution{TaskID: task.ID, Trigger: TriggerManual, Status: status, StartTime: &st, EndTime: &en}); err != nil {
			t.Fatalf("create execution: %v", err)
		}
	}
	mk(StatusSuccess, -40*time.Minute)
	mk(StatusSuccess, -30*time.Minute)
	mk(StatusFailed, -20*time.Minute)
	mk(StatusCancelled, -10*time.Minute)
	mk(StatusRunning, 0)

	fb, err := s.RecommendationFeedback(rec.ID)
	if err != nil {
		t.Fatalf("feedback: %v", err)
	}
	if fb.TaskID == nil || *fb.TaskID != task.ID {
		t.Fatalf("task link=%+v", fb.TaskID)
	}
	if fb.TotalExecutions != 5 || fb.SuccessRuns != 2 || fb.FailedRuns != 1 || fb.CancelledRuns != 1 || fb.PendingOrRunning != 1 {
		t.Fatalf("counts=%+v", fb)
	}
	if fb.LastExecution == nil || fb.LastExecution.Status != StatusRunning {
		t.Fatalf("last execution=%+v", fb.LastExecution)
	}

	// The feedback path is read-only: the recommendation row keeps its status
	// and updated_at (and nothing writes to requirements at all).
	after, err := s.GetFarmingRecommendation(rec.ID)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if after.Status != "task_created" || !after.UpdatedAt.Equal(rec.UpdatedAt) {
		t.Fatalf("recommendation mutated by feedback: %+v -> %+v", rec, after)
	}
}

func TestRecommendationFeedbackDanglingTask(t *testing.T) {
	s := newTestStore(t)
	rec := mkFeedbackRec(t, s)

	task, err := s.CreateTask(Task{GameID: "genshin", Name: "doomed", Type: "script", Params: "{}", Enabled: true})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := s.SetFarmingRecommendationTask(rec.ID, task.ID); err != nil {
		t.Fatalf("link task: %v", err)
	}
	// The normal store paths cannot produce a dangling task_id (DeleteTask
	// nulls the link via ON DELETE SET NULL), so manufacture the legacy /
	// externally-edited-DB state on a rented connection with FK enforcement
	// off. The pool is MaxOpenConns(1): the connection must go back before
	// the next store call, or RecommendationFeedback would wait forever.
	func() {
		ctx := context.Background()
		conn, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatalf("conn: %v", err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatalf("pragma off: %v", err)
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, task.ID); err != nil {
			t.Fatalf("delete task row: %v", err)
		}
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
			t.Fatalf("pragma on: %v", err)
		}
	}()

	fb, err := s.RecommendationFeedback(rec.ID)
	if err != nil {
		t.Fatalf("feedback: %v", err)
	}
	if !fb.TaskMissing || fb.TotalExecutions != 0 || fb.LastExecution != nil {
		t.Fatalf("dangling feedback=%+v", fb)
	}
}

// CreateTaskForRecommendation must be safe against concurrent double-create:
// the loser of the race rolls back its insert (no orphan/duplicate task) and
// reports ErrRecommendationTaskExists, while the recommendation keeps
// pointing at the winner's task.
func TestCreateTaskForRecommendationRaceGuard(t *testing.T) {
	s := newTestStore(t)
	rec := mkFeedbackRec(t, s)

	winner, err := s.CreateTaskForRecommendation(rec.ID, Task{GameID: "genshin", Name: "winner", Type: "script", Params: "{}", Enabled: true})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := s.CreateTaskForRecommendation(rec.ID, Task{GameID: "genshin", Name: "loser", Type: "script", Params: "{}", Enabled: true}); err != ErrRecommendationTaskExists {
		t.Fatalf("want ErrRecommendationTaskExists, got %v", err)
	}

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("tasks count=%d, want 1 (loser must roll back)", n)
	}
	cur, err := s.GetFarmingRecommendation(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.TaskID == nil || *cur.TaskID != winner.ID {
		t.Fatalf("rec link=%+v, want winner %d", cur.TaskID, winner.ID)
	}

	// An unknown recommendation stays ErrNotFound (and creates nothing).
	if _, err := s.CreateTaskForRecommendation(424242, Task{GameID: "genshin", Name: "x", Type: "script", Params: "{}"}); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
