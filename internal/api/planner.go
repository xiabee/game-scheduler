package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/xiabee/game-scheduler/internal/planner"
	"github.com/xiabee/game-scheduler/internal/scheduler"
	"github.com/xiabee/game-scheduler/internal/store"
	"github.com/xiabee/game-scheduler/internal/taskfactory"
)

var errRecommendationNoRoute = errors.New("recommendation has no route_id; create or attach a route before creating a task")

// attachRouteRequest is the POST .../attach-route body.
type attachRouteRequest struct {
	RouteID int64 `json:"route_id"`
}

// attachRecommendationRoute binds an existing route to a recommendation that
// has none, so it can move on to create-task/create-plan. The route must
// belong to the same game as the recommendation.
func (s *Server) attachRecommendationRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req attachRouteRequest
	if !decode(w, r, &req) {
		return
	}
	if req.RouteID == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("route_id is required"))
		return
	}
	rec, err := s.store.GetFarmingRecommendation(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	rt, err := s.store.GetRoute(req.RouteID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if rt.GameID != rec.GameID {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("route %q belongs to game %q, but the recommendation belongs to %q", rt.Name, rt.GameID, rec.GameID))
		return
	}
	rec.RouteID = &rt.ID
	if rec.RecommendationType == "manual" {
		rec.RecommendationType = "route"
	}
	out, err := s.store.UpdateFarmingRecommendation(rec)
	respond(w, out, s.changed(err))
}

func (s *Server) listCharacters(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListCharacters(store.CharacterFilter{GameID: r.URL.Query().Get("game_id")})
	respond(w, out, err)
}

func (s *Server) createCharacter(w http.ResponseWriter, r *http.Request) {
	var c store.Character
	if !decode(w, r, &c) {
		return
	}
	if !s.requireGame(w, c.GameID) {
		return
	}
	out, err := s.store.CreateCharacter(c)
	respondCreated(w, out, s.changed(err))
}

func (s *Server) getCharacter(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := s.store.GetCharacter(id)
	respond(w, out, err)
}

func (s *Server) updateCharacter(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var c store.Character
	if !decode(w, r, &c) {
		return
	}
	if !s.requireGame(w, c.GameID) {
		return
	}
	c.ID = id
	out, err := s.store.UpdateCharacter(c)
	respond(w, out, s.changed(err))
}

func (s *Server) deleteCharacter(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	respondNoContent(w, s.changed(s.store.DeleteCharacter(id)))
}

func (s *Server) listCharacterGoals(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListCharacterGoals(store.CharacterGoalFilter{
		CharacterID: int64Query(r, "character_id"),
		GameID:      r.URL.Query().Get("game_id"),
		Status:      r.URL.Query().Get("status"),
	})
	respond(w, out, err)
}

// requireCharacter writes a 400 when the referenced character does not exist.
func (s *Server) requireCharacter(w http.ResponseWriter, characterID int64) bool {
	if _, err := s.store.GetCharacter(characterID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("character %d does not exist", characterID))
			return false
		}
		writeStoreErr(w, err)
		return false
	}
	return true
}

func (s *Server) createCharacterGoal(w http.ResponseWriter, r *http.Request) {
	var g store.CharacterGoal
	if !decode(w, r, &g) {
		return
	}
	if !s.requireCharacter(w, g.CharacterID) {
		return
	}
	out, err := s.store.CreateCharacterGoal(g)
	respondCreated(w, out, s.changed(err))
}

func (s *Server) getCharacterGoal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := s.store.GetCharacterGoal(id)
	respond(w, out, err)
}

func (s *Server) updateCharacterGoal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var g store.CharacterGoal
	if !decode(w, r, &g) {
		return
	}
	if !s.requireCharacter(w, g.CharacterID) {
		return
	}
	g.ID = id
	out, err := s.store.UpdateCharacterGoal(g)
	respond(w, out, s.changed(err))
}

func (s *Server) deleteCharacterGoal(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	respondNoContent(w, s.changed(s.store.DeleteCharacterGoal(id)))
}

func (s *Server) listMaterials(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListMaterialItems(store.MaterialFilter{
		GameID:   r.URL.Query().Get("game_id"),
		Category: r.URL.Query().Get("category"),
	})
	respond(w, out, err)
}

func (s *Server) createMaterial(w http.ResponseWriter, r *http.Request) {
	var m store.MaterialItem
	if !decode(w, r, &m) {
		return
	}
	if !s.requireGame(w, m.GameID) {
		return
	}
	out, err := s.store.CreateMaterialItem(m)
	respondCreated(w, out, s.changed(err))
}

func (s *Server) getMaterial(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := s.store.GetMaterialItem(id)
	respond(w, out, err)
}

func (s *Server) updateMaterial(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var m store.MaterialItem
	if !decode(w, r, &m) {
		return
	}
	if !s.requireGame(w, m.GameID) {
		return
	}
	m.ID = id
	out, err := s.store.UpdateMaterialItem(m)
	respond(w, out, s.changed(err))
}

func (s *Server) deleteMaterial(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	respondNoContent(w, s.changed(s.store.DeleteMaterialItem(id)))
}

func (s *Server) listMaterialRequirements(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListMaterialRequirements(store.MaterialRequirementFilter{GoalID: int64Query(r, "goal_id")})
	respond(w, out, err)
}

