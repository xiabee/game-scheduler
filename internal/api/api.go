// Package api exposes the scheduler over a small JSON REST interface built on
// net/http's pattern router (Go 1.22+).
package api

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/events"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/guide"
	"github.com/xiabee/game-scheduler/internal/monitor"
	"github.com/xiabee/game-scheduler/internal/scheduler"
	"github.com/xiabee/game-scheduler/internal/store"
	"github.com/xiabee/game-scheduler/internal/task"
)

//go:embed web/index.html
var webFS embed.FS

// Server holds dependencies for the HTTP handlers.
type Server struct {
	store         *store.Store
	svc           *task.Service
	sched         *scheduler.Scheduler
	reg           *game.Registry
	bus           *events.Bus
	mon           *monitor.Monitor
	guides        guide.Searcher
	log           *slog.Logger
	screenshotDir string
	authToken     string

	streamsClosing   sync.Once
	streamsClosingCh chan struct{} // closed by ShutdownStreams

	hub      *streamHub // shared SSE snapshot broadcaster
	hubStart sync.Once
}

// SetGuideSearcher overrides the Bilibili search client (tests inject a stub).
func (s *Server) SetGuideSearcher(g guide.Searcher) { s.guides = g }

// ShutdownStreams releases all SSE clients so http.Server.Shutdown does not
// wait out its whole timeout on long-lived event streams. Wire it via
// http.Server.RegisterOnShutdown.
func (s *Server) ShutdownStreams() { s.streamsClosing.Do(func() { close(s.streamsClosingCh) }) }

// New builds an API server. mon may be nil (no resource panel).
func New(s *store.Store, svc *task.Service, sched *scheduler.Scheduler, reg *game.Registry, bus *events.Bus, mon *monitor.Monitor, cfg config.Config, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		store:            s,
		svc:              svc,
		sched:            sched,
		reg:              reg,
		bus:              bus,
		mon:              mon,
		guides:           guide.NewClient(),
		log:              log,
		screenshotDir:    cfg.ScreenshotDir(),
		authToken:        cfg.AuthToken,
		streamsClosingCh: make(chan struct{}),
		hub:              newStreamHub(),
	}
}

