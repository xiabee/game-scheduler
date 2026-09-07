package store

import (
	"strings"
	"testing"
)

func sampleDataset() PlannerDataset {
	return PlannerDataset{
		Characters: []Character{
			{ID: 7, Name: "香菱", RoleType: "dps", Tags: []string{"pyro"}},
		},
		Goals: []CharacterGoal{
			{ID: 70, CharacterID: 7, Name: "突破90", Priority: 5},
		},
		Materials: []MaterialItem{
			{ID: 700, Name: "绝云椒椒", Category: "collect", SourceHint: "绝云间"},
		},
		Requirements: []MaterialRequirement{
			{GoalID: 70, MaterialID: 700, RequiredCount: 10, OwnedCount: 2},
		},
	}
}

func plannerCounts(t *testing.T, s *Store, gameID string) (chars, goals, mats, reqs int) {
	t.Helper()
	c, _ := s.ListCharacters(CharacterFilter{GameID: gameID})
	g, _ := s.ListCharacterGoals(CharacterGoalFilter{GameID: gameID})
	m, _ := s.ListMaterialItems(MaterialFilter{GameID: gameID})
	var r int
	for _, gg := range g {
		reqs2, _ := s.ListMaterialRequirements(MaterialRequirementFilter{GoalID: gg.ID})
		r += len(reqs2)
	}
	return len(c), len(g), len(m), r
}

func TestImportPlannerDataCommit(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "genshin")

	res, err := s.ImportPlannerData("genshin", sampleDataset(), false, false)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 4 || res.Updated != 0 || res.Skipped != 0 {
		t.Fatalf("result=%+v", res)
	}
	chars, goals, mats, reqs := plannerCounts(t, s, "genshin")
	if chars != 1 || goals != 1 || mats != 1 || reqs != 1 {
		t.Fatalf("counts chars=%d goals=%d mats=%d reqs=%d", chars, goals, mats, reqs)
	}
	// references remapped to real ids
	c, _ := s.ListCharacters(CharacterFilter{GameID: "genshin"})
	g, _ := s.ListCharacterGoals(CharacterGoalFilter{GameID: "genshin"})
	if g[0].CharacterID != c[0].ID {
		t.Fatalf("goal character_id not remapped: %+v %+v", g[0], c[0])
	}
	m, _ := s.ListMaterialItems(MaterialFilter{GameID: "genshin"})
	r, _ := s.ListMaterialRequirements(MaterialRequirementFilter{GoalID: g[0].ID})
	if r[0].MaterialID != m[0].ID {
		t.Fatalf("requirement material_id not remapped: %+v", r[0])
	}
	if c[0].Tags == nil || len(c[0].Tags) != 1 || c[0].Tags[0] != "pyro" {
		t.Fatalf("tags lost: %+v", c[0])
	}
}

func TestImportPlannerDataDryRunWritesNothing(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "genshin")

	res, err := s.ImportPlannerData("genshin", sampleDataset(), true, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Created != 4 {
		t.Fatalf("dry run counts=%+v", res)
	}
	if chars, goals, mats, reqs := plannerCounts(t, s, "genshin"); chars != 0 || goals != 0 || mats != 0 || reqs != 0 {
		t.Fatalf("dry run wrote rows: chars=%d goals=%d mats=%d reqs=%d", chars, goals, mats, reqs)
	}
	// dry-run must not consume autoincrement ids either: the real import after
	// it starts from the same first ids.
	if _, err := s.ImportPlannerData("genshin", sampleDataset(), false, false); err != nil {
		t.Fatalf("real import after dry run: %v", err)
	}
}

func TestImportPlannerDataRollbackOnFailure(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "genshin")

	// goal references a character id that is not in the file: the import fails
	// after characters and materials were already written, and must leave
	// nothing behind.
	d := sampleDataset()
	d.Goals[0].CharacterID = 999
	if _, err := s.ImportPlannerData("genshin", d, false, false); err == nil {
		t.Fatal("expected unresolved character_id to fail the import")
	}
	if chars, goals, mats, reqs := plannerCounts(t, s, "genshin"); chars != 0 || goals != 0 || mats != 0 || reqs != 0 {
		t.Fatalf("failed import left partial data: chars=%d goals=%d mats=%d reqs=%d", chars, goals, mats, reqs)
	}

	// dry run on the broken file reports the same failure.
	if _, err := s.ImportPlannerData("genshin", d, true, false); err == nil {
		t.Fatal("expected dry run of broken file to fail too")
	}
	if chars, goals, mats, reqs := plannerCounts(t, s, "genshin"); chars != 0 || goals != 0 || mats != 0 || reqs != 0 {
		t.Fatalf("dry run wrote rows: chars=%d goals=%d mats=%d reqs=%d", chars, goals, mats, reqs)
	}
}

