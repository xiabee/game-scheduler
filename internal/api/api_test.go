package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/game-scheduler/internal/config"
	"github.com/xiabee/game-scheduler/internal/events"
	"github.com/xiabee/game-scheduler/internal/game"
	"github.com/xiabee/game-scheduler/internal/game/genshin"
	"github.com/xiabee/game-scheduler/internal/guide"
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

type stubSearcher struct {
	vids []guide.Video
	err  error
}

func (s stubSearcher) Search(ctx context.Context, kw string, limit int) ([]guide.Video, error) {
	return s.vids, s.err
}

func TestGuidesSearch(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	// game with a local script library containing one matching route
	scriptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(scriptDir, "风车菊采集路线.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	ecJSON, _ := json.Marshal(map[string]string{"scripts_dir": scriptDir})
	st.CreateGame(store.Game{ID: "genshin", Name: "原神", Adapter: "genshin", ToolPath: "x", ExtraConfig: string(ecJSON), Enabled: true})

	bus := events.New()
	reg := game.NewRegistry(genshin.New())
	cfg := config.Config{DataDir: t.TempDir(), MaxConcurrent: 1}
	svc := task.NewService(st, reg, cfg, bus, nil)
	apiSrv := New(st, svc, scheduler.New(st, svc, nil), reg, bus, nil, cfg, nil)
	apiSrv.SetGuideSearcher(stubSearcher{vids: []guide.Video{{Title: "测试视频", BVID: "BV1", URL: "https://www.bilibili.com/video/BV1"}}})
	srv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/guides/search?game_id=genshin&q=" + url.QueryEscape("风车菊"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Videos      []guide.Video      `json:"videos"`
		LocalRoutes []guide.LocalRoute `json:"local_routes"`
		VideosError string             `json:"videos_error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Videos) != 1 || out.Videos[0].Title != "测试视频" {
		t.Errorf("videos=%+v", out.Videos)
	}
	if len(out.LocalRoutes) != 1 || out.LocalRoutes[0].Name != "风车菊采集路线" {
		t.Errorf("local_routes=%+v", out.LocalRoutes)
	}

	// missing q -> 400
	r2, _ := srv.Client().Get(srv.URL + "/api/guides/search")
	r2.Body.Close()
	if r2.StatusCode != http.StatusBadRequest {
		t.Errorf("missing q status=%d want 400", r2.StatusCode)
	}

	// video source failure surfaces in videos_error, local results still work
	apiSrv.SetGuideSearcher(stubSearcher{err: errors.New("风控")})
	r3, err := srv.Client().Get(srv.URL + "/api/guides/search?game_id=genshin&q=" + url.QueryEscape("风车菊"))
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Body.Close()
	var out3 struct {
		VideosError string             `json:"videos_error"`
		LocalRoutes []guide.LocalRoute `json:"local_routes"`
	}
	json.NewDecoder(r3.Body).Decode(&out3)
	if out3.VideosError == "" || len(out3.LocalRoutes) != 1 {
		t.Errorf("partial failure handling: err=%q routes=%d", out3.VideosError, len(out3.LocalRoutes))
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

func TestRoutesAssetCenterAPI(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	scriptDir := t.TempDir()
	routePath := filepath.Join(scriptDir, "蒙德", "风车菊采集路线.json")
	if err := os.MkdirAll(filepath.Dir(routePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(routePath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	ecJSON, _ := json.Marshal(map[string]string{"scripts_dir": scriptDir})
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "原神", Adapter: "genshin", ToolPath: "BetterGI.exe", ExtraConfig: string(ecJSON), Enabled: true}); err != nil {
		t.Fatal(err)
	}

	resp, err := srv.Client().Post(srv.URL+"/api/routes/scan", "application/json", strings.NewReader(`{"game_id":"genshin"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var scan struct {
		Scanned int           `json:"scanned"`
		Created int           `json:"created"`
		Routes  []store.Route `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&scan); err != nil {
		t.Fatal(err)
	}
	if scan.Scanned != 1 || scan.Created != 1 || len(scan.Routes) != 1 {
		t.Fatalf("scan=%+v", scan)
	}
	if scan.Routes[0].RouteType != "collect" || len(scan.Routes[0].Tags) == 0 {
		t.Fatalf("route enrichment failed: %+v", scan.Routes[0])
	}

	searchURL := srv.URL + "/api/routes/search?game_id=genshin&q=" + url.QueryEscape("蒙德") + "&type=collect&tag=蒙德"
	resp, err = srv.Client().Get(searchURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var routes []store.Route
	if err := json.NewDecoder(resp.Body).Decode(&routes); err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].ID != scan.Routes[0].ID {
		t.Fatalf("search routes=%+v", routes)
	}

	route := routes[0]
	route.SourceURL = "https://example.com/guide"
	route.SourceTitle = "攻略标题"
	route.Tags = append(route.Tags, "manual")
	body, _ := json.Marshal(route)
	req, _ := http.NewRequest("PUT", srv.URL+"/api/routes/"+strconv.FormatInt(route.ID, 10), strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err = srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status=%d", resp.StatusCode)
	}

	resp, err = srv.Client().Post(srv.URL+"/api/routes/"+strconv.FormatInt(route.ID, 10)+"/create-task", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var task store.Task
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		t.Fatal(err)
	}
	var params map[string]string
	if err := json.Unmarshal([]byte(task.Params), &params); err != nil {
		t.Fatal(err)
	}
	if task.RouteID == nil || *task.RouteID != route.ID || task.Type != "script" || filepath.Clean(params["script"]) != filepath.Clean(routePath) {
		t.Fatalf("created task=%+v", task)
	}
}
