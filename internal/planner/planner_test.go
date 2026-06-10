package planner

import (
	"path/filepath"
	"testing"

	"github.com/xiabee/game-scheduler/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "planner.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seedPlanner(t *testing.T, st *store.Store) (store.CharacterGoal, store.MaterialItem, store.MaterialItem) {
	t.Helper()
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "原神", Adapter: "genshin", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ch, err := st.CreateCharacter(store.Character{GameID: "genshin", Name: "香菱"})
	if err != nil {
		t.Fatal(err)
	}
	goal, err := st.CreateCharacterGoal(store.CharacterGoal{CharacterID: ch.ID, Name: "突破", Priority: 1})
	if err != nil {
		t.Fatal(err)
	}
	boss, err := st.CreateMaterialItem(store.MaterialItem{GameID: "genshin", Name: "常燃火种", Category: "boss", SourceHint: "爆炎树", RouteTypeHint: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	flower, err := st.CreateMaterialItem(store.MaterialItem{GameID: "genshin", Name: "绝云椒椒", Category: "collect", SourceHint: "绝云", RouteTypeHint: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	return goal, boss, flower
}

func TestRecommendMissingPriorityAndHints(t *testing.T) {
	st := testStore(t)
	goal, boss, flower := seedPlanner(t, st)
	_, _ = st.CreateRoute(store.Route{GameID: "genshin", Adapter: "genshin", RouteType: "collect", Tags: []string{"璃月"}, Name: "绝云椒椒采集", FilePath: "D:/routes/jueyun.json"})
	_, _ = st.CreateRoute(store.Route{GameID: "genshin", Adapter: "genshin", RouteType: "boss", Name: "爆炎树刷取", Description: "常燃火种", FilePath: "D:/routes/boss.json"})
	_, _ = st.CreateMaterialRequirement(store.MaterialRequirement{GoalID: goal.ID, MaterialID: flower.ID, RequiredCount: 20, OwnedCount: 2, Priority: 2})
	_, _ = st.CreateMaterialRequirement(store.MaterialRequirement{GoalID: goal.ID, MaterialID: boss.ID, RequiredCount: 6, OwnedCount: 0, Priority: 9})

	recs, err := New(st).Recommend(Options{GoalID: goal.ID, MaxTasks: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("recs=%+v", recs)
	}
	if recs[0].MaterialID != boss.ID || recs[0].RouteID == nil || recs[0].Reason == "" {
		t.Fatalf("priority/type match failed: %+v", recs[0])
	}
	if recs[1].MaterialID != flower.ID || recs[1].RouteID == nil {
		t.Fatalf("source hint match failed: %+v", recs[1])
	}
}

func TestRecommendSkipsNoMissing(t *testing.T) {
	st := testStore(t)
	goal, boss, _ := seedPlanner(t, st)
	_, _ = st.CreateMaterialRequirement(store.MaterialRequirement{GoalID: goal.ID, MaterialID: boss.ID, RequiredCount: 3, OwnedCount: 3, Priority: 9})
	recs, err := New(st).Recommend(Options{GoalID: goal.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("want no recommendations, got %+v", recs)
	}
}

func TestRecommendManualWhenNoRoute(t *testing.T) {
	st := testStore(t)
	goal, boss, _ := seedPlanner(t, st)
	_, _ = st.CreateMaterialRequirement(store.MaterialRequirement{GoalID: goal.ID, MaterialID: boss.ID, RequiredCount: 4, OwnedCount: 1, Priority: 1})
	recs, err := New(st).Recommend(Options{GoalID: goal.ID, DailyStamina: 160, MaxTasks: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].RouteID != nil || recs[0].RecommendationType != "manual" || recs[0].Reason == "" {
		t.Fatalf("manual recommendation failed: %+v", recs)
	}
}