// Handler returns the configured HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "adapters": s.reg.Keys()})
	})

	// Control dashboard (single embedded page) + its aggregate feed + live stream.
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /api/dashboard", s.dashboard)
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("GET /api/meta", s.meta)
	mux.HandleFunc("POST /api/discover", s.discoverScan)
	mux.HandleFunc("GET /api/guides/search", s.guidesSearch)
	mux.HandleFunc("GET /screenshots/{name}", s.screenshot)

	// Games
	mux.HandleFunc("GET /api/games", s.listGames)
	mux.HandleFunc("POST /api/games", s.createGame)
	mux.HandleFunc("GET /api/games/{id}", s.getGame)
	mux.HandleFunc("PUT /api/games/{id}", s.updateGame)
	mux.HandleFunc("DELETE /api/games/{id}", s.deleteGame)

	// Tasks
	mux.HandleFunc("GET /api/tasks", s.listTasks)
	mux.HandleFunc("POST /api/tasks", s.createTask)
	mux.HandleFunc("GET /api/tasks/{id}", s.getTask)
	mux.HandleFunc("PUT /api/tasks/{id}", s.updateTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.deleteTask)
	mux.HandleFunc("POST /api/tasks/{id}/run", s.runTask)
	mux.HandleFunc("GET /api/tasks/{id}/preflight", s.preflightTask)

	// Routes
	mux.HandleFunc("GET /api/routes", s.listRoutes)
	mux.HandleFunc("POST /api/routes", s.createRoute)
	mux.HandleFunc("POST /api/routes/scan", s.scanRoutes)
	mux.HandleFunc("GET /api/routes/search", s.searchRoutes)
	mux.HandleFunc("GET /api/routes/{id}", s.getRoute)
	mux.HandleFunc("PUT /api/routes/{id}", s.updateRoute)
	mux.HandleFunc("POST /api/routes/{id}/create-task", s.createTaskFromRoute)
	mux.HandleFunc("DELETE /api/routes/{id}", s.deleteRoute)

	// Character planner
	mux.HandleFunc("GET /api/characters", s.listCharacters)
	mux.HandleFunc("POST /api/characters", s.createCharacter)
	mux.HandleFunc("GET /api/characters/{id}", s.getCharacter)
	mux.HandleFunc("PUT /api/characters/{id}", s.updateCharacter)
	mux.HandleFunc("DELETE /api/characters/{id}", s.deleteCharacter)
	mux.HandleFunc("GET /api/character-goals", s.listCharacterGoals)
	mux.HandleFunc("POST /api/character-goals", s.createCharacterGoal)
	mux.HandleFunc("GET /api/character-goals/{id}", s.getCharacterGoal)
	mux.HandleFunc("PUT /api/character-goals/{id}", s.updateCharacterGoal)
	mux.HandleFunc("DELETE /api/character-goals/{id}", s.deleteCharacterGoal)
	mux.HandleFunc("GET /api/materials", s.listMaterials)
	mux.HandleFunc("POST /api/materials", s.createMaterial)
	mux.HandleFunc("GET /api/materials/{id}", s.getMaterial)
	mux.HandleFunc("PUT /api/materials/{id}", s.updateMaterial)
	mux.HandleFunc("DELETE /api/materials/{id}", s.deleteMaterial)
	mux.HandleFunc("GET /api/material-requirements", s.listMaterialRequirements)
	mux.HandleFunc("POST /api/material-requirements", s.createMaterialRequirement)
	mux.HandleFunc("GET /api/material-requirements/{id}", s.getMaterialRequirement)
	mux.HandleFunc("PUT /api/material-requirements/{id}", s.updateMaterialRequirement)
	mux.HandleFunc("DELETE /api/material-requirements/{id}", s.deleteMaterialRequirement)
	mux.HandleFunc("POST /api/planner/recommend", s.recommendFarming)
	mux.HandleFunc("GET /api/planner/recommendations", s.listFarmingRecommendations)
	mux.HandleFunc("POST /api/planner/recommendations/{id}/create-task", s.createTaskFromRecommendation)
	mux.HandleFunc("POST /api/planner/recommendations/{id}/attach-route", s.attachRecommendationRoute)
	mux.HandleFunc("POST /api/planner/recommendations/{id}/create-plan", s.createPlanFromRecommendation)
	mux.HandleFunc("POST /api/planner/recommendations/{id}/dismiss", s.dismissRecommendation)
	mux.HandleFunc("POST /api/planner/recommendations/{id}/complete", s.completeRecommendation)
	mux.HandleFunc("DELETE /api/planner/recommendations/{id}", s.deleteRecommendation)
	mux.HandleFunc("GET /api/planner/export", s.plannerExport)
	mux.HandleFunc("POST /api/planner/import", s.plannerImport)

	// Plans
	mux.HandleFunc("GET /api/plans", s.listPlans)
	mux.HandleFunc("POST /api/plans", s.createPlan)
	mux.HandleFunc("GET /api/plans/{id}", s.getPlan)
	mux.HandleFunc("PUT /api/plans/{id}", s.updatePlan)
	mux.HandleFunc("DELETE /api/plans/{id}", s.deletePlan)

	// Executions
	mux.HandleFunc("GET /api/executions", s.listExecutions)
	mux.HandleFunc("GET /api/executions/{id}", s.getExecution)
	mux.HandleFunc("POST /api/executions/{id}/cancel", s.cancelExecution)

	return s.authMW(securityHeaders(s.guardOrigin(logging(s.log, mux))))
}

// securityHeaders sets baseline browser protections. The dashboard is a
// self-contained same-origin page (no external assets), so the CSP locks out
// any injected third-party content; inline script/style stay allowed because
// the whole UI lives in one HTML file.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; "+
				"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// guardOrigin rejects cross-origin state-changing requests when the server
