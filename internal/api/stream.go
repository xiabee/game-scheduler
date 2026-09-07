package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xiabee/game-scheduler/internal/version"
)

// meta returns adapter metadata (keys + task types) for the dashboard's
// add-game / add-task forms, plus the server build version.
func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"adapters": s.reg.Meta(), "version": version.Version})
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

// streamHub broadcasts ONE shared dashboard snapshot per change signal to all
// SSE subscribers. The store is single-connection, so rebuilding per client
// would serialize N tabs into N× the queries on every event; with the hub the
// cost is one build no matter how many dashboards are watching.
type streamHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newStreamHub() *streamHub {
	return &streamHub{clients: map[chan []byte]struct{}{}}
}

func (h *streamHub) add() chan []byte {
	ch := make(chan []byte, 1)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *streamHub) remove(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

// broadcast hands the snapshot to every client without blocking: a client that
// already has a pending snapshot keeps it (coalesced), a slow one simply
// catches up on the next event.
func (h *streamHub) broadcast(snapshot []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- snapshot:
		default:
		}
	}
}

// runStreamHub is the hub loop: it subscribes to the event bus, rebuilds the
// dashboard once per coalesced signal and fans the snapshot out. It exits when
// the server's stream shutdown fires; the bus registration is released via the
// defer, so no goroutine or subscription leaks.
func (s *Server) runStreamHub() {
	ch, cancel := s.bus.Subscribe()
	defer cancel()
	for {
		select {
		case <-s.streamsClosingCh:
			return
		case <-ch:
			d, err := s.buildDashboard()
			if err != nil {
				s.log.Warn("stream build", "err", err)
				continue
			}
			b, err := json.Marshal(d)
			if err != nil {
				continue
			}
			s.hub.broadcast(b)
		}
	}
}

// startStreamHub launches the hub loop once. Safe to call repeatedly (the
// dashboard page and tests both connect through it).
func (s *Server) startStreamHub() {
	s.hubStart.Do(func() {
		go s.runStreamHub()
	})
}

// stream pushes the dashboard to the client over Server-Sent Events: the full
// snapshot on connect, then a shared snapshot whenever the event bus signals a
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
	s.startStreamHub()
	ch := s.hub.add()
	defer s.hub.remove(ch)
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.streamsClosingCh:
			// Server shutdown: release the connection right away so
			// http.Server.Shutdown is not held up by idle event streams.
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case b, open := <-ch:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
