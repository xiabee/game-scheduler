package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/events"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/game/genshin"
	"github.com/xiabee/game-scheduler/internal/scheduler"
	"github.com/xiabee/game-scheduler/internal/store"
	"github.com/xiabee/game-scheduler/internal/task"
)

func newTestServer(t *testing.T, token string) (*httptest.Server, *store.Store, *events.Bus) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.New()
	reg := game.NewRegistry(genshin.New())
	cfg := config.Config{DataDir: t.TempDir(), AuthToken: token, MaxConcurrent: 1}
	svc := task.NewService(st, reg, cfg, bus, nil)
	sched := scheduler.New(st, svc, nil)
	srv := httptest.NewServer(New(st, svc, sched, reg, bus, nil, cfg, nil).Handler())
	t.Cleanup(srv.Close)
	return srv, st, bus
}

func TestAuthMiddleware(t *testing.T) {
	srv, _, _ := newTestServer(t, "sekret")
	c := srv.Client()

	cases := []struct {
		name, path, header, query string
		want                      int
	}{
		{"healthz open", "/healthz", "", "", 200},
		{"page open", "/", "", "", 200},
		{"api no token", "/api/dashboard", "", "", 401},
		{"api bad token", "/api/dashboard", "Bearer nope", "", 401},
		{"api header", "/api/dashboard", "Bearer sekret", "", 200},
		{"api query", "/api/dashboard", "", "token=sekret", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := srv.URL + tc.path
			if tc.query != "" {
				u += "?" + tc.query
			}
			req, _ := http.NewRequest("GET", u, nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			resp, err := c.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("status=%d want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestDashboardJSON(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	st.CreateGame(store.Game{ID: "genshin", Name: "原神", Adapter: "genshin", ToolPath: "x", Enabled: true})

	resp, err := srv.Client().Get(srv.URL + "/api/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var d dashboard
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Totals.Games != 1 || len(d.Games) != 1 {
		t.Errorf("got totals=%+v games=%d", d.Totals, len(d.Games))
	}
	if d.Games[0].Health != "idle" {
		t.Errorf("health=%q want idle", d.Games[0].Health)
	}
}

// TestStreamSSE guards the regression where the logging middleware's
// statusWriter did not implement http.Flusher, breaking SSE entirely.
func TestStreamSSE(t *testing.T) {
	srv, st, bus := newTestServer(t, "")
	st.CreateGame(store.Game{ID: "genshin", Name: "原神", Adapter: "genshin", Enabled: true})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/stream", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type=%q", ct)
	}

	reader := bufio.NewReader(resp.Body)
	// Initial snapshot frame.
	if line := readData(t, reader); !strings.Contains(line, `"totals"`) {
		t.Fatalf("initial frame missing totals: %q", line)
	}
	// A change should push another frame.
	go func() { time.Sleep(100 * time.Millisecond); bus.Notify() }()
	if line := readData(t, reader); !strings.Contains(line, `"generated_at"`) {
		t.Fatalf("push frame missing payload: %q", line)
	}
}

func readData(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatal("no data frame within deadline")
	return ""
}

func TestGameCRUDOverHTTP(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	c := srv.Client()

	// create
	body := `{"id":"genshin","name":"原神","adapter":"genshin","tool_path":"x","enabled":true}`
	resp, err := c.Post(srv.URL+"/api/games", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", resp.StatusCode)
	}

	// unknown adapter rejected
	resp, err = c.Post(srv.URL+"/api/games", "application/json",
		strings.NewReader(`{"id":"x","name":"x","adapter":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown adapter status=%d want 400", resp.StatusCode)
	}

	// list returns [] form, not null, when empty collection (tasks)
	resp, err = c.Get(srv.URL + "/api/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tasks []store.Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		t.Fatalf("tasks should decode as array: %v", err)
	}
}

func TestMetaEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	resp, err := srv.Client().Get(srv.URL + "/api/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m struct {
		Adapters []game.AdapterInfo `json:"adapters"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if len(m.Adapters) != 1 || m.Adapters[0].Key != "genshin" {
		t.Errorf("adapters=%+v", m.Adapters)
	}
}

func TestScreenshotTraversalBlocked(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	resp, err := srv.Client().Get(srv.URL + "/screenshots/" + "..%2f..%2fsecret")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("path traversal should not succeed")
	}
}
