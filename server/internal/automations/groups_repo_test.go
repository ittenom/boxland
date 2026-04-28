package automations_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/automations"
	"boxland/server/internal/levels"
	"boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
)

// openTestPool returns an isolated, freshly-migrated DB. testdb.New wires its own t.Cleanup that drops the database when the test ends.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// seedLevel creates per-test fixtures (a designer + map + level). The
// action_groups table now keys off levels.id (post-redesign), so the
// returned id is a level id. The pool is already empty because
// testdb.New(t) returns a fresh database for every test.
func seedLevel(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "groups-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	mapsSvc := maps.New(pool)
	m, err := mapsSvc.Create(context.Background(), maps.CreateInput{
		Name: "groups-test-map", Width: 16, Height: 16, CreatedBy: d.ID,
	})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	lv, err := levels.New(pool).Create(context.Background(), levels.CreateInput{
		Name: "groups-test-level", MapID: m.ID, CreatedBy: d.ID,
	})
	if err != nil {
		t.Fatalf("create level: %v", err)
	}
	return lv.ID
}

func actionsJSON(t *testing.T, nodes []automations.ActionNode) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGroupsRepo_UpsertAndLoadCompiled(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	levelID := seedLevel(t, pool)

	repo := automations.NewGroupsRepo(pool)
	ctx := context.Background()

	// award_xp -> spawn 5
	spawnCfg, _ := json.Marshal(map[string]any{"type_id": 5})
	awardActions := []automations.ActionNode{
		{Kind: "spawn", Config: spawnCfg},
	}
	if _, err := repo.Upsert(ctx, levelID, "award_xp", actionsJSON(t, awardActions)); err != nil {
		t.Fatalf("upsert award_xp: %v", err)
	}

	// victory -> call award_xp + spawn 5
	callCfg, _ := json.Marshal(map[string]any{"name": "award_xp"})
	victoryActions := []automations.ActionNode{
		{Kind: "call_action_group", Config: callCfg},
		{Kind: "spawn", Config: spawnCfg},
	}
	if _, err := repo.Upsert(ctx, levelID, "victory", actionsJSON(t, victoryActions)); err != nil {
		t.Fatalf("upsert victory: %v", err)
	}

	compiled, err := repo.LoadCompiled(ctx, levelID, automations.DefaultActions())
	if err != nil {
		t.Fatalf("LoadCompiled: %v", err)
	}
	if len(compiled) != 2 {
		t.Fatalf("got %d compiled groups, want 2", len(compiled))
	}
	if got := compiled["victory"].Actions[0].Kind; got != "call_action_group" {
		t.Errorf("victory[0].Kind = %q", got)
	}
}

func TestGroupsRepo_LoadCompiled_RejectsCycle(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	levelID := seedLevel(t, pool)

	repo := automations.NewGroupsRepo(pool)
	ctx := context.Background()

	// a -> b, b -> a
	cfgA, _ := json.Marshal(map[string]any{"name": "b"})
	cfgB, _ := json.Marshal(map[string]any{"name": "a"})
	if _, err := repo.Upsert(ctx, levelID, "a", actionsJSON(t, []automations.ActionNode{
		{Kind: "call_action_group", Config: cfgA},
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Upsert(ctx, levelID, "b", actionsJSON(t, []automations.ActionNode{
		{Kind: "call_action_group", Config: cfgB},
	})); err != nil {
		t.Fatal(err)
	}
	_, err := repo.LoadCompiled(ctx, levelID, automations.DefaultActions())
	if !errors.Is(err, automations.ErrActionGroupCycle) {
		t.Fatalf("LoadCompiled with cycle: want ErrActionGroupCycle, got %v", err)
	}
}

func TestGroupsRepo_TenantIsolation(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	levelID := seedLevel(t, pool)

	repo := automations.NewGroupsRepo(pool)
	ctx := context.Background()

	spawnCfg, _ := json.Marshal(map[string]any{"type_id": 5})
	if _, err := repo.Upsert(ctx, levelID, "award_xp", actionsJSON(t, []automations.ActionNode{
		{Kind: "spawn", Config: spawnCfg},
	})); err != nil {
		t.Fatal(err)
	}

	// A second level under a different designer must see zero groups.
	auth := authdesigner.New(pool)
	d2, err := auth.CreateDesigner(ctx, "other-designer@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	mapsSvc := maps.New(pool)
	m2, err := mapsSvc.Create(ctx, maps.CreateInput{
		Name: "other-map", Width: 16, Height: 16, CreatedBy: d2.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	lv2, err := levels.New(pool).Create(ctx, levels.CreateInput{
		Name: "other-level", MapID: m2.ID, CreatedBy: d2.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.ListByLevel(ctx, lv2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("cross-realm leak: got %d rows on a fresh level", len(other))
	}
}

func TestGroupsRepo_DeleteIsIdempotent(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	levelID := seedLevel(t, pool)

	repo := automations.NewGroupsRepo(pool)
	ctx := context.Background()
	if _, err := repo.Upsert(ctx, levelID, "g", actionsJSON(t, nil)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, levelID, "g"); err != nil {
		t.Fatal(err)
	}
	// Second delete should report not-found explicitly (typed error).
	if err := repo.Delete(ctx, levelID, "g"); !errors.Is(err, automations.ErrGroupNotFound) {
		t.Errorf("second delete: want ErrGroupNotFound, got %v", err)
	}
}