func TestImportPlannerDataUpsertUpdates(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "genshin")

	if _, err := s.ImportPlannerData("genshin", sampleDataset(), false, false); err != nil {
		t.Fatalf("import: %v", err)
	}
	d := sampleDataset()
	d.Characters[0].RoleType = "support"
	d.Requirements[0].OwnedCount = 5

	// upsert re-import: updates everything, no duplicates
	res, err := s.ImportPlannerData("genshin", d, false, true)
	if err != nil {
		t.Fatalf("upsert import: %v", err)
	}
	if res.Created != 0 || res.Updated != 4 || res.Skipped != 0 {
		t.Fatalf("upsert result=%+v", res)
	}
	chars, goals, mats, reqs := plannerCounts(t, s, "genshin")
	if chars != 1 || goals != 1 || mats != 1 || reqs != 1 {
		t.Fatalf("upsert duplicated rows: chars=%d goals=%d mats=%d reqs=%d", chars, goals, mats, reqs)
	}
	c, _ := s.ListCharacters(CharacterFilter{GameID: "genshin"})
	if c[0].RoleType != "support" {
		t.Fatalf("upsert did not persist change: %+v", c[0])
	}
	g, _ := s.ListCharacterGoals(CharacterGoalFilter{GameID: "genshin"})
	r, _ := s.ListMaterialRequirements(MaterialRequirementFilter{GoalID: g[0].ID})
	if r[0].OwnedCount != 5 {
		t.Fatalf("requirement not updated: %+v", r[0])
	}

	// non-upsert re-import skips existing rows
	res, err = s.ImportPlannerData("genshin", d, false, false)
	if err != nil {
		t.Fatalf("skip import: %v", err)
	}
	if res.Created != 0 || res.Skipped != 4 {
		t.Fatalf("skip result=%+v", res)
	}
	if chars, _, _, _ := plannerCounts(t, s, "genshin"); chars != 1 {
		t.Fatalf("non-upsert import duplicated characters")
	}
}

func TestImportPlannerDataInFileDuplicatesCollapse(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "genshin")

	// Two rows share a name: the second collapses onto the first row instead
	// of creating a duplicate (defense in depth; the API validator rejects
	// such files earlier).
	d := PlannerDataset{
		Characters: []Character{
			{ID: 1, Name: "香菱"},
			{ID: 2, Name: "香菱"},
		},
		Goals: []CharacterGoal{
			{ID: 10, CharacterID: 1, Name: "dup"},
			{ID: 11, CharacterID: 2, Name: "dup"},
		},
		Materials: []MaterialItem{
			{ID: 100, Name: "椒椒"},
			{ID: 101, Name: "椒椒"},
		},
		Requirements: []MaterialRequirement{
			{GoalID: 10, MaterialID: 100, RequiredCount: 1},
		},
	}
	if _, err := s.ImportPlannerData("genshin", d, false, false); err != nil {
		t.Fatalf("import: %v", err)
	}
	chars, goals, mats, reqs := plannerCounts(t, s, "genshin")
	if chars != 1 || goals != 1 || mats != 1 || reqs != 1 {
		t.Fatalf("in-file duplicates created rows: chars=%d goals=%d mats=%d reqs=%d", chars, goals, mats, reqs)
	}
}

func TestImportPlannerDataUnresolvedRequirementFailsAtomically(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "genshin")

	// requirement references a material id not in the file
	d := sampleDataset()
	d.Requirements[0].MaterialID = 424242
	_, err := s.ImportPlannerData("genshin", d, false, false)
	if err == nil || !strings.Contains(err.Error(), "material_id") {
		t.Fatalf("want unresolved material_id error, got %v", err)
	}
	if chars, _, _, _ := plannerCounts(t, s, "genshin"); chars != 0 {
		t.Fatalf("failed import left characters behind")
	}
}

