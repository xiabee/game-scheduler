package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/xiabee/game-scheduler/internal/store"
)

// plannerExportVersion is the schema version stamped into export files and
// accepted by import (0 is also accepted and treated as 1 for hand-written
// seed files that omit the field).
const plannerExportVersion = 1

// PlannerExport is the portable planner dataset for one game. IDs inside are
// only used to wire rows of the same file together; import never trusts them
// as database ids and always remaps.
type PlannerExport struct {
	Version      int                         `json:"version"`
	GameID       string                      `json:"game_id"`
	ExportedAt   time.Time                   `json:"exported_at,omitempty"`
	Characters   []store.Character           `json:"characters"`
	Goals        []store.CharacterGoal       `json:"character_goals"`
	Materials    []store.MaterialItem        `json:"material_items"`
	Requirements []store.MaterialRequirement `json:"material_requirements"`
}

// plannerExport handles GET /api/planner/export?game_id=.
func (s *Server) plannerExport(w http.ResponseWriter, r *http.Request) {
	gameID := strings.TrimSpace(r.URL.Query().Get("game_id"))
	if gameID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("missing query parameter game_id"))
		return
	}
	if _, err := s.store.GetGame(gameID); err != nil {
		writeStoreErr(w, err)
		return
	}
	out := PlannerExport{Version: plannerExportVersion, GameID: gameID, ExportedAt: time.Now().UTC()}
	var err error
	if out.Characters, err = s.store.ListCharacters(store.CharacterFilter{GameID: gameID}); err != nil {
		writeStoreErr(w, err)
		return
	}
	if out.Goals, err = s.store.ListCharacterGoals(store.CharacterGoalFilter{GameID: gameID}); err != nil {
		writeStoreErr(w, err)
		return
	}
	if out.Materials, err = s.store.ListMaterialItems(store.MaterialFilter{GameID: gameID}); err != nil {
		writeStoreErr(w, err)
		return
	}
	out.Requirements = []store.MaterialRequirement{}
	for _, g := range out.Goals {
		reqs, err := s.store.ListMaterialRequirements(store.MaterialRequirementFilter{GoalID: g.ID})
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		out.Requirements = append(out.Requirements, reqs...)
	}
	writeJSON(w, http.StatusOK, out)
}

// plannerImportRequest is the POST /api/planner/import body.
type plannerImportRequest struct {
	DryRun bool          `json:"dry_run"`
	Upsert bool          `json:"upsert"`
	Data   PlannerExport `json:"data"`
}

// plannerImportResult reports what the import did (or would do, with dry_run).
type plannerImportResult struct {
	DryRun  bool     `json:"dry_run"`
	GameID  string   `json:"game_id"`
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}

// plannerImport handles POST /api/planner/import. Characters dedupe on
// (game_id,name), materials on (game_id,name), goals on (character,name),
// requirements on (goal,material). Old ids from the file are used only to
// resolve references between rows of the same file, then remapped to real ids.
// Body size is already capped by decode()'s MaxBytesReader.
func (s *Server) plannerImport(w http.ResponseWriter, r *http.Request) {
	var req plannerImportRequest
	if !decode(w, r, &req) {
		return
	}
	d := &req.Data
	if d.Version > plannerExportVersion {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unsupported export version %d (max %d)", d.Version, plannerExportVersion))
		return
	}
	d.GameID = strings.TrimSpace(d.GameID)
	if d.GameID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("data.game_id is required"))
		return
	}
	if _, err := s.store.GetGame(d.GameID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("game %q does not exist; create it first", d.GameID))
			return
		}
		writeStoreErr(w, err)
		return
	}
	if msg := validatePlannerImport(d); msg != "" {
		writeErr(w, http.StatusBadRequest, errors.New(msg))
		return
	}

	res, err := s.runPlannerImport(req)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if !req.DryRun && (res.Created > 0 || res.Updated > 0) {
		s.bus.Notify()
	}
	writeJSON(w, http.StatusOK, res)
}

