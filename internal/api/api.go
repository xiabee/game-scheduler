// Package api exposes the scheduler over a small JSON REST interface built on
// net/http's pattern router (Go 1.22+).
package api

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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
}

// SetGuideSearcher overrides the Bilibili search client (tests inject a stub).
func (s *Server) SetGuideSearcher(g guide.Searcher) { s.guides = g }

// New builds an API server. mon may be nil (no resource panel).
func New(s *store.Store, svc *task.Service, sched *scheduler.Scheduler, reg *game.Registry, bus *events.Bus, mon *monitor.Monitor, cfg config.Config, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		store:         s,
		svc:           svc,
		sched:         sched,
		reg:           reg,
		bus:           bus,
		mon:           mon,
		guides:        guide.NewClient(),
		log:           log,
		screenshotDir: cfg.ScreenshotDir(),
		authToken:     cfg.AuthToken,
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
	mux.HandleFunc("POST /api/planner/recommendations/{id}/create-plan", s.createPlanFromRecommendation)
	mux.HandleFunc("POST /api/planner/recommendations/{id}/dismiss", s.dismissRecommendation)
	mux.HandleFunc("POST /api/planner/recommendations/{id}/complete", s.completeRecommendation)

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

	return s.authMW(logging(s.log, mux))
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

func (s *Server) createGame(w http.ResponseWriter, r *http.Request) {
	var g store.Game
	if !decode(w, r, &g) {
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
	out, err := s.store.UpdateGame(g)
	respond(w, out, s.changed(err))
}

func (s *Server) deleteGame(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteGame(r.PathValue("id"))
	respondNoContent(w, s.changed(err))
}

// ---------- tasks ----------

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.ListTasks(r.URL.Query().Get("game_id"))
	respond(w, tasks, err)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var t store.Task
	if !decode(w, r, &t) {
		return
	}
	out, err := s.store.CreateTask(t)
	respondCreated(w, out, s.changed(err))
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
	t.ID = id
	out, err := s.store.UpdateTask(t)
	respond(w, out, s.changed(err))
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
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
	s.prepareRoute(&rt)
	out, err := s.store.CreateRoute(rt)
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
		writeErr(w, http.StatusBadRequest, err)
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