func TestExportPlannerDataSnapshot(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "genshin")

	// empty game: empty (non-nil) sections
	d, err := s.ExportPlannerData("genshin")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if d.Characters == nil || d.Goals == nil || d.Materials == nil || d.Requirements == nil {
		t.Fatalf("export sections must be non-nil: %+v", d)
	}

	if _, err := s.ImportPlannerData("genshin", sampleDataset(), false, false); err != nil {
		t.Fatalf("import: %v", err)
	}
	d, err = s.ExportPlannerData("genshin")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(d.Characters) != 1 || len(d.Goals) != 1 || len(d.Materials) != 1 || len(d.Requirements) != 1 {
		t.Fatalf("counts=%+v", d)
	}
	// references resolve within the snapshot
	if d.Goals[0].CharacterID != d.Characters[0].ID || d.Requirements[0].GoalID != d.Goals[0].ID || d.Requirements[0].MaterialID != d.Materials[0].ID {
		t.Fatalf("snapshot references broken: %+v", d)
	}
	// a game that does not exist yields the same empty snapshot, not an error
	d2, err := s.ExportPlannerData("nope")
	if err != nil {
		t.Fatalf("export unknown game: %v", err)
	}
	if len(d2.Characters) != 0 {
		t.Fatalf("unknown game returned rows: %+v", d2)
	}
}

// A partial file in upsert mode must not wipe stored fields the file did not
// carry; a full-file roundtrip still overwrites everything.
func TestImportPlannerUpsertPartialFileKeepsStoredFields(t *testing.T) {
	s := newTestStore(t)
	mkGame(t, s, "genshin")

	full := PlannerDataset{
		Characters: []Character{
			{ID: 7, Name: "香菱", RoleType: "dps", Element: "pyro", Weapon: "polearm", Rarity: 4, Tags: []string{"national"}, Notes: "old note"},
		},
		Goals: []CharacterGoal{
			{ID: 70, CharacterID: 7, Name: "突破90", TargetLevel: "90", Priority: 5, Status: "in_progress", Notes: "goal note"},
		},
		Materials: []MaterialItem{
			{ID: 700, Name: "绝云椒椒", Category: "collect", SourceHint: "绝云间", RouteTypeHint: "collect", Notes: "mat note"},
		},
		Requirements: []MaterialRequirement{
			{GoalID: 70, MaterialID: 700, RequiredCount: 10, OwnedCount: 2, Priority: 3, Notes: "req note"},
		},
	}
	if _, err := s.ImportPlannerData("genshin", full, false, false); err != nil {
		t.Fatalf("full import: %v", err)
	}

	// Same names, but every optional field is empty/zero.
	partial := PlannerDataset{
		Characters:   []Character{{ID: 7, Name: "香菱"}},
		Goals:        []CharacterGoal{{ID: 70, CharacterID: 7, Name: "突破90"}},
		Materials:    []MaterialItem{{ID: 700, Name: "绝云椒椒"}},
		Requirements: []MaterialRequirement{{GoalID: 70, MaterialID: 700, RequiredCount: 10}},
	}
	res, err := s.ImportPlannerData("genshin", partial, false, true)
	if err != nil {
		t.Fatalf("partial upsert: %v", err)
	}
	if res.Updated != 4 || res.Created != 0 {
		t.Fatalf("partial upsert counts=%+v", res)
	}

	chars, _ := s.ListCharacters(CharacterFilter{GameID: "genshin"})
	c := chars[0]
	if c.RoleType != "dps" || c.Element != "pyro" || c.Weapon != "polearm" || c.Rarity != 4 || len(c.Tags) != 1 || c.Notes != "old note" {
		t.Fatalf("character fields wiped by partial upsert: %+v", c)
	}
	goals, _ := s.ListCharacterGoals(CharacterGoalFilter{GameID: "genshin"})
	g := goals[0]
	if g.TargetLevel != "90" || g.Priority != 5 || g.Status != "in_progress" || g.Notes != "goal note" {
		t.Fatalf("goal fields wiped by partial upsert: %+v", g)
	}
	mats, _ := s.ListMaterialItems(MaterialFilter{GameID: "genshin"})
	m := mats[0]
	if m.Category != "collect" || m.SourceHint != "绝云间" || m.RouteTypeHint != "collect" || m.Notes != "mat note" {
		t.Fatalf("material fields wiped by partial upsert: %+v", m)
	}

	// A full file still overwrites every field.
	full.Characters[0].Notes = "new note"
	full.Goals[0].Status = "done"
	if _, err := s.ImportPlannerData("genshin", full, false, true); err != nil {
		t.Fatalf("full upsert: %v", err)
	}
	chars, _ = s.ListCharacters(CharacterFilter{GameID: "genshin"})
	goals, _ = s.ListCharacterGoals(CharacterGoalFilter{GameID: "genshin"})
	if chars[0].Notes != "new note" || goals[0].Status != "done" {
		t.Fatalf("full upsert did not overwrite: %+v %+v", chars[0], goals[0])
	}
}
