package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// meta returns adapter metadata (keys + task types) for the dashboard's
// add-game / add-task forms.
func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"adapters": s.reg.Meta()})
}

// screenshot serves a failure screenshot from the screenshot directory. Only a
// bare filename is accepted (no path separators or "..") to prevent traversal.
func (s *Server) screenshot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		writeErr(w, http.StatusBadRequest, errors.New("invalid screenshot name"))
		return
	}
	http.ServeFile(w, r, filepath.Join(s.screenshotDir, filepath.Base(name)))
}

// stream pushes the dashboard to the client over Server-Sent Events: the full
// snapshot on connect, then a fresh snapshot whenever the event bus signals a
// change, plus a periodic heartbeat to keep proxies from idling the connection.
// SSE (vs WebSocket) fits here because the flow is purely server→client and the
// browser's EventSource reconnects automatically.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering

	ch, cancel := s.bus.Subscribe()
	defer cancel()
	ctx := r.Context()

	send := func() bool {
		d, err := s.buildDashboard()
		if err != nil {
			s.log.Warn("stream build", "err", err)
			return true
		}
		b, err := json.Marshal(d)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !send() {
		return
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case _, open := <-ch:
			if !open {
				return
			}
			if !send() {
				return
			}
		}
	}
}
