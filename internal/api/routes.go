package api

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xiabee/game-scheduler/internal/guide"
	"github.com/xiabee/game-scheduler/internal/store"
	"github.com/xiabee/game-scheduler/internal/taskfactory"
)

type routeScanRequest struct {
	GameID string `json:"game_id"`
	Limit  int    `json:"limit"`
}

type routeScanResponse struct {
	Scanned int           `json:"scanned"`
	Created int           `json:"created"`
	Updated int           `json:"updated"`
	Routes  []store.Route `json:"routes"`
}

func (s *Server) searchRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := s.store.SearchRoutes(store.RouteFilter{
		GameID:    r.URL.Query().Get("game_id"),
		RouteType: r.URL.Query().Get("type"),
		Tag:       r.URL.Query().Get("tag"),
		Query:     r.URL.Query().Get("q"),
		Limit:     intQuery(r, "limit"),
	})
	respond(w, routes, err)
}

func (s *Server) updateRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var rt store.Route
	if !decode(w, r, &rt) {
		return
	}
	rt.ID = id
	s.prepareRoute(&rt)
	out, err := s.store.UpdateRoute(rt)
	respond(w, out, s.changed(err))
}

func (s *Server) scanRoutes(w http.ResponseWriter, r *http.Request) {
	req := routeScanRequest{Limit: 500}
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	games, err := s.store.ListGames()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	resp := routeScanResponse{Routes: []store.Route{}}
	for _, g := range games {
		if req.GameID != "" && g.ID != req.GameID {
			continue
		}
		for _, lr := range guide.ScanRouteAssets(scriptRoots(g), req.Limit) {
			resp.Scanned++
			rt := store.Route{
				GameID:      g.ID,
				Adapter:     g.Adapter,
				RouteType:   lr.RouteType,
				Tags:        lr.Tags,
				Name:        lr.Name,
				FilePath:    lr.Path,
				Description: "scanned from " + lr.Root,
			}
			out, created, err := s.store.UpsertRouteByFile(rt)
			if err != nil {
				writeStoreErr(w, err)
				return
			}
			if created {
				resp.Created++
			} else {
				resp.Updated++
			}
			resp.Routes = append(resp.Routes, out)
		}
	}
	s.bus.Notify()
	writeJSON(w, http.StatusOK, resp)
}

type createTaskFromRouteRequest struct {
	Name          string `json:"name"`
	TimeoutSec    int    `json:"timeout_sec"`
	MaxRetries    int    `json:"max_retries"`
	RetryDelaySec int    `json:"retry_delay_sec"`
	Enabled       *bool  `json:"enabled,omitempty"`
}

func (s *Server) createTaskFromRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req createTaskFromRouteRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	rt, err := s.store.GetRoute(id)
	if err != nil {
		respond(w, store.Task{}, err)
		return
	}
	g, err := s.store.GetGame(rt.GameID)
	if err != nil {
		respond(w, store.Task{}, err)
		return
	}
	t, err := taskfactory.FromRoute(g, rt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Name != "" {
		t.Name = req.Name
	}
	if req.TimeoutSec > 0 {
		t.TimeoutSec = req.TimeoutSec
	}
	if req.MaxRetries > 0 {
		t.MaxRetries = req.MaxRetries
	}
	if req.RetryDelaySec > 0 {
		t.RetryDelaySec = req.RetryDelaySec
	}
	t.Enabled = true
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	out, err := s.store.CreateTask(t)
	respondCreated(w, out, s.changed(err))
}

func (s *Server) prepareRoute(rt *store.Route) {
	if rt.Adapter == "" && rt.GameID != "" {
		if g, err := s.store.GetGame(rt.GameID); err == nil {
			rt.Adapter = g.Adapter
		}
	}
	if rt.RouteType == "" {
		rt.RouteType = guide.GuessRouteType(rt.Name + " " + rt.FilePath + " " + strings.Join(rt.Tags, " "))
	}
	if rt.RouteType == "" {
		rt.RouteType = "other"
	}
	if len(rt.Tags) == 0 && rt.FilePath != "" {
		rt.Tags = guide.BuildLocalRoute(filepath.Dir(rt.FilePath), rt.FilePath).Tags
	}
}

func intQuery(r *http.Request, key string) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

func int64Query(r *http.Request, key string) int64 {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return 0
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}
