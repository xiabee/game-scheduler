// Package api exposes the scheduler over a small JSON REST interface built on
// net/http's pattern router (Go 1.22+).
package api

import (
	"embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/scheduler"
	"github.com/xiabee/game-scheduler/internal/store"
	"github.com/xiabee/game-scheduler/internal/task"
)

//go:embed web/index.html
var webFS embed.FS

// Server holds dependencies for the HTTP handlers.
type Server struct {
	store *store.Store
	svc   *task.Service
	sched *scheduler.Scheduler
	reg   *game.Registry
	log   *slog.Logger
}

// New builds an API server.
func New(s *store.Store, svc *task.Service, sched *scheduler.Scheduler, reg *game.Registry, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{store: s, svc: svc, sched: sched, reg: reg, log: log}
}

// Handler returns the configured HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "adapters": s.reg.Keys()})
	})

	// Control dashboard (single embedded page) + its aggregate feed.
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /api/dashboard", s.dashboard)

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
	mux.HandleFunc("DELETE /api/routes/{id}", s.deleteRoute)

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

	return logging(s.log, mux)
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
	respondCreated(w, out, err)
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
	respond(w, out, err)
}

func (s *Server) deleteGame(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteGame(r.PathValue("id"))
	respondNoContent(w, err)
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
	respondCreated(w, out, err)
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
	respond(w, out, err)
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	respondNoContent(w, s.store.DeleteTask(id))
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
	routes, err := s.store.ListRoutes(r.URL.Query().Get("game_id"))
	respond(w, routes, err)
}

func (s *Server) createRoute(w http.ResponseWriter, r *http.Request) {
	var rt store.Route
	if !decode(w, r, &rt) {
		return
	}
	out, err := s.store.CreateRoute(rt)
	respondCreated(w, out, err)
}

func (s *Server) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	respondNoContent(w, s.store.DeleteRoute(id))
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

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid id"))
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
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
