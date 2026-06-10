// Package store provides SQLite-backed persistence for games, tasks, routes,
// plans and execution logs.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no cgo
)

// ErrNotFound is returned when a lookup by id finds no row.
var ErrNotFound = errors.New("store: not found")

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies the
// schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite writer is single; keep it simple and safe
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	for _, col := range []struct {
		table string
		name  string
		def   string
	}{
		{"tasks", "route_id", "INTEGER REFERENCES routes(id) ON DELETE SET NULL"},
		{"routes", "adapter", "TEXT NOT NULL DEFAULT ''"},
		{"routes", "route_type", "TEXT NOT NULL DEFAULT 'other'"},
		{"routes", "tags", "TEXT NOT NULL DEFAULT '[]'"},
		{"routes", "source_url", "TEXT NOT NULL DEFAULT ''"},
		{"routes", "source_title", "TEXT NOT NULL DEFAULT ''"},
		{"routes", "last_run_at", "TIMESTAMP"},
		{"routes", "success_count", "INTEGER NOT NULL DEFAULT 0"},
		{"routes", "fail_count", "INTEGER NOT NULL DEFAULT 0"},
		{"routes", "updated_at", "TIMESTAMP"},
	} {
		if err := s.ensureColumn(col.table, col.name, col.def); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`
UPDATE routes SET updated_at=created_at WHERE updated_at IS NULL;
UPDATE routes SET route_type='other' WHERE route_type='';
UPDATE routes SET tags='[]' WHERE tags='';
CREATE INDEX IF NOT EXISTS idx_routes_type ON routes(route_type);
CREATE INDEX IF NOT EXISTS idx_routes_updated ON routes(updated_at);
CREATE INDEX IF NOT EXISTS idx_tasks_route ON tasks(route_id);
`)
	return err
}

