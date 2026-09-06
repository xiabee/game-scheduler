package taskfactory

import (
	"testing"

	"github.com/xiabee/game-scheduler/internal/store"
)

func TestFromRouteGenshinUsesScriptPath(t *testing.T) {
	g := store.Game{ID: "genshin", Adapter: "genshin"}
	rt := store.Route{ID: 9, GameID: "genshin", Name: "采集", FilePath: "D:/routes/采集.json"}
	task, err := FromRoute(g, rt)
	if err != nil {
		t.Fatal(err)
	}
	if task.Type != "script" || task.GameID != "genshin" || task.Name != "采集" {
		t.Fatalf("task=%+v", task)
	}
	if task.RouteID == nil || *task.RouteID != 9 {
		t.Fatalf("route binding lost: %+v", task)
	}
	if task.Params != `{"script":"D:/routes/采集.json"}` {
		t.Fatalf("params=%s", task.Params)
	}
	if task.TimeoutSec != 3600 || !task.Enabled {
		t.Fatalf("defaults not applied: %+v", task)
	}
}

func TestFromRouteHsrAndWuwa(t *testing.T) {
	hsrTask, err := FromRoute(store.Game{ID: "hsr", Adapter: "hsr"}, store.Route{ID: 1, GameID: "hsr", FilePath: "x.json"})
	if err != nil {
		t.Fatal(err)
	}
	if hsrTask.Type != "fhoe_route" || hsrTask.Params != `{"route":"x.json"}` {
		t.Fatalf("hsr task=%+v", hsrTask)
	}

	wuwaTask, err := FromRoute(store.Game{ID: "wuwa", Adapter: "wuwa"}, store.Route{ID: 2, GameID: "wuwa", Name: "farm-name"})
	if err != nil {
		t.Fatal(err)
	}
	if wuwaTask.Type != "farm" || wuwaTask.Params != `{"exit":true,"route":"farm-name","task_index":1}` {
		t.Fatalf("wuwa task=%+v", wuwaTask)
	}
}

func TestFromRouteR1999ConfigVariants(t *testing.T) {
	for _, routeType := range []string{"daily", "farm", "resource"} {
		task, err := FromRoute(store.Game{ID: "r1999", Adapter: "r1999"},
			store.Route{ID: 3, GameID: "r1999", Name: "cfg", RouteType: routeType})
		if err != nil {
			t.Fatal(err)
		}
		if task.Type != "run" || task.Params != `{"config":"cfg"}` {
			t.Fatalf("route_type %s: task=%+v", routeType, task)
		}
	}
	other, err := FromRoute(store.Game{ID: "r1999", Adapter: "r1999"},
		store.Route{ID: 3, GameID: "r1999", Name: "cfg", RouteType: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if other.Params != "{}" {
		t.Fatalf("non-config route should pass no config: %+v", other)
	}
}

func TestFromRouteRejectsUnknownAdapter(t *testing.T) {
	if _, err := FromRoute(store.Game{ID: "x", Adapter: "unknown"}, store.Route{GameID: "x"}); err == nil {
		t.Fatal("unknown adapter should fail")
	}
}
