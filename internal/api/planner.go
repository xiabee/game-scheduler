package api

import (
	"errors"
	"net/http"

	"github.com/xiabee/game-scheduler/internal/planner"
	"github.com/xiabee/game-scheduler/internal/scheduler"
	"github.com/xiabee/game-scheduler/internal/store"
	"github.com/xiabee/game-scheduler/internal/taskfactory"
)

var errRecommendationNoRoute = errors.New("recommendation has no route_id; create or attach a route before creating a task")

func (s *Server) listCharacters(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListCharacters(store.CharacterFilter{GameID: r.URL.Query().Get("game_id")})
	respond(w, out, err)
}

func (s *Server) createCharacter(w http.ResponseWriter, r *http.Request) {
	var c store.Character
	if !decode(w, r, &c) {
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
		Status:      r.URL.Query().Get("status"),
	})
	respond(w, out, err)
}

func (s *Server) createCharacterGoal(w http.ResponseWriter, r *http.Request) {
	var g store.CharacterGoal
	if !decode(w, r, &g) {
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
	out, err := s.store.ListMaterialItems(store.MaterialFilter{GameID: r.URL.Query().Get("game_id")})
	respond(w, out, err)
}

func (s *Server) createMaterial(w http.ResponseWriter, r *http.Request) {
	var m store.MaterialItem
	if !decode(w, r, &m) {
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

func (s *Server) createMaterialRequirement(w http.ResponseWriter, r *http.Request) {
	var req store.MaterialRequirement
	if !decode(w, r, &req) {
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
		rec, _ = s.store.SetFarmingRecommendationStatus(id, "planned")
		_ = rec
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