// runs without a token: a malicious web page could otherwise fire simple
// POSTs (no preflight, no credentials needed) at localhost and start tasks.
// With a token configured the auth layer already rejects those requests, so
// the guard stays out of the way of reverse-proxy setups.
func (s *Server) guardOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authToken == "" && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				if u, err := url.Parse(origin); err != nil || u.Host == "" || u.Host != r.Host {
					writeErr(w, http.StatusForbidden, errors.New("cross-origin request rejected"))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// authMW protects /api/* and /screenshots/* with the configured token (if any).
// The dashboard page (/) and /healthz stay open so the page can load and prompt
// for a token. The token may arrive as `Authorization: Bearer <t>` or `?token=`
// (the query form lets the browser's EventSource, which cannot set headers,
// authenticate the live stream).
func (s *Server) authMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authToken == "" || !protected(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		tok := r.URL.Query().Get("token")
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			tok = strings.TrimPrefix(h, "Bearer ")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.authToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, errors.New("missing or invalid token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func protected(path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/screenshots/")
}

// ---------- games ----------

func (s *Server) listGames(w http.ResponseWriter, r *http.Request) {
	games, err := s.store.ListGames()
	respond(w, games, err)
}

// gameIDPattern is the character set allowed for game ids. They surface in
// URLs, JS handler args and filenames; restricting them here closes injection
// into all three at the write boundary (the dashboard form already enforces
// the same pattern client-side).
var gameIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validGameID(id string) bool { return id != "" && len(id) <= 128 && gameIDPattern.MatchString(id) }

// requireGame writes a descriptive 400 when the referenced game does not exist
// (otherwise such inserts die on the FK constraint as an unhelpful 500).
func (s *Server) requireGame(w http.ResponseWriter, gameID string) bool {
	if _, err := s.store.GetGame(gameID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("game %q does not exist; create it first", gameID))
			return false
		}
		writeStoreErr(w, err)
		return false
	}
	return true
}

// requireTask is requireGame for task references (plans).
func (s *Server) requireTask(w http.ResponseWriter, taskID int64) bool {
	if _, err := s.store.GetTask(taskID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("task %d does not exist", taskID))
			return false
		}
		writeStoreErr(w, err)
		return false
	}
	return true
}

// urlSchemeOK accepts only empty or absolute http(s) URLs for operator-facing
// link fields (route source_url), blocking javascript:/data: style payloads.
func urlSchemeOK(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	s := strings.ToLower(u.Scheme)
	return s == "http" || s == "https"
}

func (s *Server) createGame(w http.ResponseWriter, r *http.Request) {
	req := createGameRequest{Game: store.Game{Enabled: true}}
	if !decode(w, r, &req) {
		return
	}
	g := req.Game
	if req.Enabled != nil {
		g.Enabled = *req.Enabled
	}
	if !validGameID(g.ID) {
		writeErr(w, http.StatusBadRequest, errors.New("game id must be 1-128 characters of [A-Za-z0-9_-]"))
		return
	}
	if _, err := s.reg.Get(g.Adapter); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.store.CreateGame(g)
	respondCreated(w, out, s.changed(err))
}

func (s *Server) getGame(w http.ResponseWriter, r *http.Request) {
	g, err := s.store.GetGame(r.PathValue("id"))
	respond(w, g, err)
}

func (s *Server) updateGame(w http.ResponseWriter, r *http.Request) {
	var g store.Game
	if !decode(w, r, &g) {
		return
	}
	g.ID = r.PathValue("id")
	if !validGameID(g.ID) {
		writeErr(w, http.StatusBadRequest, errors.New("game id must be 1-128 characters of [A-Za-z0-9_-]"))
		return
	}
	if _, err := s.reg.Get(g.Adapter); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.store.UpdateGame(g)
	respond(w, out, s.changed(err))
}

