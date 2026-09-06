package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	bus := events.New()
	reg := game.NewRegistry(genshin.New())
	cfg := config.Config{DataDir: t.TempDir(), AuthToken: token, MaxConcurrent: 1}
	svc := task.NewService(st, reg, cfg, bus, nil)
	sched := scheduler.New(st, svc, nil)
	srv := httptest.NewServer(New(st, svc, sched, reg, bus, nil, cfg, nil).Handler())
	// Order matters: stop HTTP, drain in-flight task workers, then close the DB.
	t.Cleanup(func() {
		srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Shutdown(ctx)
		st.Close()
	})
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

func TestGetRouteByID(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "原神", Adapter: "genshin", ToolPath: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rt, err := st.CreateRoute(store.Route{GameID: "genshin", Adapter: "genshin", RouteType: "collect", Name: "r", FilePath: "D:/routes/r.json"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Get(srv.URL + "/api/routes/" + strconv.FormatInt(rt.ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get route status=%d", resp.StatusCode)
	}
	var out store.Route
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.ID != rt.ID || out.Name != "r" {
		t.Fatalf("route=%+v", out)
	}
	// unknown id maps to 404
	resp2, err := srv.Client().Get(srv.URL + "/api/routes/424242")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown route status=%d", resp2.StatusCode)
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

func TestCharacterPlannerAPI(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "原神", Adapter: "genshin", ToolPath: "BetterGI.exe", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	c := srv.Client()
	post := func(path, body string, out any) int {
		t.Helper()
		resp, err := c.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode
	}

	var ch store.Character
	if st := post("/api/characters", `{"game_id":"genshin","name":"香菱","role_type":"sub_dps","tags":["pyro"]}`, &ch); st != http.StatusCreated {
		t.Fatalf("character status=%d", st)
	}
	var goal store.CharacterGoal
	if st := post("/api/character-goals", `{"character_id":`+strconv.FormatInt(ch.ID, 10)+`,"name":"突破90","priority":5}`, &goal); st != http.StatusCreated {
		t.Fatalf("goal status=%d", st)
	}
	var mat store.MaterialItem
	if st := post("/api/materials", `{"game_id":"genshin","name":"绝云椒椒","category":"collect","source_hint":"绝云","route_type_hint":"collect"}`, &mat); st != http.StatusCreated {
		t.Fatalf("material status=%d", st)
	}
	var req store.MaterialRequirement
	if st := post("/api/material-requirements", `{"goal_id":`+strconv.FormatInt(goal.ID, 10)+`,"material_id":`+strconv.FormatInt(mat.ID, 10)+`,"required_count":10,"owned_count":2,"priority":8}`, &req); st != http.StatusCreated {
		t.Fatalf("requirement status=%d", st)
	}
	rt, err := st.CreateRoute(store.Route{GameID: "genshin", Adapter: "genshin", RouteType: "collect", Tags: []string{"绝云"}, Name: "绝云椒椒采集", FilePath: "D:/routes/jueyun.json"})
	if err != nil {
		t.Fatal(err)
	}

	var recs []store.FarmingRecommendation
	if code := post("/api/planner/recommend", `{"goal_id":`+strconv.FormatInt(goal.ID, 10)+`,"max_tasks":3}`, &recs); code != http.StatusCreated {
		t.Fatalf("recommend status=%d", code)
	}
	if len(recs) != 1 || recs[0].RouteID == nil || *recs[0].RouteID != rt.ID {
		t.Fatalf("recommendations=%+v", recs)
	}
	var task store.Task
	if code := post("/api/planner/recommendations/"+strconv.FormatInt(recs[0].ID, 10)+"/create-task", `{}`, &task); code != http.StatusCreated {
		t.Fatalf("create task status=%d", code)
	}
	if task.RouteID == nil || *task.RouteID != rt.ID {
		t.Fatalf("task=%+v", task)
	}
	var plan store.Plan
	if code := post("/api/planner/recommendations/"+strconv.FormatInt(recs[0].ID, 10)+"/create-plan", `{"cron_expr":"0 9 * * *"}`, &plan); code != http.StatusCreated {
		t.Fatalf("create plan status=%d", code)
	}
	if plan.TaskID != task.ID {
		t.Fatalf("plan=%+v task=%+v", plan, task)
	}
	resp, err := c.Get(srv.URL + "/api/planner/recommendations?goal_id=" + strconv.FormatInt(goal.ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var listed []store.FarmingRecommendation
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].TaskID == nil || listed[0].Status != "planned" {
		t.Fatalf("listed=%+v", listed)
	}
}

func TestRecommendationManualCreateTaskError(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "原神", Adapter: "genshin", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ch, _ := st.CreateCharacter(store.Character{GameID: "genshin", Name: "角色"})
	goal, _ := st.CreateCharacterGoal(store.CharacterGoal{CharacterID: ch.ID, Name: "目标"})
	mat, _ := st.CreateMaterialItem(store.MaterialItem{GameID: "genshin", Name: "未知材料"})
	rec, _ := st.CreateFarmingRecommendation(store.FarmingRecommendation{GoalID: goal.ID, GameID: "genshin", MaterialID: mat.ID, Title: "手动", Reason: "无路线"})
	resp, err := srv.Client().Post(srv.URL+"/api/planner/recommendations/"+strconv.FormatInt(rec.ID, 10)+"/create-task", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create-task status=%d want 400", resp.StatusCode)
	}

	// create-plan on a recommendation with neither route nor task must also 400
	// (and must not create a dangling plan).
	resp2, err := srv.Client().Post(srv.URL+"/api/planner/recommendations/"+strconv.FormatInt(rec.ID, 10)+"/create-plan", "application/json", strings.NewReader(`{"cron_expr":"0 9 * * *"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("create-plan status=%d want 400", resp2.StatusCode)
	}
	if plans, _ := st.ListPlans(false); len(plans) != 0 {
		t.Fatalf("no plan should be created on failure, got %+v", plans)
	}
}

func TestPlannerAttachRoute(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	for _, gid := range []string{"genshin", "hsr"} {
		if _, err := st.CreateGame(store.Game{ID: gid, Name: gid, Adapter: "genshin", ToolPath: "x", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	c := srv.Client()
	post := func(path, body string, out any) int {
		t.Helper()
		resp, err := c.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode
	}

	ch, err := st.CreateCharacter(store.Character{GameID: "genshin", Name: "香菱"})
	if err != nil {
		t.Fatal(err)
	}
	goal, err := st.CreateCharacterGoal(store.CharacterGoal{CharacterID: ch.ID, Name: "突破90", Priority: 5})
	if err != nil {
		t.Fatal(err)
	}
	mat, err := st.CreateMaterialItem(store.MaterialItem{GameID: "genshin", Name: "绝云椒椒", Category: "collect"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMaterialRequirement(store.MaterialRequirement{GoalID: goal.ID, MaterialID: mat.ID, RequiredCount: 10, OwnedCount: 2, Priority: 8}); err != nil {
		t.Fatal(err)
	}
	// no routes yet: the recommendation comes out as manual, without a route
	var recs []store.FarmingRecommendation
	if code := post("/api/planner/recommend", `{"goal_id":`+strconv.FormatInt(goal.ID, 10)+`,"max_tasks":3}`, &recs); code != http.StatusCreated {
		t.Fatalf("recommend status=%d", code)
	}
	if len(recs) != 1 || recs[0].RouteID != nil || recs[0].RecommendationType != "manual" {
		t.Fatalf("recommendations=%+v", recs)
	}
	recID := strconv.FormatInt(recs[0].ID, 10)

	// create-task without a route fails with a clear 400
	var e struct {
		Error string `json:"error"`
	}
	if code := post("/api/planner/recommendations/"+recID+"/create-task", `{}`, &e); code != http.StatusBadRequest || e.Error == "" {
		t.Fatalf("create-task without route: status=%d err=%q", code, e.Error)
	}

	rtG, err := st.CreateRoute(store.Route{GameID: "genshin", Adapter: "genshin", RouteType: "collect", Name: "绝云椒椒采集", FilePath: "D:/routes/jueyun.json"})
	if err != nil {
		t.Fatal(err)
	}
	rtH, err := st.CreateRoute(store.Route{GameID: "hsr", Adapter: "genshin", RouteType: "collect", Name: "hsr路线", FilePath: "D:/routes/h.json"})
	if err != nil {
		t.Fatal(err)
	}

	// route of another game is rejected
	if code := post("/api/planner/recommendations/"+recID+"/attach-route", `{"route_id":`+strconv.FormatInt(rtH.ID, 10)+`}`, &e); code != http.StatusBadRequest || e.Error == "" {
		t.Fatalf("cross-game attach: status=%d err=%q", code, e.Error)
	}
	// missing route_id is rejected
	if code := post("/api/planner/recommendations/"+recID+"/attach-route", `{}`, &e); code != http.StatusBadRequest {
		t.Fatalf("attach without route_id: status=%d", code)
	}
	// unknown route is 404
	if code := post("/api/planner/recommendations/"+recID+"/attach-route", `{"route_id":424242}`, &e); code != http.StatusNotFound {
		t.Fatalf("attach unknown route: status=%d", code)
	}
	// attach succeeds and flips type to route
	var out store.FarmingRecommendation
	if code := post("/api/planner/recommendations/"+recID+"/attach-route", `{"route_id":`+strconv.FormatInt(rtG.ID, 10)+`}`, &out); code != http.StatusOK {
		t.Fatalf("attach status=%d", code)
	}
	if out.RouteID == nil || *out.RouteID != rtG.ID || out.RecommendationType != "route" {
		t.Fatalf("attach result=%+v", out)
	}

	// the recommendation can now produce a task bound to the attached route
	var task store.Task
	if code := post("/api/planner/recommendations/"+recID+"/create-task", `{}`, &task); code != http.StatusCreated {
		t.Fatalf("create-task after attach status=%d", code)
	}
	if task.RouteID == nil || *task.RouteID != rtG.ID {
		t.Fatalf("task=%+v", task)
	}

	// unknown recommendation is 404
	if code := post("/api/planner/recommendations/424242/attach-route", `{"route_id":`+strconv.FormatInt(rtG.ID, 10)+`}`, &e); code != http.StatusNotFound {
		t.Fatalf("attach unknown recommendation: status=%d", code)
	}
}

func TestPlannerExportImport(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	c := srv.Client()
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "原神", Adapter: "genshin", ToolPath: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	postJSON := func(path, body string, out any) int {
		t.Helper()
		resp, err := c.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode
	}

	importBody := `{"dry_run":%v,"upsert":true,"data":{
		"version":1,"game_id":"genshin",
		"characters":[{"id":7,"name":"香菱","role_type":"dps","tags":["pyro"]}],
		"character_goals":[{"id":70,"character_id":7,"name":"突破90","priority":5}],
		"material_items":[{"id":700,"name":"绝云椒椒","category":"collect","source_hint":"绝云间"}],
		"material_requirements":[{"goal_id":70,"material_id":700,"required_count":10,"owned_count":2}]}}`

	// 1) dry_run reports creations but writes nothing
	var dry plannerImportResult
	if code := postJSON("/api/planner/import", strings.Replace(importBody, "%v", "true", 1), &dry); code != http.StatusOK {
		t.Fatalf("dry_run status=%d", code)
	}
	if !dry.DryRun || dry.Created != 4 || dry.Updated != 0 || len(dry.Errors) != 0 {
		t.Fatalf("dry_run result=%+v", dry)
	}
	if chars, _ := st.ListCharacters(store.CharacterFilter{GameID: "genshin"}); len(chars) != 0 {
		t.Fatalf("dry_run must not write, found %d characters", len(chars))
	}

	// 2) real import creates everything with remapped ids
	var imp plannerImportResult
	if code := postJSON("/api/planner/import", strings.Replace(importBody, "%v", "false", 1), &imp); code != http.StatusOK {
		t.Fatalf("import status=%d", code)
	}
	if imp.Created != 4 || len(imp.Errors) != 0 {
		t.Fatalf("import result=%+v", imp)
	}
	chars, _ := st.ListCharacters(store.CharacterFilter{GameID: "genshin"})
	if len(chars) != 1 || chars[0].ID == 7 && chars[0].Name != "香菱" {
		t.Fatalf("characters=%+v", chars)
	}
	goals, _ := st.ListCharacterGoals(store.CharacterGoalFilter{GameID: "genshin"})
	if len(goals) != 1 || goals[0].CharacterID != chars[0].ID {
		t.Fatalf("goal character_id not remapped: goals=%+v chars=%+v", goals, chars)
	}
	mats, _ := st.ListMaterialItems(store.MaterialFilter{GameID: "genshin"})
	if len(mats) != 1 {
		t.Fatalf("materials=%+v", mats)
	}
	reqs, _ := st.ListMaterialRequirements(store.MaterialRequirementFilter{GoalID: goals[0].ID})
	if len(reqs) != 1 || reqs[0].MaterialID != mats[0].ID {
		t.Fatalf("requirement ids not remapped: %+v (material %d)", reqs, mats[0].ID)
	}

	// 3) re-import with upsert: updates, no duplicates
	var again plannerImportResult
	if code := postJSON("/api/planner/import", strings.Replace(importBody, "%v", "false", 1), &again); code != http.StatusOK {
		t.Fatalf("re-import status=%d", code)
	}
	if again.Created != 0 || again.Updated != 4 {
		t.Fatalf("re-import should update not duplicate: %+v", again)
	}
	if chars, _ := st.ListCharacters(store.CharacterFilter{GameID: "genshin"}); len(chars) != 1 {
		t.Fatalf("duplicated characters: %+v", chars)
	}
	if reqs, _ := st.ListMaterialRequirements(store.MaterialRequirementFilter{GoalID: goals[0].ID}); len(reqs) != 1 {
		t.Fatalf("duplicated requirements: %+v", reqs)
	}

	// 4) export round-trips the data
	resp, err := c.Get(srv.URL + "/api/planner/export?game_id=genshin")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var exp PlannerExport
	if err := json.NewDecoder(resp.Body).Decode(&exp); err != nil {
		t.Fatal(err)
	}
	if exp.Version != 1 || exp.GameID != "genshin" ||
		len(exp.Characters) != 1 || len(exp.Goals) != 1 || len(exp.Materials) != 1 || len(exp.Requirements) != 1 {
		t.Fatalf("export=%+v", exp)
	}

	// 5) clear validation errors
	cases := []struct {
		name, body string
	}{
		{"missing game_id", `{"data":{"version":1,"characters":[]}}`},
		{"unknown game", `{"data":{"version":1,"game_id":"nope"}}`},
		{"goal references unknown character", `{"data":{"version":1,"game_id":"genshin","characters":[{"id":1,"name":"a"}],"character_goals":[{"id":2,"character_id":99,"name":"g"}]}}`},
		{"character missing name", `{"data":{"version":1,"game_id":"genshin","characters":[{"id":1}]}}`},
		{"future version", `{"data":{"version":99,"game_id":"genshin"}}`},
		{"malformed json", `{"data":{`},
		{"duplicate character name in file", `{"data":{"version":1,"game_id":"genshin","characters":[{"id":1,"name":"a"},{"id":2,"name":" A"}]}}`},
		{"duplicate character file id", `{"data":{"version":1,"game_id":"genshin","characters":[{"id":1,"name":"a"},{"id":1,"name":"b"}]}}`},
		{"duplicate material name in file", `{"data":{"version":1,"game_id":"genshin","characters":[{"id":1,"name":"a"}],"material_items":[{"id":5,"name":"m"},{"id":6,"name":"m"}]}}`},
		{"duplicate goal for character in file", `{"data":{"version":1,"game_id":"genshin","characters":[{"id":1,"name":"a"}],"character_goals":[{"id":2,"character_id":1,"name":"g"},{"id":3,"character_id":1,"name":"G"}]}}`},
		{"duplicate requirement in file", `{"data":{"version":1,"game_id":"genshin","characters":[{"id":1,"name":"a"}],"character_goals":[{"id":2,"character_id":1,"name":"g"}],"material_items":[{"id":5,"name":"m"}],"material_requirements":[{"goal_id":2,"material_id":5},{"goal_id":2,"material_id":5}]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e struct {
				Error string `json:"error"`
			}
			if code := postJSON("/api/planner/import", tc.body, &e); code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400 (err=%q)", code, e.Error)
			}
			if e.Error == "" {
				t.Fatal("expected a clear error message")
			}
		})
	}

	// 6) export requires game_id / existing game
	r2, _ := c.Get(srv.URL + "/api/planner/export")
	r2.Body.Close()
	if r2.StatusCode != http.StatusBadRequest {
		t.Fatalf("export without game_id status=%d", r2.StatusCode)
	}
}

func TestPlannerListFilters(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	for _, gid := range []string{"genshin", "hsr"} {
		adapter := gid
		if _, err := st.CreateGame(store.Game{ID: gid, Name: gid, Adapter: adapter, ToolPath: "x", ExtraConfig: `{"march7th_dir":"C:/x"}`, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	chG, _ := st.CreateCharacter(store.Character{GameID: "genshin", Name: "甲"})
	chH, _ := st.CreateCharacter(store.Character{GameID: "hsr", Name: "乙"})
	st.CreateCharacterGoal(store.CharacterGoal{CharacterID: chG.ID, Name: "g-open"})                 // status defaults open
	st.CreateCharacterGoal(store.CharacterGoal{CharacterID: chG.ID, Name: "g-done", Status: "done"}) //
	st.CreateCharacterGoal(store.CharacterGoal{CharacterID: chH.ID, Name: "h-open"})                 //
	st.CreateMaterialItem(store.MaterialItem{GameID: "genshin", Name: "花", Category: "collect"})     //
	st.CreateMaterialItem(store.MaterialItem{GameID: "genshin", Name: "核", Category: "boss"})        //
	st.CreateMaterialItem(store.MaterialItem{GameID: "hsr", Name: "矿", Category: "collect"})         //

	getJSON := func(path string, out any) {
		t.Helper()
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}

	var goals []store.CharacterGoal
	getJSON("/api/character-goals?game_id=genshin", &goals)
	if len(goals) != 2 {
		t.Fatalf("goals by game_id=%d want 2: %+v", len(goals), goals)
	}
	getJSON("/api/character-goals?game_id=genshin&status=open", &goals)
	if len(goals) != 1 || goals[0].Name != "g-open" {
		t.Fatalf("goals by game_id+status: %+v", goals)
	}
	getJSON("/api/character-goals?game_id=hsr", &goals)
	if len(goals) != 1 || goals[0].Name != "h-open" {
		t.Fatalf("goals by other game: %+v", goals)
	}

	var mats []store.MaterialItem
	getJSON("/api/materials?game_id=genshin", &mats)
	if len(mats) != 2 {
		t.Fatalf("materials by game_id=%d want 2", len(mats))
	}
	getJSON("/api/materials?game_id=genshin&category=boss", &mats)
	if len(mats) != 1 || mats[0].Name != "核" {
		t.Fatalf("materials by game_id+category: %+v", mats)
	}
	getJSON("/api/materials?category=collect", &mats)
	if len(mats) != 2 {
		t.Fatalf("materials by category across games=%d want 2", len(mats))
	}
}

func TestGameIDAndSourceURLValidation(t *testing.T) {
	srv, _, _ := newTestServer(t, "")
	c := srv.Client()
	post := func(path, body string) int {
		t.Helper()
		resp, err := c.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// game id character set is enforced server-side (it flows into URLs and
	// JS handler args on the dashboard)
	cases := []struct {
		id   string
		want int
	}{
		{"genshin-2_x", http.StatusCreated},
		{"", http.StatusBadRequest},
		{"has space", http.StatusBadRequest},
		{"quote'id", http.StatusBadRequest},
		{"paren(id", http.StatusBadRequest},
		{strings.Repeat("x", 200), http.StatusBadRequest},
	}
	for _, tc := range cases {
		body := `{"id":"` + tc.id + `","name":"n","adapter":"genshin","tool_path":"x","enabled":true}`
		if code := post("/api/games", body); code != tc.want {
			t.Errorf("game id %q: status=%d want %d", tc.id, code, tc.want)
		}
	}

	// route source_url must be empty or absolute http(s)
	if _, err := c.Post(srv.URL+"/api/routes", "application/json", strings.NewReader(``)); err != nil {
		t.Fatal(err)
	}
	var rt store.Route
	resp, err := c.Post(srv.URL+"/api/routes", "application/json", strings.NewReader(`{"game_id":"genshin-2_x","name":"r","file_path":"D:/x.json"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&rt); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("route without source_url: status=%d", resp.StatusCode)
	}
	bad := `{"game_id":"genshin-2_x","name":"bad","file_path":"D:/x.json","source_url":"javascript:alert(1)"}`
	if code := post("/api/routes", bad); code != http.StatusBadRequest {
		t.Fatalf("javascript: source_url accepted (status=%d)", code)
	}
	good := `{"game_id":"genshin-2_x","name":"ok","file_path":"D:/y.json","source_url":"https://example.com/v"}`
	if code := post("/api/routes", good); code != http.StatusCreated {
		t.Fatalf("https source_url rejected (status=%d)", code)
	}
}

func TestReferentialValidationReturns400(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "g", Adapter: "genshin", ToolPath: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	c := srv.Client()
	post := func(path, body string) int {
		t.Helper()
		resp, err := c.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if resp.StatusCode == http.StatusBadRequest && e.Error == "" {
			t.Fatalf("400 without an error message for %s", path)
		}
		return resp.StatusCode
	}

	// task referencing a missing game
	if code := post("/api/tasks", `{"game_id":"nope","name":"t","type":"raw","params":{"raw_args":["a"]}}`); code != http.StatusBadRequest {
		t.Errorf("task with unknown game: status=%d", code)
	}
	// negative retry settings
	if code := post("/api/tasks", `{"game_id":"genshin","name":"t","type":"raw","max_retries":-1}`); code != http.StatusBadRequest {
		t.Errorf("negative max_retries: status=%d", code)
	}
	if code := post("/api/tasks", `{"game_id":"genshin","name":"t","type":"raw","timeout_sec":-5}`); code != http.StatusBadRequest {
		t.Errorf("negative timeout_sec: status=%d", code)
	}
	// plan referencing a missing task
	if code := post("/api/plans", `{"name":"p","task_id":424242,"cron_expr":"0 9 * * *"}`); code != http.StatusBadRequest {
		t.Errorf("plan with unknown task: status=%d", code)
	}
	// character with unknown game
	if code := post("/api/characters", `{"game_id":"nope","name":"a"}`); code != http.StatusBadRequest {
		t.Errorf("character with unknown game: status=%d", code)
	}
	// goal with unknown character
	if code := post("/api/character-goals", `{"character_id":987,"name":"g"}`); code != http.StatusBadRequest {
		t.Errorf("goal with unknown character: status=%d", code)
	}
	// route with unknown game
	if code := post("/api/routes", `{"game_id":"nope","name":"r"}`); code != http.StatusBadRequest {
		t.Errorf("route with unknown game: status=%d", code)
	}

	// sanity: valid task still goes through
	resp, err := c.Post(srv.URL+"/api/tasks", "application/json", strings.NewReader(`{"game_id":"genshin","name":"ok","type":"onedragon","params":"{}","max_retries":2,"timeout_sec":10,"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("valid task status=%d", resp.StatusCode)
	}
}

// TestHelperProcess is re-executed as a child process by
// TestDeleteTaskCancelsRunningExecution: a cross-platform controllable child.
// The child is selected with -test.run=TestHelperProcess and told to sleep via
// the args after "--" (task params cannot pass env vars, so no sentinel env).
func TestHelperProcess(t *testing.T) {
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) >= 2 && args[0] == "sleep" {
		d, _ := time.ParseDuration(args[1])
		time.Sleep(d)
		os.Exit(0)
	}
	// In the parent test binary there are no "--" args: not a helper run.
}

// Deleting a task whose run is still active must return promptly (the service
// kills the run) instead of leaving an orphaned process with cascaded rows.
func TestDeleteTaskCancelsRunningExecution(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "g", Adapter: "genshin", ToolPath: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{
		"exe":      os.Args[0],
		"raw_args": []string{"-test.run=TestHelperProcess", "--", "sleep", "30s"},
	})
	task, err := st.CreateTask(store.Task{GameID: "genshin", Name: "long", Type: "raw", Params: string(params), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c := srv.Client()

	resp, err := c.Post(srv.URL+"/api/tasks/"+strconv.FormatInt(task.ID, 10)+"/run", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// wait until the execution actually starts; finishing early means the
	// helper child exited instead of sleeping, which is itself a failure
	deadline := time.Now().Add(10 * time.Second)
	for {
		e, err := st.GetExecution(1)
		if err == nil {
			if e.Status == store.StatusRunning {
				break
			}
			if e.Status != store.StatusPending {
				t.Fatalf("child finished before delete (status=%s) - helper did not sleep", e.Status)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution never started")
		}
		time.Sleep(50 * time.Millisecond)
	}

	start := time.Now()
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/tasks/"+strconv.FormatInt(task.ID, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	delResp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("delete blocked for %s", elapsed)
	}
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d", delResp.StatusCode)
	}
	if _, err := st.GetTask(task.ID); err != store.ErrNotFound {
		t.Fatalf("task still exists: %v", err)
	}
}

func TestListExecutionsMetaMode(t *testing.T) {
	srv, st, _ := newTestServer(t, "")
	if _, err := st.CreateGame(store.Game{ID: "genshin", Name: "g", Adapter: "genshin", ToolPath: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	task, err := st.CreateTask(store.Task{GameID: "genshin", Name: "t", Type: "raw", Params: "{}", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateExecution(store.Execution{TaskID: task.ID, Trigger: store.TriggerManual, Status: store.StatusSuccess, Stdout: "very-long-output", Stderr: "e"}); err != nil {
		t.Fatal(err)
	}

	resp, err := srv.Client().Get(srv.URL + "/api/executions?meta=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "very-long-output") {
		t.Fatal("meta mode leaked stdout")
	}
	var metas []store.ExecutionMeta
	if err := json.Unmarshal(raw, &metas); err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Status != store.StatusSuccess || metas[0].TaskID != task.ID {
		t.Fatalf("metas=%+v", metas)
	}

	// default mode still carries the full output
	resp2, err := srv.Client().Get(srv.URL + "/api/executions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	raw2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(raw2), "very-long-output") {
		t.Fatal("default mode lost stdout")
	}
}
