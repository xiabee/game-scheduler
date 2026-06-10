package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CharacterFilter narrows ListCharacters.
type CharacterFilter struct {
	GameID string
}

func (s *Store) CreateCharacter(c Character) (Character, error) {
	now := time.Now().UTC()
	c.CreatedAt, c.UpdatedAt = now, now
	tags, err := encodeTags(c.Tags)
	if err != nil {
		return Character{}, err
	}
	res, err := s.db.Exec(`INSERT INTO characters (game_id,name,role_type,element,weapon,rarity,tags,notes,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		c.GameID, c.Name, c.RoleType, c.Element, c.Weapon, c.Rarity, tags, c.Notes, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return Character{}, err
	}
	c.ID, _ = res.LastInsertId()
	return c, nil
}

func (s *Store) GetCharacter(id int64) (Character, error) {
	var c Character
	var tags string
	err := s.db.QueryRow(`SELECT id,game_id,name,role_type,element,weapon,rarity,tags,notes,created_at,updated_at FROM characters WHERE id=?`, id).
		Scan(&c.ID, &c.GameID, &c.Name, &c.RoleType, &c.Element, &c.Weapon, &c.Rarity, &tags, &c.Notes, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Character{}, ErrNotFound
	}
	if err != nil {
		return Character{}, err
	}
	c.Tags = decodeTags(tags)
	return c, nil
}

func (s *Store) UpdateCharacter(c Character) (Character, error) {
	c.UpdatedAt = time.Now().UTC()
	tags, err := encodeTags(c.Tags)
	if err != nil {
		return Character{}, err
	}
	res, err := s.db.Exec(`UPDATE characters SET game_id=?,name=?,role_type=?,element=?,weapon=?,rarity=?,tags=?,notes=?,updated_at=? WHERE id=?`,
		c.GameID, c.Name, c.RoleType, c.Element, c.Weapon, c.Rarity, tags, c.Notes, c.UpdatedAt, c.ID)
	if err != nil {
		return Character{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Character{}, ErrNotFound
	}
	return s.GetCharacter(c.ID)
}

func (s *Store) DeleteCharacter(id int64) error {
	res, err := s.db.Exec(`DELETE FROM characters WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListCharacters(f CharacterFilter) ([]Character, error) {
	q := `SELECT id,game_id,name,role_type,element,weapon,rarity,tags,notes,created_at,updated_at FROM characters WHERE 1=1`
	var args []any
	if f.GameID != "" {
		q += ` AND game_id=?`
		args = append(args, f.GameID)
	}
	q += ` ORDER BY game_id,name,id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
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

// CharacterGoalFilter narrows ListCharacterGoals.
type CharacterGoalFilter struct {
	CharacterID int64
	Status      string
}

func (s *Store) CreateCharacterGoal(g CharacterGoal) (CharacterGoal, error) {
	now := time.Now().UTC()
	g.CreatedAt, g.UpdatedAt = now, now
	if g.Status == "" {
		g.Status = "open"
	}
	res, err := s.db.Exec(`INSERT INTO character_goals (character_id,name,target_level,target_skill,target_equipment,priority,status,notes,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		g.CharacterID, g.Name, g.TargetLevel, g.TargetSkill, g.TargetEquipment, g.Priority, g.Status, g.Notes, g.CreatedAt, g.UpdatedAt)
	if err != nil {
		return CharacterGoal{}, err
	}
	g.ID, _ = res.LastInsertId()
	return g, nil
}

func (s *Store) GetCharacterGoal(id int64) (CharacterGoal, error) {
	var g CharacterGoal
	err := s.db.QueryRow(`SELECT id,character_id,name,target_level,target_skill,target_equipment,priority,status,notes,created_at,updated_at FROM character_goals WHERE id=?`, id).
		Scan(&g.ID, &g.CharacterID, &g.Name, &g.TargetLevel, &g.TargetSkill, &g.TargetEquipment, &g.Priority, &g.Status, &g.Notes, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CharacterGoal{}, ErrNotFound
	}
	return g, err
}

func (s *Store) UpdateCharacterGoal(g CharacterGoal) (CharacterGoal, error) {
	g.UpdatedAt = time.Now().UTC()
	if g.Status == "" {
		g.Status = "open"
	}
	res, err := s.db.Exec(`UPDATE character_goals SET character_id=?,name=?,target_level=?,target_skill=?,target_equipment=?,priority=?,status=?,notes=?,updated_at=? WHERE id=?`,
		g.CharacterID, g.Name, g.TargetLevel, g.TargetSkill, g.TargetEquipment, g.Priority, g.Status, g.Notes, g.UpdatedAt, g.ID)
	if err != nil {
		return CharacterGoal{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return CharacterGoal{}, ErrNotFound
	}
	return s.GetCharacterGoal(g.ID)
}

func (s *Store) DeleteCharacterGoal(id int64) error {
	res, err := s.db.Exec(`DELETE FROM character_goals WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListCharacterGoals(f CharacterGoalFilter) ([]CharacterGoal, error) {
	q := `SELECT id,character_id,name,target_level,target_skill,target_equipment,priority,status,notes,created_at,updated_at FROM character_goals WHERE 1=1`
	var args []any
	if f.CharacterID != 0 {
		q += ` AND character_id=?`
		args = append(args, f.CharacterID)
	}
	if f.Status != "" {
		q += ` AND status=?`
		args = append(args, f.Status)
	}
	q += ` ORDER BY priority DESC,id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
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

// MaterialFilter narrows ListMaterialItems.
type MaterialFilter struct {
	GameID string
}

func (s *Store) CreateMaterialItem(m MaterialItem) (MaterialItem, error) {
	now := time.Now().UTC()
	m.CreatedAt, m.UpdatedAt = now, now
	res, err := s.db.Exec(`INSERT INTO material_items (game_id,name,category,source_hint,route_type_hint,notes,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		m.GameID, m.Name, m.Category, m.SourceHint, m.RouteTypeHint, m.Notes, m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return MaterialItem{}, err
	}
	m.ID, _ = res.LastInsertId()
	return m, nil
}

func (s *Store) GetMaterialItem(id int64) (MaterialItem, error) {
	var m MaterialItem
	err := s.db.QueryRow(`SELECT id,game_id,name,category,source_hint,route_type_hint,notes,created_at,updated_at FROM material_items WHERE id=?`, id).
		Scan(&m.ID, &m.GameID, &m.Name, &m.Category, &m.SourceHint, &m.RouteTypeHint, &m.Notes, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MaterialItem{}, ErrNotFound
	}
	return m, err
}

func (s *Store) UpdateMaterialItem(m MaterialItem) (MaterialItem, error) {
	m.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(`UPDATE material_items SET game_id=?,name=?,category=?,source_hint=?,route_type_hint=?,notes=?,updated_at=? WHERE id=?`,
		m.GameID, m.Name, m.Category, m.SourceHint, m.RouteTypeHint, m.Notes, m.UpdatedAt, m.ID)
	if err != nil {
		return MaterialItem{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return MaterialItem{}, ErrNotFound
	}
	return s.GetMaterialItem(m.ID)
}

func (s *Store) DeleteMaterialItem(id int64) error {
	res, err := s.db.Exec(`DELETE FROM material_items WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListMaterialItems(f MaterialFilter) ([]MaterialItem, error) {
	q := `SELECT id,game_id,name,category,source_hint,route_type_hint,notes,created_at,updated_at FROM material_items WHERE 1=1`
	var args []any
	if f.GameID != "" {
		q += ` AND game_id=?`
		args = append(args, f.GameID)
	}
	q += ` ORDER BY game_id,name,id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
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

// MaterialRequirementFilter narrows ListMaterialRequirements.
type MaterialRequirementFilter struct {
	GoalID int64
}

func (s *Store) CreateMaterialRequirement(r MaterialRequirement) (MaterialRequirement, error) {
	now := time.Now().UTC()
	r.CreatedAt, r.UpdatedAt = now, now
	res, err := s.db.Exec(`INSERT INTO material_requirements (goal_id,material_id,required_count,owned_count,priority,notes,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		r.GoalID, r.MaterialID, r.RequiredCount, r.OwnedCount, r.Priority, r.Notes, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return MaterialRequirement{}, err
	}
	r.ID, _ = res.LastInsertId()
	return r, nil
}

func (s *Store) GetMaterialRequirement(id int64) (MaterialRequirement, error) {
	var r MaterialRequirement
	err := s.db.QueryRow(`SELECT id,goal_id,material_id,required_count,owned_count,priority,notes,created_at,updated_at FROM material_requirements WHERE id=?`, id).
		Scan(&r.ID, &r.GoalID, &r.MaterialID, &r.RequiredCount, &r.OwnedCount, &r.Priority, &r.Notes, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MaterialRequirement{}, ErrNotFound
	}
	return r, err
}

func (s *Store) UpdateMaterialRequirement(r MaterialRequirement) (MaterialRequirement, error) {
	r.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(`UPDATE material_requirements SET goal_id=?,material_id=?,required_count=?,owned_count=?,priority=?,notes=?,updated_at=? WHERE id=?`,
		r.GoalID, r.MaterialID, r.RequiredCount, r.OwnedCount, r.Priority, r.Notes, r.UpdatedAt, r.ID)
	if err != nil {
		return MaterialRequirement{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return MaterialRequirement{}, ErrNotFound
	}
	return s.GetMaterialRequirement(r.ID)
}

func (s *Store) DeleteMaterialRequirement(id int64) error {
	res, err := s.db.Exec(`DELETE FROM material_requirements WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListMaterialRequirements(f MaterialRequirementFilter) ([]MaterialRequirement, error) {
	q := `SELECT id,goal_id,material_id,required_count,owned_count,priority,notes,created_at,updated_at FROM material_requirements WHERE 1=1`
	var args []any
	if f.GoalID != 0 {
		q += ` AND goal_id=?`
		args = append(args, f.GoalID)
	}
	q += ` ORDER BY priority DESC,id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
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

// FarmingRecommendationFilter narrows ListFarmingRecommendations.
type FarmingRecommendationFilter struct {
	GoalID int64
	GameID string
	Status string
	Limit  int
}

func (s *Store) CreateFarmingRecommendation(r FarmingRecommendation) (FarmingRecommendation, error) {
	now := time.Now().UTC()
	r.CreatedAt, r.UpdatedAt = now, now
	if r.Status == "" {
		r.Status = "open"
	}
	if r.RecommendationType == "" {
		r.RecommendationType = "manual"
	}
	res, err := s.db.Exec(`INSERT INTO farming_recommendations (goal_id,game_id,material_id,route_id,task_id,recommendation_type,title,reason,priority,estimated_runs,estimated_stamina,status,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.GoalID, r.GameID, r.MaterialID, r.RouteID, r.TaskID, r.RecommendationType, r.Title, r.Reason, r.Priority, r.EstimatedRuns, r.EstimatedStamina, r.Status, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return FarmingRecommendation{}, err
	}
	r.ID, _ = res.LastInsertId()
	return r, nil
}

func (s *Store) GetFarmingRecommendation(id int64) (FarmingRecommendation, error) {
	var r FarmingRecommendation
	err := s.db.QueryRow(`SELECT id,goal_id,game_id,material_id,route_id,task_id,recommendation_type,title,reason,priority,estimated_runs,estimated_stamina,status,created_at,updated_at FROM farming_recommendations WHERE id=?`, id).
		Scan(&r.ID, &r.GoalID, &r.GameID, &r.MaterialID, &r.RouteID, &r.TaskID, &r.RecommendationType, &r.Title, &r.Reason, &r.Priority, &r.EstimatedRuns, &r.EstimatedStamina, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return FarmingRecommendation{}, ErrNotFound
	}
	return r, err
}

func (s *Store) UpdateFarmingRecommendation(r FarmingRecommendation) (FarmingRecommendation, error) {
	r.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(`UPDATE farming_recommendations SET goal_id=?,game_id=?,material_id=?,route_id=?,task_id=?,recommendation_type=?,title=?,reason=?,priority=?,estimated_runs=?,estimated_stamina=?,status=?,updated_at=? WHERE id=?`,
		r.GoalID, r.GameID, r.MaterialID, r.RouteID, r.TaskID, r.RecommendationType, r.Title, r.Reason, r.Priority, r.EstimatedRuns, r.EstimatedStamina, r.Status, r.UpdatedAt, r.ID)
	if err != nil {
		return FarmingRecommendation{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return FarmingRecommendation{}, ErrNotFound
	}
	return s.GetFarmingRecommendation(r.ID)
}

func (s *Store) DeleteFarmingRecommendation(id int64) error {
	res, err := s.db.Exec(`DELETE FROM farming_recommendations WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListFarmingRecommendations(f FarmingRecommendationFilter) ([]FarmingRecommendation, error) {
	q := `SELECT id,goal_id,game_id,material_id,route_id,task_id,recommendation_type,title,reason,priority,estimated_runs,estimated_stamina,status,created_at,updated_at FROM farming_recommendations WHERE 1=1`
	var args []any
	if f.GoalID != 0 {
		q += ` AND goal_id=?`
		args = append(args, f.GoalID)
	}
	if f.GameID != "" {
		q += ` AND game_id=?`
		args = append(args, f.GameID)
	}
	if f.Status != "" {
		q += ` AND status=?`
		args = append(args, f.Status)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q += fmt.Sprintf(` ORDER BY priority DESC,id DESC LIMIT %d`, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FarmingRecommendation{}
	for rows.Next() {
		var r FarmingRecommendation
		if err := rows.Scan(&r.ID, &r.GoalID, &r.GameID, &r.MaterialID, &r.RouteID, &r.TaskID, &r.RecommendationType, &r.Title, &r.Reason, &r.Priority, &r.EstimatedRuns, &r.EstimatedStamina, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ClearFarmingRecommendations(goalID int64) error {
	_, err := s.db.Exec(`DELETE FROM farming_recommendations WHERE goal_id=? AND status='open' AND task_id IS NULL`, goalID)
	return err
}

func (s *Store) SetFarmingRecommendationStatus(id int64, status string) (FarmingRecommendation, error) {
	rec, err := s.GetFarmingRecommendation(id)
	if err != nil {
		return FarmingRecommendation{}, err
	}
	rec.Status = status
	return s.UpdateFarmingRecommendation(rec)
}

func (s *Store) SetFarmingRecommendationTask(id, taskID int64) (FarmingRecommendation, error) {
	rec, err := s.GetFarmingRecommendation(id)
	if err != nil {
		return FarmingRecommendation{}, err
	}
	rec.TaskID = &taskID
	if rec.Status == "open" {
		rec.Status = "task_created"
	}
	return s.UpdateFarmingRecommendation(rec)
}