// validatePlannerImport checks referential integrity inside the file itself so
// errors are reported before anything is written.
func validatePlannerImport(d *PlannerExport) string {
	chIDs := map[int64]bool{}
	for i, c := range d.Characters {
		if strings.TrimSpace(c.Name) == "" {
			return fmt.Sprintf("characters[%d]: name is required", i)
		}
		if c.GameID != "" && c.GameID != d.GameID {
			return fmt.Sprintf("characters[%d] (%s): game_id %q does not match data.game_id %q", i, c.Name, c.GameID, d.GameID)
		}
		if c.ID != 0 {
			chIDs[c.ID] = true
		}
	}
	goalIDs := map[int64]bool{}
	for i, g := range d.Goals {
		if strings.TrimSpace(g.Name) == "" {
			return fmt.Sprintf("character_goals[%d]: name is required", i)
		}
		if g.CharacterID == 0 || !chIDs[g.CharacterID] {
			return fmt.Sprintf("character_goals[%d] (%s): character_id %d not found among characters in this file", i, g.Name, g.CharacterID)
		}
		if g.ID != 0 {
			goalIDs[g.ID] = true
		}
	}
	matIDs := map[int64]bool{}
	for i, m := range d.Materials {
		if strings.TrimSpace(m.Name) == "" {
			return fmt.Sprintf("material_items[%d]: name is required", i)
		}
		if m.GameID != "" && m.GameID != d.GameID {
			return fmt.Sprintf("material_items[%d] (%s): game_id %q does not match data.game_id %q", i, m.Name, m.GameID, d.GameID)
		}
		if m.ID != 0 {
			matIDs[m.ID] = true
		}
	}
	for i, r := range d.Requirements {
		if r.GoalID == 0 || !goalIDs[r.GoalID] {
			return fmt.Sprintf("material_requirements[%d]: goal_id %d not found among character_goals in this file", i, r.GoalID)
		}
		if r.MaterialID == 0 || !matIDs[r.MaterialID] {
			return fmt.Sprintf("material_requirements[%d]: material_id %d not found among material_items in this file", i, r.MaterialID)
		}
	}
	return ""
}

