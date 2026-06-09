package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/xiabee/game-scheduler/internal/discover"
)

// discoverScan scans the filesystem for known tool executables/directories.
// Body (all optional): {"paths":["F:/Games"],"max_depth":5}. With no paths it
// scans sensible defaults (fixed drives on Windows). Read-only.
func (s *Server) discoverScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths    []string `json:"paths"`
		MaxDepth int      `json:"max_depth"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	res := discover.Scan(r.Context(), discover.Options{Paths: req.Paths, MaxDepth: req.MaxDepth})
	writeJSON(w, http.StatusOK, res)
}
