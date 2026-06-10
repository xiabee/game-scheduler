package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
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

func TestRouteAssetCRUDSearchAndStats(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "g")
	rt, err := s.CreateRoute(Route{
		GameID: "g", Adapter: "genshin", RouteType: "collect", Tags: []string{"mondstadt", "collect"},
		Name: "风车菊采集路线", FilePath: "D:/routes/风车菊.json", SourceURL: "https://example.com/video",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rt.UpdatedAt.IsZero() || rt.RouteType != "collect" {
		t.Fatalf("route defaults not populated: %+v", rt)
	}
	got, err := s.SearchRoutes(RouteFilter{Query: "wind", Tag: "mondstadt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("unexpected latin match: %+v", got)
	}
	got, err = s.SearchRoutes(RouteFilter{Query: "风车", Tag: "mondstadt", RouteType: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tags[0] != "mondstadt" {
		t.Fatalf("route search failed: %+v", got)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.RecordRouteRun(rt.ID, true, now); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetRoute(rt.ID)
	if after.SuccessCount != 1 || after.FailCount != 0 || after.LastRunAt == nil {
		t.Fatalf("stats not updated: %+v", after)
	}
}

func TestRouteMigrationKeepsExistingRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE games (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, adapter TEXT NOT NULL,
    tool_path TEXT NOT NULL DEFAULT '', working_dir TEXT NOT NULL DEFAULT '',
    extra_config TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE TABLE routes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    file_path TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);
CREATE TABLE tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    name TEXT NOT NULL, type TEXT NOT NULL, params TEXT NOT NULL DEFAULT '',
    max_retries INTEGER NOT NULL DEFAULT 0, retry_delay_sec INTEGER NOT NULL DEFAULT 0,
    timeout_sec INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = db.Exec(`INSERT INTO games (id,name,adapter,created_at,updated_at) VALUES (?,?,?,?,?)`, "g", "g", "genshin", now, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO routes (game_id,name,file_path,description,created_at) VALUES (?,?,?,?,?)`, "g", "old", "D:/old.json", "legacy", now)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	routes, err := s.ListRoutes("g")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Name != "old" || routes[0].RouteType != "other" || routes[0].UpdatedAt.IsZero() {
		t.Fatalf("legacy route not migrated: %+v", routes)
	}
	tk, err := s.CreateTask(Task{GameID: "g", RouteID: &routes[0].ID, Name: "task", Type: "script", Params: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if tk.RouteID == nil || *tk.RouteID != routes[0].ID {
		t.Fatalf("route_id not persisted: %+v", tk)
	}
}

func TestPlannerTablesCRUD(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "g")
	ch, err := s.CreateCharacter(Character{GameID: "g", Name: "Rover", RoleType: "dps", Tags: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	ch.Element = "spectro"
	ch, err = s.UpdateCharacter(ch)
	if err != nil || ch.Element != "spectro" {
		t.Fatalf("update character: %v %+v", err, ch)
	}
	if got, _ := s.ListCharacters(CharacterFilter{GameID: "g"}); len(got) != 1 || got[0].Tags[0] != "main" {
		t.Fatalf("list characters: %+v", got)
	}

	goal, err := s.CreateCharacterGoal(CharacterGoal{CharacterID: ch.ID, Name: "90级", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	goal.Status = "active"
	if _, err := s.UpdateCharacterGoal(goal); err != nil {
		t.Fatal(err)
	}
	mat, err := s.CreateMaterialItem(MaterialItem{GameID: "g", Name: "Boss Core", Category: "boss", SourceHint: "core", RouteTypeHint: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	mat.Notes = "weekly later"
	if _, err := s.UpdateMaterialItem(mat); err != nil {
		t.Fatal(err)
	}
	req, err := s.CreateMaterialRequirement(MaterialRequirement{GoalID: goal.ID, MaterialID: mat.ID, RequiredCount: 10, OwnedCount: 3, Priority: 5})
	if err != nil {
		t.Fatal(err)
	}
	req.OwnedCount = 4
	if _, err := s.UpdateMaterialRequirement(req); err != nil {
		t.Fatal(err)
	}
	rec, err := s.CreateFarmingRecommendation(FarmingRecommendation{
		GoalID: goal.ID, GameID: "g", MaterialID: mat.ID, RecommendationType: "manual",
		Title: "farm core", Reason: "missing", Priority: 5, EstimatedRuns: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(Task{GameID: "g", Name: "t", Type: "raw", Params: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if rec, err = s.SetFarmingRecommendationTask(rec.ID, task.ID); err != nil || rec.TaskID == nil || *rec.TaskID != task.ID {
		t.Fatalf("set task: %v %+v", err, rec)
	}
	if rec, err = s.SetFarmingRecommendationStatus(rec.ID, "completed"); err != nil || rec.Status != "completed" {
		t.Fatalf("set status: %v %+v", err, rec)
	}
	if got, _ := s.ListFarmingRecommendations(FarmingRecommendationFilter{GoalID: goal.ID}); len(got) != 1 {
		t.Fatalf("list recommendations: %+v", got)
	}
	if err := s.DeleteFarmingRecommendation(rec.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMaterialRequirement(req.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMaterialItem(mat.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCharacterGoal(goal.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCharacter(ch.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPlannerMigrationKeepsOldTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old-planner.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = db.Exec(`
CREATE TABLE games (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, adapter TEXT NOT NULL,
    tool_path TEXT NOT NULL DEFAULT '', working_dir TEXT NOT NULL DEFAULT '',
    extra_config TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE TABLE tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    route_id INTEGER REFERENCES routes(id) ON DELETE SET NULL,
    name TEXT NOT NULL, type TEXT NOT NULL, params TEXT NOT NULL DEFAULT '',
    max_retries INTEGER NOT NULL DEFAULT 0, retry_delay_sec INTEGER NOT NULL DEFAULT 0,
    timeout_sec INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE TABLE routes (
    id INTEGER PRIMARY KEY AUTOINCREMENT, game_id TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    adapter TEXT NOT NULL DEFAULT '', route_type TEXT NOT NULL DEFAULT 'other', tags TEXT NOT NULL DEFAULT '[]',
    name TEXT NOT NULL, file_path TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '', source_title TEXT NOT NULL DEFAULT '', last_run_at TIMESTAMP,
    success_count INTEGER NOT NULL DEFAULT 0, fail_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE TABLE plans (
    id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL,
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    cron_expr TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
    last_run_at TIMESTAMP, next_run_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE TABLE executions (
    id INTEGER PRIMARY KEY AUTOINCREMENT, task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    plan_id INTEGER REFERENCES plans(id) ON DELETE SET NULL, trigger TEXT NOT NULL, status TEXT NOT NULL,
    command TEXT NOT NULL DEFAULT '', stdout TEXT NOT NULL DEFAULT '', stderr TEXT NOT NULL DEFAULT '',
    exit_code INTEGER, error_msg TEXT NOT NULL DEFAULT '', screenshot_path TEXT NOT NULL DEFAULT '',
    retry_count INTEGER NOT NULL DEFAULT 0, start_time TIMESTAMP, end_time TIMESTAMP, created_at TIMESTAMP NOT NULL
);
INSERT INTO games (id,name,adapter,created_at,updated_at) VALUES ('g','g','genshin',?,?);
`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateCharacter(Character{GameID: "g", Name: "new"}); err != nil {
		t.Fatalf("new planner table missing after migration: %v", err)
	}
	if games, err := s.ListGames(); err != nil || len(games) != 1 {
		t.Fatalf("old games table broken: %v %+v", err, games)
	}
}
