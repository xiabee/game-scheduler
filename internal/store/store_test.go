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
