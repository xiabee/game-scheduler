package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/xiabee/game-scheduler/internal/store"
)

func mkFeedbackFixture(t *testing.T, st *store.Store) (rec store.FarmingRecommendation, task store.Task) {
	t.Helper()
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "原神", Adapter: "genshin", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ch, err := st.CreateCharacter(store.Character{GameID: "genshin", Name: "香菱"})
	if err != nil {
		t.Fatal(err)
	}
	goal, err := st.CreateCharacterGoal(store.CharacterGoal{CharacterID: ch.ID, Name: "突破90"})
	if err != nil {
		t.Fatal(err)
	}
	mat, err := st.CreateMaterialItem(store.MaterialItem{GameID: "genshin", Name: "绝云椒椒", Category: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	rec, err = st.CreateFarmingRecommendation(store.FarmingRecommendation{
		GoalID: goal.ID, GameID: "genshin", MaterialID: mat.ID,
		Title: "刷绝云椒椒", Reason: "缺口 8", EstimatedRuns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err = st.CreateTask(store.Task{GameID: "genshin", Name: "rec task", Type: "script", Params: "{}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetFarmingRecommendationTask(rec.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	return rec, task
}

func TestRecommendationFeedbackAPI(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	rec, task := mkFeedbackFixture(t, st)

	end := time.Now().UTC()
	mk := func(status string) {
		t.Helper()
		if _, err := st.CreateExecution(store.Execution{TaskID: task.ID, Trigger: store.TriggerManual, Status: status, StartTime: &end, EndTime: &end}); err != nil {
			t.Fatal(err)
		}
	}
	mk(store.StatusSuccess)
	mk(store.StatusFailed)
	mk(store.StatusSuccess)

	c := srv.Client()
	get := func(path string) (*http.Response, error) {
		t.Helper()
		return c.Get(srv.URL + path)
	}

	resp, err := get("/api/planner/recommendations/" + strconv.FormatInt(rec.ID, 10) + "/feedback")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feedback status=%d", resp.StatusCode)
	}
	var fb store.RecommendationFeedback
	if err := json.NewDecoder(resp.Body).Decode(&fb); err != nil {
		t.Fatal(err)
	}
	if fb.RecommendationID != rec.ID || fb.TaskID == nil || *fb.TaskID != task.ID {
		t.Fatalf("feedback=%+v", fb)
	}
	if fb.TotalExecutions != 3 || fb.SuccessRuns != 2 || fb.FailedRuns != 1 || fb.CancelledRuns != 0 || fb.PendingOrRunning != 0 {
		t.Fatalf("counts=%+v", fb)
	}
	if fb.EstimatedRuns != 4 || fb.LastExecution == nil || fb.LastExecution.Status != store.StatusSuccess {
		t.Fatalf("estimate/last=%+v %+v", fb.EstimatedRuns, fb.LastExecution)
	}

	// Unknown recommendation is a 404, not an empty rollup.
	resp2, err := get("/api/planner/recommendations/424242/feedback")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown rec status=%d", resp2.StatusCode)
	}

	// A recommendation without a task link reports zeros (not an error).
	openRec, err := st.CreateFarmingRecommendation(store.FarmingRecommendation{
		GoalID: rec.GoalID, GameID: "genshin", MaterialID: rec.MaterialID,
		Title: "还没建任务", Reason: "无路线", EstimatedRuns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp3, err := get("/api/planner/recommendations/" + strconv.FormatInt(openRec.ID, 10) + "/feedback")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("no-task status=%d", resp3.StatusCode)
	}
	var empty store.RecommendationFeedback
	if err := json.NewDecoder(resp3.Body).Decode(&empty); err != nil {
		t.Fatal(err)
	}
	if empty.TaskID != nil || empty.TotalExecutions != 0 || empty.TaskMissing {
		t.Fatalf("no-task feedback=%+v", empty)
	}
}