func (s *Store) ensureColumn(table, name, def string) error {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	exists := false
	for rows.Next() {
		var cid int
		var colName, colType string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if colName == name {
			exists = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, def))
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS games (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    adapter      TEXT NOT NULL,
    tool_path    TEXT NOT NULL DEFAULT '',
    working_dir  TEXT NOT NULL DEFAULT '',
    extra_config TEXT NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id         TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    route_id        INTEGER REFERENCES routes(id) ON DELETE SET NULL,
    name            TEXT NOT NULL,
    type            TEXT NOT NULL,
    params          TEXT NOT NULL DEFAULT '',
    max_retries     INTEGER NOT NULL DEFAULT 0,
    retry_delay_sec INTEGER NOT NULL DEFAULT 0,
    timeout_sec     INTEGER NOT NULL DEFAULT 0,
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TIMESTAMP NOT NULL,
    updated_at      TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_game ON tasks(game_id);

CREATE TABLE IF NOT EXISTS routes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id     TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    adapter     TEXT NOT NULL DEFAULT '',
    route_type  TEXT NOT NULL DEFAULT 'other',
    tags        TEXT NOT NULL DEFAULT '[]',
    name        TEXT NOT NULL,
    file_path   TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    source_url  TEXT NOT NULL DEFAULT '',
    source_title TEXT NOT NULL DEFAULT '',
    last_run_at TIMESTAMP,
    success_count INTEGER NOT NULL DEFAULT 0,
    fail_count INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_routes_game ON routes(game_id);

CREATE TABLE IF NOT EXISTS characters (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id    TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    role_type  TEXT NOT NULL DEFAULT '',
    element    TEXT NOT NULL DEFAULT '',
    weapon     TEXT NOT NULL DEFAULT '',
    rarity     INTEGER NOT NULL DEFAULT 0,
    tags       TEXT NOT NULL DEFAULT '[]',
    notes      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_characters_game ON characters(game_id);

CREATE TABLE IF NOT EXISTS character_goals (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    character_id     INTEGER NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    target_level     TEXT NOT NULL DEFAULT '',
    target_skill     TEXT NOT NULL DEFAULT '',
    target_equipment TEXT NOT NULL DEFAULT '',
    priority         INTEGER NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'open',
    notes            TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMP NOT NULL,
    updated_at       TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_character_goals_character ON character_goals(character_id);
CREATE INDEX IF NOT EXISTS idx_character_goals_status ON character_goals(status);

CREATE TABLE IF NOT EXISTS material_items (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id         TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    category        TEXT NOT NULL DEFAULT '',
    source_hint     TEXT NOT NULL DEFAULT '',
    route_type_hint TEXT NOT NULL DEFAULT '',
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMP NOT NULL,
    updated_at      TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_material_items_game ON material_items(game_id);

CREATE TABLE IF NOT EXISTS material_requirements (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    goal_id        INTEGER NOT NULL REFERENCES character_goals(id) ON DELETE CASCADE,
    material_id    INTEGER NOT NULL REFERENCES material_items(id) ON DELETE CASCADE,
    required_count INTEGER NOT NULL DEFAULT 0,
    owned_count    INTEGER NOT NULL DEFAULT 0,
    priority       INTEGER NOT NULL DEFAULT 0,
    notes          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMP NOT NULL,
    updated_at     TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_material_requirements_goal ON material_requirements(goal_id);
CREATE INDEX IF NOT EXISTS idx_material_requirements_material ON material_requirements(material_id);

CREATE TABLE IF NOT EXISTS farming_recommendations (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    goal_id             INTEGER NOT NULL REFERENCES character_goals(id) ON DELETE CASCADE,
    game_id             TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    material_id         INTEGER NOT NULL REFERENCES material_items(id) ON DELETE CASCADE,
    route_id            INTEGER REFERENCES routes(id) ON DELETE SET NULL,
    task_id             INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
    recommendation_type TEXT NOT NULL DEFAULT 'manual',
    title               TEXT NOT NULL,
    reason              TEXT NOT NULL,
    priority            INTEGER NOT NULL DEFAULT 0,
    estimated_runs      INTEGER NOT NULL DEFAULT 0,
    estimated_stamina   INTEGER NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'open',
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_farming_recommendations_goal ON farming_recommendations(goal_id);
CREATE INDEX IF NOT EXISTS idx_farming_recommendations_game ON farming_recommendations(game_id);
CREATE INDEX IF NOT EXISTS idx_farming_recommendations_status ON farming_recommendations(status);

CREATE TABLE IF NOT EXISTS plans (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    task_id     INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    cron_expr   TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    last_run_at TIMESTAMP,
    next_run_at TIMESTAMP,
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_plans_task ON plans(task_id);

CREATE TABLE IF NOT EXISTS executions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id         INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    plan_id         INTEGER REFERENCES plans(id) ON DELETE SET NULL,
    trigger         TEXT NOT NULL,
    status          TEXT NOT NULL,
    command         TEXT NOT NULL DEFAULT '',
    stdout          TEXT NOT NULL DEFAULT '',
    stderr          TEXT NOT NULL DEFAULT '',
    exit_code       INTEGER,
    error_msg       TEXT NOT NULL DEFAULT '',
    screenshot_path TEXT NOT NULL DEFAULT '',
    retry_count     INTEGER NOT NULL DEFAULT 0,
    start_time      TIMESTAMP,
    end_time        TIMESTAMP,
    created_at      TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_exec_task ON executions(task_id);
CREATE INDEX IF NOT EXISTS idx_exec_status ON executions(status);
`

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------- Games ----------

// CreateGame inserts a game.
func (s *Store) CreateGame(g Game) (Game, error) {
	now := time.Now().UTC()
	g.CreatedAt, g.UpdatedAt = now, now
	_, err := s.db.Exec(`INSERT INTO games (id,name,adapter,tool_path,working_dir,extra_config,enabled,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		g.ID, g.Name, g.Adapter, g.ToolPath, g.WorkingDir, g.ExtraConfig, b2i(g.Enabled), g.CreatedAt, g.UpdatedAt)
	if err != nil {
		return Game{}, err
	}
	return g, nil
}

// UpdateGame updates mutable fields of a game.
func (s *Store) UpdateGame(g Game) (Game, error) {
	g.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(`UPDATE games SET name=?,adapter=?,tool_path=?,working_dir=?,extra_config=?,enabled=?,updated_at=? WHERE id=?`,
		g.Name, g.Adapter, g.ToolPath, g.WorkingDir, g.ExtraConfig, b2i(g.Enabled), g.UpdatedAt, g.ID)
	if err != nil {
		return Game{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Game{}, ErrNotFound
	}
	return s.GetGame(g.ID)
}

// GetGame fetches a game by id.
func (s *Store) GetGame(id string) (Game, error) {
	var g Game
	var enabled int
	err := s.db.QueryRow(`SELECT id,name,adapter,tool_path,working_dir,extra_config,enabled,created_at,updated_at FROM games WHERE id=?`, id).
		Scan(&g.ID, &g.Name, &g.Adapter, &g.ToolPath, &g.WorkingDir, &g.ExtraConfig, &enabled, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Game{}, ErrNotFound
	}
	if err != nil {
		return Game{}, err
	}
	g.Enabled = enabled != 0
	return g, nil
}

// ListGames returns all games ordered by id.
func (s *Store) ListGames() ([]Game, error) {
	rows, err := s.db.Query(`SELECT id,name,adapter,tool_path,working_dir,extra_config,enabled,created_at,updated_at FROM games ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Game{}
	for rows.Next() {
		var g Game
		var enabled int
		if err := rows.Scan(&g.ID, &g.Name, &g.Adapter, &g.ToolPath, &g.WorkingDir, &g.ExtraConfig, &enabled, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		g.Enabled = enabled != 0
		out = append(out, g)
	}
	return out, rows.Err()
}

// DeleteGame removes a game (cascading to its tasks/routes).
func (s *Store) DeleteGame(id string) error {
	res, err := s.db.Exec(`DELETE FROM games WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- Tasks ----------

// CreateTask inserts a task.
func (s *Store) CreateTask(t Task) (Task, error) {
	now := time.Now().UTC()
	t.CreatedAt, t.UpdatedAt = now, now
	res, err := s.db.Exec(`INSERT INTO tasks (game_id,route_id,name,type,params,max_retries,retry_delay_sec,timeout_sec,enabled,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		t.GameID, t.RouteID, t.Name, t.Type, t.Params, t.MaxRetries, t.RetryDelaySec, t.TimeoutSec, b2i(t.Enabled), t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return Task{}, err
	}
	t.ID, _ = res.LastInsertId()
	return t, nil
}

// UpdateTask updates mutable fields of a task.
func (s *Store) UpdateTask(t Task) (Task, error) {
	t.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(`UPDATE tasks SET game_id=?,route_id=?,name=?,type=?,params=?,max_retries=?,retry_delay_sec=?,timeout_sec=?,enabled=?,updated_at=? WHERE id=?`,
		t.GameID, t.RouteID, t.Name, t.Type, t.Params, t.MaxRetries, t.RetryDelaySec, t.TimeoutSec, b2i(t.Enabled), t.UpdatedAt, t.ID)
	if err != nil {
		return Task{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Task{}, ErrNotFound
	}
	return s.GetTask(t.ID)
}

// GetTask fetches a task by id.
func (s *Store) GetTask(id int64) (Task, error) {
	var t Task
	var enabled int
	err := s.db.QueryRow(`SELECT id,game_id,route_id,name,type,params,max_retries,retry_delay_sec,timeout_sec,enabled,created_at,updated_at FROM tasks WHERE id=?`, id).
		Scan(&t.ID, &t.GameID, &t.RouteID, &t.Name, &t.Type, &t.Params, &t.MaxRetries, &t.RetryDelaySec, &t.TimeoutSec, &enabled, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	t.Enabled = enabled != 0
	return t, nil
}

// ListTasks returns tasks, optionally filtered by gameID (empty = all).
func (s *Store) ListTasks(gameID string) ([]Task, error) {
	q := `SELECT id,game_id,route_id,name,type,params,max_retries,retry_delay_sec,timeout_sec,enabled,created_at,updated_at FROM tasks`
	var args []any
	if gameID != "" {
		q += ` WHERE game_id=?`
		args = append(args, gameID)
	}
	q += ` ORDER BY id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		var enabled int
		if err := rows.Scan(&t.ID, &t.GameID, &t.RouteID, &t.Name, &t.Type, &t.Params, &t.MaxRetries, &t.RetryDelaySec, &t.TimeoutSec, &enabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Enabled = enabled != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTask removes a task.
func (s *Store) DeleteTask(id int64) error {
	res, err := s.db.Exec(`DELETE FROM tasks WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- Routes ----------

// CreateRoute inserts a route.
func (s *Store) CreateRoute(r Route) (Route, error) {
	now := time.Now().UTC()
	r.CreatedAt, r.UpdatedAt = now, now
	if r.RouteType == "" {
		r.RouteType = "other"
	}
	tags, err := encodeTags(r.Tags)
	if err != nil {
		return Route{}, err
	}
	res, err := s.db.Exec(`INSERT INTO routes (game_id,adapter,route_type,tags,name,file_path,description,source_url,source_title,last_run_at,success_count,fail_count,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.GameID, r.Adapter, r.RouteType, tags, r.Name, r.FilePath, r.Description, r.SourceURL, r.SourceTitle, r.LastRunAt, r.SuccessCount, r.FailCount, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return Route{}, err
	}
	r.ID, _ = res.LastInsertId()
	return r, nil
}

// GetRoute fetches a route by id.
func (s *Store) GetRoute(id int64) (Route, error) {
	var r Route
	var tags string
	err := s.db.QueryRow(`SELECT id,game_id,adapter,route_type,tags,name,file_path,description,source_url,source_title,last_run_at,success_count,fail_count,created_at,updated_at FROM routes WHERE id=?`, id).
		Scan(&r.ID, &r.GameID, &r.Adapter, &r.RouteType, &tags, &r.Name, &r.FilePath, &r.Description, &r.SourceURL, &r.SourceTitle, &r.LastRunAt, &r.SuccessCount, &r.FailCount, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Route{}, ErrNotFound
	}
	if err != nil {
		return Route{}, err
	}
	r.Tags = decodeTags(tags)
	return r, nil
}

// UpdateRoute updates mutable route metadata.
func (s *Store) UpdateRoute(r Route) (Route, error) {
	if r.RouteType == "" {
		r.RouteType = "other"
	}
	r.UpdatedAt = time.Now().UTC()
	tags, err := encodeTags(r.Tags)
	if err != nil {
		return Route{}, err
	}
	res, err := s.db.Exec(`UPDATE routes SET game_id=?,adapter=?,route_type=?,tags=?,name=?,file_path=?,description=?,source_url=?,source_title=?,last_run_at=?,success_count=?,fail_count=?,updated_at=? WHERE id=?`,
		r.GameID, r.Adapter, r.RouteType, tags, r.Name, r.FilePath, r.Description, r.SourceURL, r.SourceTitle, r.LastRunAt, r.SuccessCount, r.FailCount, r.UpdatedAt, r.ID)
	if err != nil {
		return Route{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Route{}, ErrNotFound
	}
	return s.GetRoute(r.ID)
}

// ListRoutes returns routes, optionally filtered by gameID.
func (s *Store) ListRoutes(gameID string) ([]Route, error) {
	return s.SearchRoutes(RouteFilter{GameID: gameID})
}

// RouteFilter narrows route searches.
type RouteFilter struct {
	GameID    string
	RouteType string
	Tag       string
	Query     string
	Limit     int
}

// SearchRoutes returns route assets newest-first. Query matches name, file path,
// description, source title, source URL, and tags.
func (s *Store) SearchRoutes(f RouteFilter) ([]Route, error) {
	q := `SELECT id,game_id,adapter,route_type,tags,name,file_path,description,source_url,source_title,last_run_at,success_count,fail_count,created_at,updated_at FROM routes WHERE 1=1`
	var args []any
	if f.GameID != "" {
		q += ` AND game_id=?`
		args = append(args, f.GameID)
	}
	if f.RouteType != "" {
		q += ` AND route_type=?`
		args = append(args, f.RouteType)
	}
	if f.Tag != "" {
		q += ` AND tags LIKE ?`
		args = append(args, "%"+f.Tag+"%")
	}
	if f.Query != "" {
		like := "%" + strings.ToLower(f.Query) + "%"
		q += ` AND (lower(name) LIKE ? OR lower(file_path) LIKE ? OR lower(description) LIKE ? OR lower(source_title) LIKE ? OR lower(source_url) LIKE ? OR lower(tags) LIKE ?)`
		args = append(args, like, like, like, like, like, like)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q += fmt.Sprintf(` ORDER BY updated_at DESC, id DESC LIMIT %d`, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Route{}
	for rows.Next() {
		var r Route
		var tags string
		if err := rows.Scan(&r.ID, &r.GameID, &r.Adapter, &r.RouteType, &tags, &r.Name, &r.FilePath, &r.Description, &r.SourceURL, &r.SourceTitle, &r.LastRunAt, &r.SuccessCount, &r.FailCount, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Tags = decodeTags(tags)
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertRouteByFile inserts or updates one scanned route asset.
func (s *Store) UpsertRouteByFile(r Route) (Route, bool, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM routes WHERE game_id=? AND file_path=?`, r.GameID, r.FilePath).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		out, err := s.CreateRoute(r)
		return out, true, err
	}
	if err != nil {
		return Route{}, false, err
	}
	existing, err := s.GetRoute(id)
	if err != nil {
		return Route{}, false, err
	}
	existing.Adapter = r.Adapter
	existing.RouteType = r.RouteType
	existing.Tags = mergeTags(existing.Tags, r.Tags)
	existing.Name = r.Name
	existing.FilePath = r.FilePath
	if existing.Description == "" {
		existing.Description = r.Description
	}
	out, err := s.UpdateRoute(existing)
	return out, false, err
}

// RecordRouteRun updates aggregate stats after a task associated with a route
// finishes.
func (s *Store) RecordRouteRun(routeID int64, success bool, at time.Time) error {
	col := "fail_count"
	if success {
		col = "success_count"
	}
	res, err := s.db.Exec(fmt.Sprintf(`UPDATE routes SET last_run_at=?, %s=%s+1, updated_at=? WHERE id=?`, col, col),
		at.UTC(), at.UTC(), routeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteRoute removes a route.
func (s *Store) DeleteRoute(id int64) error {
	res, err := s.db.Exec(`DELETE FROM routes WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func encodeTags(tags []string) (string, error) {
	clean := mergeTags(nil, tags)
	b, err := json.Marshal(clean)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeTags(s string) []string {
	var tags []string
	if err := json.Unmarshal([]byte(s), &tags); err == nil {
		return mergeTags(nil, tags)
	}
	if s == "" {
		return []string{}
	}
	return mergeTags(nil, strings.Split(s, ","))
}

func mergeTags(base, extra []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, tag := range append(base, extra...) {
		tag = strings.TrimSpace(strings.ToLower(tag))
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

// ---------- Plans ----------

// CreatePlan inserts a plan.
func (s *Store) CreatePlan(p Plan) (Plan, error) {
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	res, err := s.db.Exec(`INSERT INTO plans (name,task_id,cron_expr,enabled,last_run_at,next_run_at,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		p.Name, p.TaskID, p.CronExpr, b2i(p.Enabled), p.LastRunAt, p.NextRunAt, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return Plan{}, err
	}
	p.ID, _ = res.LastInsertId()
	return p, nil
}

// UpdatePlan updates mutable fields of a plan.
func (s *Store) UpdatePlan(p Plan) (Plan, error) {
	p.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(`UPDATE plans SET name=?,task_id=?,cron_expr=?,enabled=?,updated_at=? WHERE id=?`,
		p.Name, p.TaskID, p.CronExpr, b2i(p.Enabled), p.UpdatedAt, p.ID)
	if err != nil {
		return Plan{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Plan{}, ErrNotFound
	}
	return s.GetPlan(p.ID)
}

// SetPlanRunTimes records the last/next fire times for a plan.
func (s *Store) SetPlanRunTimes(id int64, last, next *time.Time) error {
	_, err := s.db.Exec(`UPDATE plans SET last_run_at=?,next_run_at=? WHERE id=?`, last, next, id)
	return err
}

// GetPlan fetches a plan by id.
func (s *Store) GetPlan(id int64) (Plan, error) {
	var p Plan
	var enabled int
	err := s.db.QueryRow(`SELECT id,name,task_id,cron_expr,enabled,last_run_at,next_run_at,created_at,updated_at FROM plans WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.TaskID, &p.CronExpr, &enabled, &p.LastRunAt, &p.NextRunAt, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, ErrNotFound
	}
	if err != nil {
		return Plan{}, err
	}
	p.Enabled = enabled != 0
	return p, nil
}

// ListPlans returns all plans. If onlyEnabled is true, only enabled ones.
func (s *Store) ListPlans(onlyEnabled bool) ([]Plan, error) {
	q := `SELECT id,name,task_id,cron_expr,enabled,last_run_at,next_run_at,created_at,updated_at FROM plans`
	if onlyEnabled {
		q += ` WHERE enabled=1`
	}
	q += ` ORDER BY id`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		var p Plan
		var enabled int
		if err := rows.Scan(&p.ID, &p.Name, &p.TaskID, &p.CronExpr, &enabled, &p.LastRunAt, &p.NextRunAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePlan removes a plan.
func (s *Store) DeletePlan(id int64) error {
	res, err := s.db.Exec(`DELETE FROM plans WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- Executions ----------

// CreateExecution inserts a new execution row (typically status=pending) and
// returns it with its assigned id.
func (s *Store) CreateExecution(e Execution) (Execution, error) {
	e.CreatedAt = time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO executions (task_id,plan_id,trigger,status,command,stdout,stderr,exit_code,error_msg,screenshot_path,retry_count,start_time,end_time,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.TaskID, e.PlanID, e.Trigger, e.Status, e.Command, e.Stdout, e.Stderr, e.ExitCode, e.ErrorMsg, e.ScreenshotPath, e.RetryCount, e.StartTime, e.EndTime, e.CreatedAt)
	if err != nil {
		return Execution{}, err
	}
	e.ID, _ = res.LastInsertId()
	return e, nil
}

// UpdateExecution persists all mutable fields of an execution.
func (s *Store) UpdateExecution(e Execution) error {
	res, err := s.db.Exec(`UPDATE executions SET status=?,command=?,stdout=?,stderr=?,exit_code=?,error_msg=?,screenshot_path=?,retry_count=?,start_time=?,end_time=? WHERE id=?`,
		e.Status, e.Command, e.Stdout, e.Stderr, e.ExitCode, e.ErrorMsg, e.ScreenshotPath, e.RetryCount, e.StartTime, e.EndTime, e.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetExecution fetches an execution by id.
func (s *Store) GetExecution(id int64) (Execution, error) {
	var e Execution
	err := s.db.QueryRow(`SELECT id,task_id,plan_id,trigger,status,command,stdout,stderr,exit_code,error_msg,screenshot_path,retry_count,start_time,end_time,created_at FROM executions WHERE id=?`, id).
		Scan(&e.ID, &e.TaskID, &e.PlanID, &e.Trigger, &e.Status, &e.Command, &e.Stdout, &e.Stderr, &e.ExitCode, &e.ErrorMsg, &e.ScreenshotPath, &e.RetryCount, &e.StartTime, &e.EndTime, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Execution{}, ErrNotFound
	}
	if err != nil {
		return Execution{}, err
	}
	return e, nil
}

// RecoverOrphans marks any execution left in pending/running (because the
// server stopped while it was in flight) as failed. The in-memory process and
// cancel handle no longer exist, so the row would otherwise be stuck forever.
// Returns the number of rows reconciled.
func (s *Store) RecoverOrphans() (int64, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`UPDATE executions
		SET status=?, error_msg=?, end_time=COALESCE(end_time, ?)
		WHERE status IN (?, ?)`,
		StatusFailed, "interrupted: server stopped while task was in flight", now,
		StatusPending, StatusRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// CountActiveByTask returns how many executions for taskID are currently
// pending or running, per the database. Used as a backstop for the in-memory
// active-task guard.
func (s *Store) CountActiveByTask(taskID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM executions WHERE task_id=? AND status IN (?, ?)`,
		taskID, StatusPending, StatusRunning).Scan(&n)
	return n, err
}

// ExecutionFilter narrows ListExecutions.
type ExecutionFilter struct {
	TaskID int64  // 0 = any
	Status string // "" = any
	Limit  int    // <=0 => 100
}

// ListExecutions returns executions newest-first matching the filter.
func (s *Store) ListExecutions(f ExecutionFilter) ([]Execution, error) {
	q := `SELECT id,task_id,plan_id,trigger,status,command,stdout,stderr,exit_code,error_msg,screenshot_path,retry_count,start_time,end_time,created_at FROM executions WHERE 1=1`
	var args []any
	if f.TaskID != 0 {
		q += ` AND task_id=?`
		args = append(args, f.TaskID)
	}
	if f.Status != "" {
		q += ` AND status=?`
		args = append(args, f.Status)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q += fmt.Sprintf(` ORDER BY id DESC LIMIT %d`, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Execution{}
	for rows.Next() {
		var e Execution
		if err := rows.Scan(&e.ID, &e.TaskID, &e.PlanID, &e.Trigger, &e.Status, &e.Command, &e.Stdout, &e.Stderr, &e.ExitCode, &e.ErrorMsg, &e.ScreenshotPath, &e.RetryCount, &e.StartTime, &e.EndTime, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
