package store

import (
	"fmt"
	"strings"
)

// PlannerDataset is one game's planner rows in importable form. Ids inside are
// only used to wire rows of the same file together; ImportPlannerData always
// remaps them to real database ids.
type PlannerDataset struct {
	Characters   []Character
	Goals        []CharacterGoal
	Materials    []MaterialItem
	Requirements []MaterialRequirement
}

// PlannerImportResult counts what an import did (or would do, when dry run).
type PlannerImportResult struct {
	Created int
	Updated int
	Skipped int
}

// ImportPlannerData imports one game's planner dataset inside a single
// transaction: either every write commits or none does. Rows dedupe on
// (game_id,name) for characters and materials, (character,name) for goals and
// (goal,material) for requirements; with upsert the existing row is updated,
// otherwise it is skipped. Duplicate names inside one file collapse onto the
// first row. With dryRun the identical resolution and counting logic runs and
// the transaction is rolled back, so the counts describe what a real import
// would have done.
func (s *Store) ImportPlannerData(gameID string, d PlannerDataset, dryRun, upsert bool) (PlannerImportResult, error) {
	res := PlannerImportResult{}
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	norm := func(v string) string { return strings.ToLower(strings.TrimSpace(v)) }
	placeholder := int64(-1)
	nextPlaceholder := func() int64 { placeholder--; return placeholder }

	// In upsert mode an empty incoming field keeps the stored value: a partial
	// file must not wipe data it simply did not carry (export/import roundtrips
	// always carry every field, so clearing a field is not expressible via
	// import — delete it through the CRUD API instead).
	overrideStr := func(cur, in string) string {
		if in == "" {
			return cur
		}
		return in
	}
	overrideInt := func(cur, in int) int {
		if in == 0 {
			return cur
		}
		return in
	}
	overrideTags := func(cur, in []string) []string {
		if len(in) == 0 {
			return cur
		}
		return in
	}

	// characters: dedupe by (game_id, name)
	chByName := map[string]int64{}
	if err := scanIDName(tx, `SELECT id,name FROM characters WHERE game_id=?`, gameID, norm, chByName); err != nil {
		return res, err
	}
	chByOld := map[int64]int64{}
	for _, c := range d.Characters {
		key := norm(c.Name)
		old := c.ID
		if ex, ok := chByName[key]; ok {
			if upsert {
				full, err := getCharacter(tx, ex)
				if err != nil {
					return res, fmt.Errorf("character %q: %w", c.Name, err)
				}
				full.RoleType = overrideStr(full.RoleType, c.RoleType)
				full.Element = overrideStr(full.Element, c.Element)
				full.Weapon = overrideStr(full.Weapon, c.Weapon)
				full.Rarity = overrideInt(full.Rarity, c.Rarity)
				full.Tags = overrideTags(full.Tags, c.Tags)
				full.Notes = overrideStr(full.Notes, c.Notes)
				if !dryRun {
					if err := updateCharacter(tx, &full); err != nil {
						return res, fmt.Errorf("character %q: %w", c.Name, err)
					}
				}
				res.Updated++
			} else {
				res.Skipped++
			}
			if old != 0 {
				chByOld[old] = ex
			}
			continue
		}
		c.ID = 0
		c.GameID = gameID
		if dryRun {
			c.ID = nextPlaceholder()
		} else if err := insertCharacter(tx, &c); err != nil {
			return res, fmt.Errorf("character %q: %w", c.Name, err)
		}
		chByName[key] = c.ID
		res.Created++
		if old != 0 {
			chByOld[old] = c.ID
		}
	}

	// materials: dedupe by (game_id, name)
	matByName := map[string]int64{}
	if err := scanIDName(tx, `SELECT id,name FROM material_items WHERE game_id=?`, gameID, norm, matByName); err != nil {
		return res, err
	}
	matByOld := map[int64]int64{}
	for _, m := range d.Materials {
		key := norm(m.Name)
		old := m.ID
		if ex, ok := matByName[key]; ok {
			if upsert {
				full, err := getMaterialItem(tx, ex)
				if err != nil {
					return res, fmt.Errorf("material %q: %w", m.Name, err)
				}
				full.Category = overrideStr(full.Category, m.Category)
				full.SourceHint = overrideStr(full.SourceHint, m.SourceHint)
				full.RouteTypeHint = overrideStr(full.RouteTypeHint, m.RouteTypeHint)
				full.Notes = overrideStr(full.Notes, m.Notes)
				if !dryRun {
					if err := updateMaterialItem(tx, &full); err != nil {
						return res, fmt.Errorf("material %q: %w", m.Name, err)
					}
				}
				res.Updated++
			} else {
				res.Skipped++
			}
			if old != 0 {
				matByOld[old] = ex
			}
			continue
		}
		m.ID = 0
		m.GameID = gameID
		if dryRun {
			m.ID = nextPlaceholder()
		} else if err := insertMaterialItem(tx, &m); err != nil {
			return res, fmt.Errorf("material %q: %w", m.Name, err)
		}
		matByName[key] = m.ID
		res.Created++
		if old != 0 {
			matByOld[old] = m.ID
		}
	}

	// goals: dedupe by (character, name); character ids are remapped
	goalsByChar := map[int64]map[string]int64{}
	goalByOld := map[int64]int64{}
	for _, g := range d.Goals {
		realChar, ok := chByOld[g.CharacterID]
		if !ok {
			return res, fmt.Errorf("goal %q: character_id %d unresolved", g.Name, g.CharacterID)
		}
		old := g.ID
		byName, ok := goalsByChar[realChar]
		if !ok {
			byName = map[string]int64{}
			if err := scanIDName(tx, `SELECT id,name FROM character_goals WHERE character_id=?`, realChar, norm, byName); err != nil {
				return res, err
			}
			goalsByChar[realChar] = byName
		}
		if ex, ok := byName[norm(g.Name)]; ok {
			if upsert {
				full, err := getCharacterGoal(tx, ex)
				if err != nil {
					return res, fmt.Errorf("goal %q: %w", g.Name, err)
				}
				full.TargetLevel = overrideStr(full.TargetLevel, g.TargetLevel)
				full.TargetSkill = overrideStr(full.TargetSkill, g.TargetSkill)
				full.TargetEquipment = overrideStr(full.TargetEquipment, g.TargetEquipment)
				full.Priority = overrideInt(full.Priority, g.Priority)
				full.Notes = overrideStr(full.Notes, g.Notes)
				full.Status = overrideStr(full.Status, g.Status)
				if !dryRun {
					if err := updateCharacterGoal(tx, &full); err != nil {
						return res, fmt.Errorf("goal %q: %w", g.Name, err)
					}
				}
				res.Updated++
			} else {
				res.Skipped++
			}
			if old != 0 {
				goalByOld[old] = ex
			}
			continue
		}
		g.ID = 0
		g.CharacterID = realChar
		if dryRun {
			g.ID = nextPlaceholder()
		} else if err := insertCharacterGoal(tx, &g); err != nil {
			return res, fmt.Errorf("goal %q: %w", g.Name, err)
		}
		byName[norm(g.Name)] = g.ID
		res.Created++
		if old != 0 {
			goalByOld[old] = g.ID
		}
	}

	// requirements: dedupe by (goal, material); both ids are remapped
	reqsByGoal := map[int64]map[int64]int64{}
	for i, r := range d.Requirements {
		realGoal, ok := goalByOld[r.GoalID]
		if !ok {
			return res, fmt.Errorf("requirement[%d]: goal_id %d unresolved", i, r.GoalID)
		}
		realMat, ok := matByOld[r.MaterialID]
		if !ok {
			return res, fmt.Errorf("requirement[%d]: material_id %d unresolved", i, r.MaterialID)
		}
		byMat, ok := reqsByGoal[realGoal]
		if !ok {
			byMat = map[int64]int64{}
			if err := scanIDID(tx, `SELECT material_id,id FROM material_requirements WHERE goal_id=?`, realGoal, byMat); err != nil {
				return res, err
			}
			reqsByGoal[realGoal] = byMat
		}
		if _, ok := byMat[realMat]; ok {
			if upsert {
				if !dryRun {
					existing := MaterialRequirement{
						ID:            byMat[realMat],
						GoalID:        realGoal,
						MaterialID:    realMat,
						RequiredCount: r.RequiredCount,
						OwnedCount:    r.OwnedCount,
						Priority:      r.Priority,
						Notes:         r.Notes,
					}
					if err := updateMaterialRequirement(tx, &existing); err != nil {
						return res, fmt.Errorf("requirement[%d]: %w", i, err)
					}
				}
				res.Updated++
			} else {
				res.Skipped++
			}
			continue
		}
		r.ID = 0
		r.GoalID, r.MaterialID = realGoal, realMat
		if !dryRun {
			if err := insertMaterialRequirement(tx, &r); err != nil {
				return res, fmt.Errorf("requirement[%d]: %w", i, err)
			}
		}
		byMat[realMat] = r.ID
		res.Created++
	}

	if dryRun {
		return res, nil
	}
	return res, tx.Commit()
}

// scanIDName loads `SELECT id,name` rows into a map keyed by the (optionally
// normalized) name.
func scanIDName(q dbtx, query string, arg any, keyFn func(string) string, into map[string]int64) error {
	rows, err := q.Query(query, arg)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return err
		}
		into[keyFn(key)] = id
	}
	return rows.Err()
}

// scanIDID loads `SELECT a,b` rows into a b->a keyed map (used for
// material_requirements: material_id -> requirement id).
func scanIDID(q dbtx, query string, arg any, into map[int64]int64) error {
	rows, err := q.Query(query, arg)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b int64
		if err := rows.Scan(&a, &b); err != nil {
			return err
		}
		into[a] = b
	}
	return rows.Err()
}
