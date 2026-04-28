package worlds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/levels"
	"boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/worlds"
)

func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

func fixture(t *testing.T, pool *pgxpool.Pool) (designerID int64) {
	t.Helper()
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "worlds-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	return d.ID
}

func TestCreate_HappyPath(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID := fixture(t, pool)
	svc := worlds.New(pool)

	w, err := svc.Create(context.Background(), worlds.CreateInput{
		Name: "Mainland", CreatedBy: dID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.Name != "Mainland" || w.StartLevelID != nil {
		t.Errorf("got %+v", w)
	}
}

func TestCreate_DuplicateNameRejected(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID := fixture(t, pool)
	svc := worlds.New(pool)

	if _, err := svc.Create(context.Background(), worlds.CreateInput{Name: "dup", CreatedBy: dID}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(context.Background(), worlds.CreateInput{Name: "dup", CreatedBy: dID})
	if !errors.Is(err, worlds.ErrNameInUse) {
		t.Fatalf("want ErrNameInUse, got %v", err)
	}
}

func TestSetStartLevel_ClearsToNil(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID := fixture(t, pool)
	ctx := context.Background()

	mp := maps.New(pool)
	m, _ := mp.Create(ctx, maps.CreateInput{Name: "m", Width: 4, Height: 4, CreatedBy: dID})
	lvSvc := levels.New(pool)
	lv, _ := lvSvc.Create(ctx, levels.CreateInput{Name: "Start", MapID: m.ID, CreatedBy: dID})

	wsvc := worlds.New(pool)
	w, _ := wsvc.Create(ctx, worlds.CreateInput{Name: "World", CreatedBy: dID})

	if err := wsvc.SetStartLevel(ctx, w.ID, &lv.ID); err != nil {
		t.Fatalf("SetStartLevel: %v", err)
	}
	got, _ := wsvc.FindByID(ctx, w.ID)
	if got.StartLevelID == nil || *got.StartLevelID != lv.ID {
		t.Errorf("start_level_id = %v, want %d", got.StartLevelID, lv.ID)
	}

	if err := wsvc.SetStartLevel(ctx, w.ID, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = wsvc.FindByID(ctx, w.ID)
	if got.StartLevelID != nil {
		t.Errorf("expected nil after clear, got %v", *got.StartLevelID)
	}
}

// Deleting a world should NOT cascade-delete the levels in it; their
// world_id just becomes NULL.
func TestDelete_LevelsSurvive(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID := fixture(t, pool)
	ctx := context.Background()

	mp := maps.New(pool)
	m, _ := mp.Create(ctx, maps.CreateInput{Name: "m", Width: 4, Height: 4, CreatedBy: dID})
	lvSvc := levels.New(pool)
	wsvc := worlds.New(pool)

	w, _ := wsvc.Create(ctx, worlds.CreateInput{Name: "Realm", CreatedBy: dID})
	lv, _ := lvSvc.Create(ctx, levels.CreateInput{
		Name: "Town", MapID: m.ID, WorldID: &w.ID, CreatedBy: dID,
	})

	if err := wsvc.Delete(ctx, w.ID); err != nil {
		t.Fatalf("delete world: %v", err)
	}

	got, err := lvSvc.FindByID(ctx, lv.ID)
	if err != nil {
		t.Fatalf("level survived check: %v", err)
	}
	if got.WorldID != nil {
		t.Errorf("expected world_id NULL after world delete, got %v", *got.WorldID)
	}
}