func (s *Server) deleteGame(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Kill the tools' process trees before the cascade removes their rows, or
	// a running tool would keep controlling the game with nothing left to
	// record its result.
	if tasks, err := s.store.ListTasks(id); err == nil {
		for _, t := range tasks {
			s.svc.CancelTaskExecs(t.ID)
		}
	}
	err := s.store.DeleteGame(id)
	respondNoContent(w, s.changed(err))
}

// ---------- tasks ----------

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.ListTasks(r.URL.Query().Get("game_id"))
	respond(w, tasks, err)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	req := createTaskRequest{Task: store.Task{Enabled: true}}
	if !decode(w, r, &req) {
		return
	}
	t := req.Task
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if !validTaskFields(w, t) {
		return
	}
	if !s.validTaskType(w, t) {
		return
	}
	out, err := s.store.CreateTask(t)
	respondCreated(w, out, s.changed(err))
}

// createGameRequest shadows Enabled with a pointer so a missing field can be
// told apart from an explicit false: creating without `enabled` must produce
// an enabled row (matching the DB column default), not a silent no-op.
type createGameRequest struct {
	store.Game
	Enabled *bool `json:"enabled,omitempty"`
}

type createTaskRequest struct {
	store.Task
	Enabled *bool `json:"enabled,omitempty"`
}

// validTaskType checks the game's adapter actually accepts the task type, so
// a typo fails at creation instead of at fire time (or never, for a task only
// a plan would have run).
func (s *Server) validTaskType(w http.ResponseWriter, t store.Task) bool {
	g, err := s.store.GetGame(t.GameID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("game %q does not exist; create it first", t.GameID))
			return false
		}
		writeStoreErr(w, err)
		return false
	}
	// NC6 (draft decision D4): type "native" marks a native-controller
	// task. No adapter owns it — the dispatch keys off params
	// "executor":"native" and validates the real prerequisites itself, so
	// the adapter TaskTypes check does not apply.
	if t.Type == "native" {
		return true
	}
	ad, err := s.reg.Get(g.Adapter)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	if !slices.Contains(ad.TaskTypes(), t.Type) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("adapter %q does not support task type %q (supported: %v)", g.Adapter, t.Type, ad.TaskTypes()))
		return false
	}
	return true
}

// validTaskFields rejects negative retry/timeout settings: a negative
// max_retries would zero out the attempt loop entirely (the task would be
// recorded as a success without ever running), and a negative timeout would
// silently disable the timeout.
func validTaskFields(w http.ResponseWriter, t store.Task) bool {
	if t.MaxRetries < 0 || t.RetryDelaySec < 0 || t.TimeoutSec < 0 {
		writeErr(w, http.StatusBadRequest, errors.New("max_retries, retry_delay_sec and timeout_sec must be >= 0"))
		return false
	}
	return true
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := s.store.GetTask(id)
	respond(w, t, err)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var t store.Task
	if !decode(w, r, &t) {
		return
	}
	if !validTaskFields(w, t) {
		return
	}
	// validTaskType also verifies the game exists (a task cannot outlive its
	// game anyway), so no separate requireGame pass is needed here.
	if !s.validTaskType(w, t) {
		return
	}
	t.ID = id
	out, err := s.store.UpdateTask(t)
	respond(w, out, s.changed(err))
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// Same as deleteGame: stop any live run before its rows are cascaded away.
	s.svc.CancelTaskExecs(id)
	respondNoContent(w, s.changed(s.store.DeleteTask(id)))
}

func (s *Server) runTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// Manual triggers do not skip when active: an operator asking to run is an
	// explicit intent, so the run is queued behind any in-flight execution.
	exec, _, err := s.svc.Enqueue(id, store.TriggerManual, nil, false)
	respondCreated(w, exec, err)
}

func (s *Server) preflightTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	pf, err := s.svc.Preflight(id)
	respond(w, pf, err)
}

