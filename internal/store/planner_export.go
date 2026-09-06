package store

import (
	"database/sql"
)

// ExportPlannerData reads one game's planner rows inside a single read
// transaction, so the snapshot is internally consistent even if concurrent
// writes happen between the queries: every requirement references a goal and
// material present in the same export.
func (s *Store) ExportPlannerData(gameID string) (PlannerDataset, error) {
	var d PlannerDataset
	tx, err := s.db.Begin()
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT id,game_id,name,role_type,element,weapon,rarity,tags,notes,created_at,updated_at
		FROM characters WHERE game_id=? ORDER BY game_id,name,id`, gameID)
	if err != nil {
		return d, err
	}
	d.Characters, err = scanCharacters(rows)
	if err != nil {
		return d, err
	}

	rows, err = tx.Query(`SELECT id,character_id,name,target_level,target_skill,target_equipment,priority,status,notes,created_at,updated_at
		FROM character_goals WHERE character_id IN (SELECT id FROM characters WHERE game_id=?) ORDER BY character_id,id`, gameID)
	if err != nil {
		return d, err
	}
	d.Goals, err = scanGoals(rows)
	if err != nil {
		return d, err
	}

	rows, err = tx.Query(`SELECT id,game_id,name,category,source_hint,route_type_hint,notes,created_at,updated_at
		FROM material_items WHERE game_id=? ORDER BY game_id,name,id`, gameID)
	if err != nil {
		return d, err
	}
	d.Materials, err = scanMaterials(rows)
	if err != nil {
		return d, err
	}

	rows, err = tx.Query(`SELECT r.id,r.goal_id,r.material_id,r.required_count,r.owned_count,r.priority,r.notes,r.created_at,r.updated_at
		FROM material_requirements r
		JOIN character_goals g ON g.id=r.goal_id
		WHERE g.character_id IN (SELECT id FROM characters WHERE game_id=?)
		ORDER BY r.goal_id,r.id`, gameID)
	if err != nil {
		return d, err
	}
	d.Requirements, err = scanRequirements(rows)
	if err != nil {
		return d, err
	}

	return d, tx.Rollback() // read-only: end the snapshot without committing
}

func scanCharacters(rows *sql.Rows) ([]Character, error) {
	defer rows.Close()
	out := []Character{}
	for rows.Next() {
		var c Character
		var tags string
		if err := rows.Scan(&c.ID, &c.GameID, &c.Name, &c.RoleType, &c.Element, &c.Weapon, &c.Rarity, &tags, &c.Notes, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Tags = decodeTags(tags)
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanGoals(rows *sql.Rows) ([]CharacterGoal, error) {
	defer rows.Close()
	out := []CharacterGoal{}
	for rows.Next() {
		var g CharacterGoal
		if err := rows.Scan(&g.ID, &g.CharacterID, &g.Name, &g.TargetLevel, &g.TargetSkill, &g.TargetEquipment, &g.Priority, &g.Status, &g.Notes, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func scanMaterials(rows *sql.Rows) ([]MaterialItem, error) {
	defer rows.Close()
	out := []MaterialItem{}
	for rows.Next() {
		var m MaterialItem
		if err := rows.Scan(&m.ID, &m.GameID, &m.Name, &m.Category, &m.SourceHint, &m.RouteTypeHint, &m.Notes, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanRequirements(rows *sql.Rows) ([]MaterialRequirement, error) {
	defer rows.Close()
	out := []MaterialRequirement{}
	for rows.Next() {
		var r MaterialRequirement
		if err := rows.Scan(&r.ID, &r.GoalID, &r.MaterialID, &r.RequiredCount, &r.OwnedCount, &r.Priority, &r.Notes, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