// runPlannerImport performs the (possibly simulated) import. Dry-run uses the
// same code path but skips every write; remapped ids for would-be-created rows
// are simulated with negative placeholders so downstream references still
// resolve.
func (s *Server) runPlannerImport(req plannerImportRequest) (plannerImportResult, error) {
	d := &req.Data
	res := plannerImportResult{DryRun: req.DryRun, GameID: d.GameID, Errors: []string{}}

	existingChars, err := s.store.ListCharacters(store.CharacterFilter{GameID: d.GameID})
	if err != nil {
		return res, err
	}
	charByName := map[string]store.Character{}
	for _, c := range existingChars {
		charByName[strings.ToLower(strings.TrimSpace(c.Name))] = c
	}
	existingMats, err := s.store.ListMaterialItems(store.MaterialFilter{GameID: d.GameID})
	if err != nil {
		return res, err
	}
	matByName := map[string]store.MaterialItem{}
	for _, m := range existingMats {
		matByName[strings.ToLower(strings.TrimSpace(m.Name))] = m
	}

	placeholder := int64(-1)
	nextPlaceholder := func() int64 { placeholder--; return placeholder }

	// characters: dedupe by (game_id, name)
	charIDMap := map[int64]int64{} // old file id -> real (or placeholder) id
	for _, c := range d.Characters {
		key := strings.ToLower(strings.TrimSpace(c.Name))
		oldID := c.ID
		if ex, ok := charByName[key]; ok {
			if req.Upsert {
				upd := ex
				upd.RoleType, upd.Element, upd.Weapon, upd.Rarity, upd.Tags, upd.Notes =
					c.RoleType, c.Element, c.Weapon, c.Rarity, c.Tags, c.Notes
				if !req.DryRun {
					if _, err := s.store.UpdateCharacter(upd); err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("character %q: %v", c.Name, err))
						continue
					}
				}
				res.Updated++
			} else {
				res.Skipped++
			}
			if oldID != 0 {
				charIDMap[oldID] = ex.ID
			}
			continue
		}
		c.ID = 0
		c.GameID = d.GameID
		newID := nextPlaceholder()
		if !req.DryRun {
			created, err := s.store.CreateCharacter(c)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("character %q: %v", c.Name, err))
				continue
			}
			newID = created.ID
		}
		res.Created++
		if oldID != 0 {
			charIDMap[oldID] = newID
		}
	}

	// materials: dedupe by (game_id, name)
	matIDMap := map[int64]int64{}
	for _, m := range d.Materials {
		key := strings.ToLower(strings.TrimSpace(m.Name))
		oldID := m.ID
		if ex, ok := matByName[key]; ok {
			if req.Upsert {
				upd := ex
				upd.Category, upd.SourceHint, upd.RouteTypeHint, upd.Notes =
					m.Category, m.SourceHint, m.RouteTypeHint, m.Notes
				if !req.DryRun {
					if _, err := s.store.UpdateMaterialItem(upd); err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("material %q: %v", m.Name, err))
						continue
					}
				}
				res.Updated++
			} else {
				res.Skipped++
			}
			if oldID != 0 {
				matIDMap[oldID] = ex.ID
			}
			continue
		}
		m.ID = 0
		m.GameID = d.GameID
		newID := nextPlaceholder()
		if !req.DryRun {
			created, err := s.store.CreateMaterialItem(m)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("material %q: %v", m.Name, err))
				continue
			}
			newID = created.ID
		}
		res.Created++
		if oldID != 0 {
			matIDMap[oldID] = newID
		}
	}

	// goals: dedupe by (character, name); remap character_id
	goalIDMap := map[int64]int64{}
	for _, g := range d.Goals {
		oldID := g.ID
		realChar, ok := charIDMap[g.CharacterID]
		if !ok {
			res.Errors = append(res.Errors, fmt.Sprintf("goal %q: character_id %d unresolved", g.Name, g.CharacterID))
			continue
		}
		var existing *store.CharacterGoal
		if realChar > 0 {
			goals, err := s.store.ListCharacterGoals(store.CharacterGoalFilter{CharacterID: realChar})
			if err != nil {
				return res, err
			}
			for i := range goals {
				if strings.EqualFold(strings.TrimSpace(goals[i].Name), strings.TrimSpace(g.Name)) {
					existing = &goals[i]
					break
				}
			}
		}
		if existing != nil {
			if req.Upsert {
				upd := *existing
				upd.TargetLevel, upd.TargetSkill, upd.TargetEquipment = g.TargetLevel, g.TargetSkill, g.TargetEquipment
				upd.Priority, upd.Notes = g.Priority, g.Notes
				if g.Status != "" {
					upd.Status = g.Status
				}
				if !req.DryRun {
					if _, err := s.store.UpdateCharacterGoal(upd); err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("goal %q: %v", g.Name, err))
						continue
					}
				}
				res.Updated++
			} else {
				res.Skipped++
			}
			if oldID != 0 {
				goalIDMap[oldID] = existing.ID
			}
			continue
		}
		g.ID = 0
		g.CharacterID = realChar
		newID := nextPlaceholder()
		if !req.DryRun {
			created, err := s.store.CreateCharacterGoal(g)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("goal %q: %v", g.Name, err))
				continue
			}
			newID = created.ID
		}
		res.Created++
		if oldID != 0 {
			goalIDMap[oldID] = newID
		}
	}

	// requirements: dedupe by (goal, material); remap both ids
	for i, r := range d.Requirements {
		realGoal, ok := goalIDMap[r.GoalID]
		if !ok {
			res.Errors = append(res.Errors, fmt.Sprintf("requirement[%d]: goal_id %d unresolved", i, r.GoalID))
			continue
		}
		realMat, ok := matIDMap[r.MaterialID]
		if !ok {
			res.Errors = append(res.Errors, fmt.Sprintf("requirement[%d]: material_id %d unresolved", i, r.MaterialID))
			continue
		}
		var existing *store.MaterialRequirement
		if realGoal > 0 {
			reqs, err := s.store.ListMaterialRequirements(store.MaterialRequirementFilter{GoalID: realGoal})
			if err != nil {
				return res, err
			}
			for j := range reqs {
				if reqs[j].MaterialID == realMat {
					existing = &reqs[j]
					break
				}
			}
		}
		if existing != nil {
			if req.Upsert {
				upd := *existing
				upd.RequiredCount, upd.OwnedCount, upd.Priority, upd.Notes = r.RequiredCount, r.OwnedCount, r.Priority, r.Notes
				if !req.DryRun {
					if _, err := s.store.UpdateMaterialRequirement(upd); err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("requirement[%d]: %v", i, err))
						continue
					}
				}
				res.Updated++
			} else {
				res.Skipped++
			}
			continue
		}
		r.ID = 0
		r.GoalID = realGoal
		r.MaterialID = realMat
		if !req.DryRun {
			if _, err := s.store.CreateMaterialRequirement(r); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("requirement[%d]: %v", i, err))
				continue
			}
		}
		res.Created++
	}
	return res, nil
}
