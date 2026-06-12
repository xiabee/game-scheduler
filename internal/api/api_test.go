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