// requireRequirementRefs validates both halves of a material requirement.
func (s *Server) requireRequirementRefs(w http.ResponseWriter, goalID, materialID int64) bool {
	if _, err := s.store.GetCharacterGoal(goalID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("goal %d does not exist", goalID))
			return false
		}
		writeStoreErr(w, err)
		return false
	}
	if _, err := s.store.GetMaterialItem(materialID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("material %d does not exist", materialID))
			return false
		}
		writeStoreErr(w, err)
		return false
	}
	return true
}

func (s *Server) createMaterialRequirement(w http.ResponseWriter, r *http.Request) {
	var req store.MaterialRequirement
	if !decode(w, r, &req) {
		return
	}
	if !s.requireRequirementRefs(w, req.GoalID, req.MaterialID) {
		return
	}
	out, err := s.store.CreateMaterialRequirement(req)
	respondCreated(w, out, s.changed(err))
}

func (s *Server) getMaterialRequirement(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := s.store.GetMaterialRequirement(id)
	respond(w, out, err)
}

func (s *Server) updateMaterialRequirement(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req store.MaterialRequirement
	if !decode(w, r, &req) {
		return
	}
	if !s.requireRequirementRefs(w, req.GoalID, req.MaterialID) {
		return
	}
	req.ID = id
	out, err := s.store.UpdateMaterialRequirement(req)
	respond(w, out, s.changed(err))
}

func (s *Server) deleteMaterialRequirement(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	respondNoContent(w, s.changed(s.store.DeleteMaterialRequirement(id)))
}

func (s *Server) recommendFarming(w http.ResponseWriter, r *http.Request) {
	var opts planner.Options
	if !decode(w, r, &opts) {
		return
	}
	out, err := planner.New(s.store).Recommend(opts)
	respondCreated(w, out, s.changed(err))
}

func (s *Server) listFarmingRecommendations(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListFarmingRecommendations(store.FarmingRecommendationFilter{
		GoalID: int64Query(r, "goal_id"),
		GameID: r.URL.Query().Get("game_id"),
		Status: r.URL.Query().Get("status"),
		Limit:  intQuery(r, "limit"),
	})
	respond(w, out, err)
}

func (s *Server) createTaskFromRecommendation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	task, err := s.ensureRecommendationTask(id)
	if err != nil {
		if errors.Is(err, errRecommendationNoRoute) {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeStoreErr(w, err)
		return
	}
	respondCreated(w, task, nil)
}

type createPlanFromRecommendationRequest struct {
	Name     string `json:"name"`
	CronExpr string `json:"cron_expr"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

func (s *Server) createPlanFromRecommendation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	req := createPlanFromRecommendationRequest{CronExpr: "0 9 * * *"}
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	if err := scheduler.ValidateCron(req.CronExpr); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rec, err := s.store.GetFarmingRecommendation(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var taskID int64
	if rec.TaskID != nil {
		taskID = *rec.TaskID
	} else {
		task, err := s.ensureRecommendationTask(id)
		if err != nil {
			if errors.Is(err, errRecommendationNoRoute) {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			writeStoreErr(w, err)
			return
		}
		taskID = task.ID
	}
	if req.Name == "" {
		req.Name = rec.Title
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	plan, err := s.store.CreatePlan(store.Plan{Name: req.Name, TaskID: taskID, CronExpr: req.CronExpr, Enabled: enabled})
	if err == nil {
		if _, statusErr := s.store.SetFarmingRecommendationStatus(id, "planned"); statusErr != nil {
			// The plan exists now; a stale recommendation status would invite
			// the operator to click "create plan" again and duplicate it.
			s.log.Warn("recommendation status update failed", "rec_id", id, "status", "planned", "err", statusErr)
		}
		_ = s.sched.Reload()
		s.bus.Notify()
	}
	respondCreated(w, plan, err)
}

func (s *Server) dismissRecommendation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := s.store.SetFarmingRecommendationStatus(id, "dismissed")
	respond(w, out, s.changed(err))
}

func (s *Server) completeRecommendation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := s.store.SetFarmingRecommendationStatus(id, "completed")
	respond(w, out, s.changed(err))
}

func (s *Server) ensureRecommendationTask(id int64) (store.Task, error) {
	rec, err := s.store.GetFarmingRecommendation(id)
	if err != nil {
		return store.Task{}, err
	}
	if rec.TaskID != nil {
		return s.store.GetTask(*rec.TaskID)
	}
	if rec.RouteID == nil {
		return store.Task{}, errRecommendationNoRoute
	}
	rt, err := s.store.GetRoute(*rec.RouteID)
	if err != nil {
		return store.Task{}, err
	}
	g, err := s.store.GetGame(rt.GameID)
	if err != nil {
		return store.Task{}, err
	}
	task, err := taskfactory.FromRoute(g, rt)
	if err != nil {
		return store.Task{}, err
	}
	if rec.Title != "" {
		task.Name = rec.Title
	}
	out, err := s.store.CreateTask(task)
	if err != nil {
		return store.Task{}, err
	}
	if _, err := s.store.SetFarmingRecommendationTask(id, out.ID); err != nil {
		return store.Task{}, err
	}
	s.bus.Notify()
	return out, nil
}
