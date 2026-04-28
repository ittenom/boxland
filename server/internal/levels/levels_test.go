package levels_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/levels"
	"boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/worlds"
)

func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// fixture builds a designer + a base map + a base entity_type for
// placement tests.
func fixture(t *testing.T, pool *pgxpool.Pool) (designerID, mapID, entityID int64) {
	t.Helper()
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "levels-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	mp := maps.New(pool)
	m, err := mp.Create(context.Background(), maps.CreateInput{
		Name: "test-map", Width: 10, Height: 10, CreatedBy: d.ID,
	})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	es := entities.New(pool, components.Default())
	et, err := es.Create(context.Background(), entities.CreateInput{
		Name: "spawn-point", EntityClass: entities.ClassLogic, CreatedBy: d.ID,
	})
	if err != nil {
		t.Fatalf("create entity type: %v", err)
	}
	return d.ID, m.ID, et.ID
}

func TestCreate_AppliesDefaults(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, mID, _ := fixture(t, pool)
	svc := levels.New(pool)

	lv, err := svc.Create(context.Background(), levels.CreateInput{
		Name: "Town", MapID: mID, CreatedBy: dID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if lv.InstancingMode != "shared" || lv.PersistenceMode != "persistent" || lv.SpectatorPolicy != "public" {
		t.Errorf("defaults wrong: %+v", lv)
	}
	if lv.Public {
		t.Errorf("default public should be false")
	}
}

func TestCreate_RejectsMissingMap(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, _, _ := fixture(t, pool)
	svc := levels.New(pool)

	_, err := svc.Create(context.Background(), levels.CreateInput{
		Name: "x", CreatedBy: dID,
	})
	if !errors.Is(err, levels.ErrMapMissing) {
		t.Fatalf("want ErrMapMissing, got %v", err)
	}
}

func TestCreate_DuplicateNameRejected(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, mID, _ := fixture(t, pool)
	svc := levels.New(pool)

	if _, err := svc.Create(context.Background(), levels.CreateInput{Name: "dup", MapID: mID, CreatedBy: dID}); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := svc.Create(context.Background(), levels.CreateInput{Name: "dup", MapID: mID, CreatedBy: dID})
	if !errors.Is(err, levels.ErrNameInUse) {
		t.Fatalf("want ErrNameInUse, got %v", err)
	}
}

func TestPlaceEntity_RoundTrip(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, mID, etID := fixture(t, pool)
	svc := levels.New(pool)
	ctx := context.Background()

	lv, _ := svc.Create(ctx, levels.CreateInput{Name: "lv", MapID: mID, CreatedBy: dID})

	le, err := svc.PlaceEntity(ctx, levels.PlaceEntityInput{
		LevelID: lv.ID, EntityTypeID: etID,
		X: 3, Y: 4, RotationDegrees: 90,
	})
	if err != nil {
		t.Fatalf("PlaceEntity: %v", err)
	}
	if le.X != 3 || le.Y != 4 || le.RotationDegrees != 90 {
		t.Errorf("got %+v", le)
	}

	got, err := svc.ListEntities(ctx, lv.ID)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 placement, got %d", len(got))
	}

	if err := svc.MoveEntity(ctx, le.ID, 5, 6, 0); err != nil {
		t.Fatalf("MoveEntity: %v", err)
	}
	rect, _ := svc.EntitiesInRect(ctx, lv.ID, 5, 6, 5, 6)
	if len(rect) != 1 {
		t.Fatalf("expected 1 in rect after move, got %d", len(rect))
	}
	if rect[0].X != 5 || rect[0].Y != 6 || rect[0].RotationDegrees != 0 {
		t.Errorf("after move: %+v", rect[0])
	}

	if err := svc.RemoveEntity(ctx, le.ID); err != nil {
		t.Fatalf("RemoveEntity: %v", err)
	}
	got, _ = svc.ListEntities(ctx, lv.ID)
	if len(got) != 0 {
		t.Errorf("expected 0 placements after remove, got %d", len(got))
	}
}

func TestSetWorld_AttachAndDetach(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, mID, _ := fixture(t, pool)
	svc := levels.New(pool)
	wsvc := worlds.New(pool)
	ctx := context.Background()

	lv, _ := svc.Create(ctx, levels.CreateInput{Name: "lv", MapID: mID, CreatedBy: dID})
	w, _ := wsvc.Create(ctx, worlds.CreateInput{Name: "Realm", CreatedBy: dID})

	if err := svc.SetWorld(ctx, lv.ID, &w.ID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	got, _ := svc.FindByID(ctx, lv.ID)
	if got.WorldID == nil || *got.WorldID != w.ID {
		t.Errorf("world_id = %v, want %d", got.WorldID, w.ID)
	}

	if err := svc.SetWorld(ctx, lv.ID, nil); err != nil {
		t.Fatalf("detach: %v", err)
	}
	got, _ = svc.FindByID(ctx, lv.ID)
	if got.WorldID != nil {
		t.Errorf("expected world_id nil after detach, got %v", *got.WorldID)
	}
}

func TestSpectatorInvites_LifeCycle(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, mID, _ := fixture(t, pool)
	svc := levels.New(pool)
	ctx := context.Background()

	lv, _ := svc.Create(ctx, levels.CreateInput{
		Name: "private", MapID: mID, SpectatorPolicy: "invite", CreatedBy: dID,
	})

	// Insert a player so the FK on level_spectator_invites is happy.
	var playerID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO players (email, password_hash, email_verified)
		VALUES ('p@x.com', 'hash', true) RETURNING id
	`).Scan(&playerID); err != nil {
		t.Fatalf("seed player: %v", err)
	}

	if err := svc.GrantSpectatorInvite(ctx, lv.ID, playerID, dID); err != nil {
		t.Fatalf("grant: %v", err)
	}
	allowed, err := svc.IsPlayerSpectatorAllowed(ctx, lv.ID, playerID, "invite")
	if err != nil || !allowed {
		t.Errorf("invited player should be allowed: %v %v", allowed, err)
	}
	allowed, _ = svc.IsPlayerSpectatorAllowed(ctx, lv.ID, playerID, "private")
	if allowed {
		t.Errorf("private should never be allowed")
	}

	if err := svc.RevokeSpectatorInvite(ctx, lv.ID, playerID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	allowed, _ = svc.IsPlayerSpectatorAllowed(ctx, lv.ID, playerID, "invite")
	if allowed {
		t.Errorf("revoked player should not be allowed")
	}
}

func TestCreate_InvalidInstancingRejected(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, mID, _ := fixture(t, pool)
	svc := levels.New(pool)

	_, err := svc.Create(context.Background(), levels.CreateInput{
		Name: "bad", MapID: mID, InstancingMode: "weird", CreatedBy: dID,
	})
	if !errors.Is(err, levels.ErrInvalidMode) {
		t.Fatalf("want ErrInvalidMode, got %v", err)
	}
}
