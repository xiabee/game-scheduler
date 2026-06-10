package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/xiabee/game-scheduler/internal/guide"
	"github.com/xiabee/game-scheduler/internal/store"
)

// guidesResponse aggregates both guide sources. Source failures are reported
// per-field so a Bilibili hiccup doesn't hide local matches (and vice versa).
type guidesResponse struct {
	Keyword     string             `json:"keyword"`
	Videos      []guide.Video      `json:"videos"`
	VideosError string             `json:"videos_error,omitempty"`
	LocalRoutes []guide.LocalRoute `json:"local_routes"`
	LocalRoots  []string           `json:"local_roots"`
}

// guidesSearch handles GET /api/guides/search?q=&game_id=&source=all|video|local.
// Videos come from Bilibili's public search (read-only, no credentials); local
// routes are filename matches inside the game's configured script libraries
// (extra_config keys: scripts_dir / fhoe_dir / march7th_dir).
func (s *Server) guidesSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, errors.New("missing query parameter q"))
		return
	}
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "all"
	}
	resp := guidesResponse{Keyword: q, Videos: []guide.Video{}, LocalRoutes: []guide.LocalRoute{}, LocalRoots: []string{}}

	if gameID := r.URL.Query().Get("game_id"); gameID != "" {
		if g, err := s.store.GetGame(gameID); err == nil {
			resp.LocalRoots = scriptRoots(g)
		} else if !errors.Is(err, store.ErrNotFound) {
			writeStoreErr(w, err)
			return
		}
	}

	if source == "all" || source == "video" {
		if s.guides == nil {
			resp.VideosError = "video search disabled"
		} else if vids, err := s.guides.Search(r.Context(), q, 15); err != nil {
			resp.VideosError = err.Error()
		} else {
			resp.Videos = vids
		}
	}
	if (source == "all" || source == "local") && len(resp.LocalRoots) > 0 {
		resp.LocalRoutes = guide.ScanLocalRoutes(resp.LocalRoots, q, 50)
	}
	writeJSON(w, http.StatusOK, resp)
}

// scriptRoots extracts the local script-library directories from a game's
// configuration.
func scriptRoots(g store.Game) []string {
	roots := []string{}
	ec, err := g.ExtraConfigMap()
	if err != nil {
		return roots
	}
	for _, key := range []string{"scripts_dir", "fhoe_dir", "march7th_dir"} {
		if v, ok := ec[key].(string); ok && strings.TrimSpace(v) != "" {
			roots = append(roots, v)
		}
	}
	return roots
}
