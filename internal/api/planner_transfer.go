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
	// One read transaction: the file's references are guaranteed to resolve
	// within itself even if the planner is being edited concurrently.
	d, err := s.store.ExportPlannerData(gameID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, PlannerExport{
		Version:      plannerExportVersion,
		GameID:       gameID,
		ExportedAt:   time.Now().UTC(),
		Characters:   d.Characters,
		Goals:        d.Goals,
		Materials:    d.Materials,
		Requirements: d.Requirements,
	})
}

// plannerImportRequest is the POST /api/planner/import body.
type plannerImportRequest struct {
	DryRun bool          `json:"dry_run"`
	Upsert bool          `json:"upsert"`
	Data   PlannerExport `json:"data"`
}

// plannerImportResult reports what the import did (or would do, with dry_run).
// Validation problems are reported as a 400 error listing every problem, not
// here: a result is only ever returned for a file that validated cleanly.
type plannerImportResult struct {
	DryRun  bool   `json:"dry_run"`
	GameID  string `json:"game_id"`
	Created int    `json:"created"`
	Updated int    `json:"updated"`
	Skipped int    `json:"skipped"`
}

// plannerImport handles POST /api/planner/import. Characters dedupe on
// (game_id,name), materials on (game_id,name), goals on (character,name),
// requirements on (goal,material). Old ids from the file are used only to
// resolve references between rows of the same file, then remapped to real ids.
// The whole import runs in one transaction: either every write commits or
// nothing changes. Body size is already capped by decode()'s MaxBytesReader.
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
	if problems := validatePlannerImport(d); len(problems) > 0 {
		writeErr(w, http.StatusBadRequest, errors.New(strings.Join(problems, "; ")))
		return
	}

	res, err := s.store.ImportPlannerData(d.GameID, store.PlannerDataset{
		Characters:   d.Characters,
		Goals:        d.Goals,
		Materials:    d.Materials,
		Requirements: d.Requirements,
	}, req.DryRun, req.Upsert)
	if err != nil {
		// The store rolls back on error, so a failed import leaves no partial
		// data behind; surface the row context to the operator.
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out := plannerImportResult{
		DryRun:  req.DryRun,
		GameID:  d.GameID,
		Created: res.Created,
		Updated: res.Updated,
		Skipped: res.Skipped,
	}
	if !req.DryRun && (out.Created > 0 || out.Updated > 0) {
		s.bus.Notify()
	}
	writeJSON(w, http.StatusOK, out)
}

// maxReportedImportProblems bounds how many problems one 400 lists; a wild
// file can have hundreds and the operator only needs the pattern.
const maxReportedImportProblems = 10

// validatePlannerImport checks referential integrity inside the file itself so
// errors are reported before anything is written. It also rejects duplicate
// file ids and duplicate dedupe keys, which would otherwise silently collapse
// or duplicate rows. All problems are collected, not just the first, so a
// hand-edited file can be fixed in one pass.
func validatePlannerImport(d *PlannerExport) []string {
	var problems []string
	add := func(format string, args ...any) {
		if len(problems) < maxReportedImportProblems {
			problems = append(problems, fmt.Sprintf(format, args...))
		}
	}
	chIDs := map[int64]bool{}
	chNames := map[string]bool{}
	for i, c := range d.Characters {
		if strings.TrimSpace(c.Name) == "" {
			add("characters[%d]: name is required", i)
			continue
		}
		if c.GameID != "" && c.GameID != d.GameID {
			add("characters[%d] (%s): game_id %q does not match data.game_id %q", i, c.Name, c.GameID, d.GameID)
		}
		if c.ID != 0 {
			if chIDs[c.ID] {
				add("characters[%d] (%s): duplicate file id %d", i, c.Name, c.ID)
			}
			chIDs[c.ID] = true
		}
		key := strings.ToLower(strings.TrimSpace(c.Name))
		if chNames[key] {
			add("characters[%d]: duplicate name %q in file", i, c.Name)
		}
		chNames[key] = true
	}
	goalIDs := map[int64]bool{}
	goalKeys := map[string]bool{}
	for i, g := range d.Goals {
		if strings.TrimSpace(g.Name) == "" {
			add("character_goals[%d]: name is required", i)
			continue
		}
		if g.CharacterID == 0 || !chIDs[g.CharacterID] {
			add("character_goals[%d] (%s): character_id %d not found among characters in this file", i, g.Name, g.CharacterID)
		}
		if g.ID != 0 {
			if goalIDs[g.ID] {
				add("character_goals[%d] (%s): duplicate file id %d", i, g.Name, g.ID)
			}
			goalIDs[g.ID] = true
		}
		key := fmt.Sprintf("%d/%s", g.CharacterID, strings.ToLower(strings.TrimSpace(g.Name)))
		if goalKeys[key] {
			add("character_goals[%d]: duplicate name %q for the same character in file", i, g.Name)
		}
		goalKeys[key] = true
	}
	matIDs := map[int64]bool{}
	matNames := map[string]bool{}
	for i, m := range d.Materials {
		if strings.TrimSpace(m.Name) == "" {
			add("material_items[%d]: name is required", i)
			continue
		}
		if m.GameID != "" && m.GameID != d.GameID {
			add("material_items[%d] (%s): game_id %q does not match data.game_id %q", i, m.Name, m.GameID, d.GameID)
		}
		if m.ID != 0 {
			if matIDs[m.ID] {
				add("material_items[%d] (%s): duplicate file id %d", i, m.Name, m.ID)
			}
			matIDs[m.ID] = true
		}
		key := strings.ToLower(strings.TrimSpace(m.Name))
		if matNames[key] {
			add("material_items[%d]: duplicate name %q in file", i, m.Name)
		}
		matNames[key] = true
	}
	reqKeys := map[string]bool{}
	for i, r := range d.Requirements {
		if r.GoalID == 0 || !goalIDs[r.GoalID] {
			add("material_requirements[%d]: goal_id %d not found among character_goals in this file", i, r.GoalID)
		}
		if r.MaterialID == 0 || !matIDs[r.MaterialID] {
			add("material_requirements[%d]: material_id %d not found among material_items in this file", i, r.MaterialID)
		}
		key := fmt.Sprintf("%d/%d", r.GoalID, r.MaterialID)
		if reqKeys[key] {
			add("material_requirements[%d]: duplicate goal_id %d + material_id %d in file", i, r.GoalID, r.MaterialID)
		}
		reqKeys[key] = true
	}
	if len(d.Characters)+len(d.Goals)+len(d.Materials)+len(d.Requirements) > 0 && len(problems) == maxReportedImportProblems {
		problems = append(problems, "...more problems not listed")
	}
	return problems
}
