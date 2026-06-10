// Package taskfactory builds scheduler tasks from higher-level assets.
package taskfactory

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/xiabee/game-scheduler/internal/store"
)

// FromRoute converts a route asset into a scheduler task while preserving the
// existing adapter boundary: the runner will still only invoke an external tool.
func FromRoute(g store.Game, rt store.Route) (store.Task, error) {
	routeID := rt.ID
	name := rt.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(rt.FilePath), filepath.Ext(rt.FilePath))
	}
	params := map[string]any{}
	taskType := "raw"
	switch g.Adapter {
	case "genshin":
		taskType = "script"
		params["script"] = rt.FilePath
	case "hsr":
		taskType = "fhoe_route"
		params["route"] = rt.FilePath
	case "wuwa":
		taskType = "farm"
		params["task_index"] = 1
		params["route"] = name
		params["exit"] = true
	case "r1999":
		taskType = "run"
		if rt.RouteType == "daily" || rt.RouteType == "farm" || rt.RouteType == "resource" {
			params["config"] = name
		}
	default:
		return store.Task{}, errors.New("route adapter is not supported for task creation")
	}
	b, err := json.Marshal(params)
	if err != nil {
		return store.Task{}, err
	}
	return store.Task{
		GameID:     g.ID,
		RouteID:    &routeID,
		Name:       name,
		Type:       taskType,
		Params:     string(b),
		TimeoutSec: 3600,
		Enabled:    true,
	}, nil
}