// ---------- routes ----------

func (s *Server) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := s.store.SearchRoutes(store.RouteFilter{
		GameID:    r.URL.Query().Get("game_id"),
		RouteType: r.URL.Query().Get("type"),
		Tag:       r.URL.Query().Get("tag"),
		Query:     r.URL.Query().Get("q"),
	})
	respond(w, routes, err)
}

func (s *Server) createRoute(w http.ResponseWriter, r *http.Request) {
	var rt store.Route
	if !decode(w, r, &rt) {
		return
	}
	if !s.requireGame(w, rt.GameID) {
		return
	}
	if !urlSchemeOK(rt.SourceURL) {
		writeErr(w, http.StatusBadRequest, errors.New("source_url must be an absolute http(s) URL"))
		return
	}
	s.prepareRoute(&rt)
	out, err := s.store.CreateRoute(rt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		writeErr(w, http.StatusConflict, errors.New("a route with this game_id and file_path already exists"))
		return
	}
	respondCreated(w, out, s.changed(err))
}

func (s *Server) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	respondNoContent(w, s.changed(s.store.DeleteRoute(id)))
}

// ---------- plans ----------

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.ListPlans(false)
	respond(w, plans, err)
}

func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	var p store.Plan
	if !decode(w, r, &p) {
		return
	}
	if err := scheduler.ValidateCron(p.CronExpr); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !s.requireTask(w, p.TaskID) {
		return
	}
	out, err := s.store.CreatePlan(p)
	if err == nil {
		_ = s.sched.Reload()
		s.bus.Notify()
	}
	respondCreated(w, out, err)
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPlan(id)
	respond(w, p, err)
}

func (s *Server) updatePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var p store.Plan
	if !decode(w, r, &p) {
		return
	}
	if err := scheduler.ValidateCron(p.CronExpr); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !s.requireTask(w, p.TaskID) {
		return
	}
	p.ID = id
	out, err := s.store.UpdatePlan(p)
	if err == nil {
		_ = s.sched.Reload()
		s.bus.Notify()
	}
	respond(w, out, err)
}

func (s *Server) deletePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.store.DeletePlan(id)
	if err == nil {
		_ = s.sched.Reload()
		s.bus.Notify()
	}
	respondNoContent(w, err)
}

// ---------- executions ----------

func (s *Server) listExecutions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var f store.ExecutionFilter
	if v := q.Get("task_id"); v != "" {
		f.TaskID, _ = strconv.ParseInt(v, 10, 64)
	}
	f.Status = q.Get("status")
	if v := q.Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	// meta=1 skips the potentially large stdout/stderr columns - what list
	// views (history modal, dashboards) want; detail views use the default.
	if q.Get("meta") == "1" {
		execs, err := s.store.ListExecutionMetas(f)
		respond(w, execs, err)
		return
	}
	execs, err := s.store.ListExecutions(f)
	respond(w, execs, err)
}

func (s *Server) getExecution(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	e, err := s.store.GetExecution(id)
	respond(w, e, err)
}

func (s *Server) cancelExecution(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.svc.Cancel(id); err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "cancelling", "execution_id": id})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/index.html")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// ---------- helpers ----------

// changed notifies live subscribers when a mutation succeeded, then passes the
// error through unchanged for the normal response path.
func (s *Server) changed(err error) error {
	if err == nil {
		s.bus.Notify()
	}
	return err
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return 0, false
	}
	return id, true
}

// maxBodyBytes caps request bodies; all legitimate payloads here are tiny.
const maxBodyBytes = 1 << 20 // 1 MiB

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Errorf("request body exceeds %d bytes", mbe.Limit))
		} else {
			writeErr(w, http.StatusBadRequest, err)
		}
		return false
	}
	return true
}

func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func respondCreated(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func respondNoContent(w http.ResponseWriter, err error) {
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func logging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer so Server-Sent Events (which require
// http.Flusher) keep working through this middleware.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
