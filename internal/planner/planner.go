// Package planner recommends safe farming work from manually maintained gaps.
package planner

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/xiabee/game-scheduler/internal/store"
)

// Options controls one recommendation run.
type Options struct {
	GoalID       int64 `json:"goal_id"`
	DailyStamina int   `json:"daily_stamina"`
	MaxTasks     int   `json:"max_tasks"`
}

// Service writes cultivation recommendations into the store.
type Service struct {
	store *store.Store
}

func New(st *store.Store) *Service {
	return &Service{store: st}
}

type gap struct {
	req      store.MaterialRequirement
	material store.MaterialItem
	missing  int
}

// Recommend calculates current gaps and persists fresh open recommendations.
func (s *Service) Recommend(opts Options) ([]store.FarmingRecommendation, error) {
	if opts.GoalID == 0 {
		return nil, fmt.Errorf("goal_id is required")
	}
	goal, err := s.store.GetCharacterGoal(opts.GoalID)
	if err != nil {
		return nil, err
	}
	character, err := s.store.GetCharacter(goal.CharacterID)
	if err != nil {
		return nil, err
	}
	reqs, err := s.store.ListMaterialRequirements(store.MaterialRequirementFilter{GoalID: goal.ID})
	if err != nil {
		return nil, err
	}
	gaps := []gap{}
	for _, req := range reqs {
		missing := req.RequiredCount - req.OwnedCount
		if missing <= 0 {
			continue
		}
		item, err := s.store.GetMaterialItem(req.MaterialID)
		if err != nil {
			return nil, err
		}
		gaps = append(gaps, gap{req: req, material: item, missing: missing})
	}
	sort.SliceStable(gaps, func(i, j int) bool {
		if gaps[i].req.Priority == gaps[j].req.Priority {
			return gaps[i].missing > gaps[j].missing
		}
		return gaps[i].req.Priority > gaps[j].req.Priority
	})
	if err := s.store.ClearFarmingRecommendations(goal.ID); err != nil {
		return nil, err
	}

	maxTasks := opts.MaxTasks
	if maxTasks <= 0 {
		maxTasks = len(gaps)
	}
	out := []store.FarmingRecommendation{}
	usedStamina := 0
	for _, g := range gaps {
		if len(out) >= maxTasks {
			break
		}
		rec := s.recommendForGap(goal, character.GameID, g)
		if opts.DailyStamina > 0 && rec.EstimatedStamina > 0 && usedStamina+rec.EstimatedStamina > opts.DailyStamina && len(out) > 0 {
			continue
		}
		usedStamina += rec.EstimatedStamina
		saved, err := s.store.CreateFarmingRecommendation(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, saved)
	}
	return out, nil
}

func (s *Service) recommendForGap(goal store.CharacterGoal, gameID string, g gap) store.FarmingRecommendation {
	routes, _ := s.store.SearchRoutes(store.RouteFilter{GameID: gameID, Limit: 200})
	best, score := bestRoute(routes, g.material)
	recType := "manual"
	var routeID *int64
	reason := fmt.Sprintf("%s 缺口 %d, 需求优先级 %d。", g.material.Name, g.missing, g.req.Priority)
	if best.ID != 0 && score > 0 {
		id := best.ID
		routeID = &id
		recType = "route"
		reason += fmt.Sprintf("匹配路线「%s」", best.Name)
		if g.material.RouteTypeHint != "" && strings.EqualFold(best.RouteType, g.material.RouteTypeHint) {
			reason += fmt.Sprintf(", route_type=%s", best.RouteType)
		}
		if g.material.SourceHint != "" {
			reason += fmt.Sprintf(", source_hint=%q", g.material.SourceHint)
		}
		reason += "。"
	} else {
		reason += "暂未匹配到路线资产, 生成手动刷取建议; 可用 source_hint 或 route_type_hint 校准。"
	}
	return store.FarmingRecommendation{
		GoalID:             goal.ID,
		GameID:             gameID,
		MaterialID:         g.material.ID,
		RouteID:            routeID,
		RecommendationType: recType,
		Title:              fmt.Sprintf("刷取 %s x%d", g.material.Name, g.missing),
		Reason:             reason,
		Priority:           g.req.Priority,
		EstimatedRuns:      estimateRuns(g.missing, g.material.Category),
		EstimatedStamina:   estimateStamina(g.missing, g.material),
		Status:             "open",
	}
}

func bestRoute(routes []store.Route, material store.MaterialItem) (store.Route, int) {
	bestScore := 0
	var best store.Route
	for _, rt := range routes {
		score := 0
		if material.RouteTypeHint != "" && strings.EqualFold(rt.RouteType, material.RouteTypeHint) {
			score += 10
		}
		if material.SourceHint != "" && routeContains(rt, material.SourceHint) {
			score += 7
		}
		if material.Name != "" && routeContains(rt, material.Name) {
			score += 3
		}
		if score > bestScore {
			bestScore = score
			best = rt
		}
	}
	return best, bestScore
}

func routeContains(rt store.Route, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	hay := strings.ToLower(rt.Name + " " + rt.Description + " " + rt.SourceTitle + " " + strings.Join(rt.Tags, " "))
	return strings.Contains(hay, needle)
}

func estimateRuns(missing int, category string) int {
	if missing <= 0 {
		return 0
	}
	switch strings.ToLower(category) {
	case "boss", "weekly":
		return int(math.Ceil(float64(missing) / 2))
	default:
		return missing
	}
}

func estimateStamina(missing int, material store.MaterialItem) int {
	runs := estimateRuns(missing, material.Category)
	if runs == 0 {
		return 0
	}
	key := strings.ToLower(material.RouteTypeHint + " " + material.Category)
	switch {
	case strings.Contains(key, "weekly"):
		return runs * 60
	case strings.Contains(key, "boss"), strings.Contains(key, "stagnant_shadow"):
		return runs * 40
	case strings.Contains(key, "collect"), strings.Contains(key, "world_farm"), strings.Contains(key, "daily"):
		return 0
	default:
		return runs * 20
	}
}
