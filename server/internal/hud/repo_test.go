package hud_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/hud"
	"boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
)

// openPool returns an isolated, freshly-migrated DB. testdb.New wires its own t.Cleanup that drops the database when the test ends.
func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

func seed(t *testing.T, pool *pgxpool.Pool, name string) (designerID, mapID int64) {
	t.Helper()
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), name+"@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	mapsSvc := maps.New(pool)
	m, err := mapsSvc.Create(context.Background(), maps.CreateInput{
		Name: "hud-test-" + name, Width: 16, Height: 16, CreatedBy: d.ID,
	})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	return d.ID, m.ID
}

func TestRepo_GetEmpty_ReturnsCanonicalEmpty(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()

	designerID, mapID := seed(t, pool, "empty")
	repo := &hud.Repo{Pool: pool}
	got, err := repo.Get(context.Background(), mapID, designerID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.V != hud.LayoutVersion {
		t.Errorf("V=%d, want %d", got.V, hud.LayoutVersion)
	}
	if len(got.Anchors) != 0 {
		t.Errorf("expected empty anchors, got %d", len(got.Anchors))
	}
}

func TestRepo_Mutate_AddsAndPersists(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()

	designerID, mapID := seed(t, pool, "mutate")
	repo := &hud.Repo{Pool: pool}
	reg := hud.DefaultRegistry()

	if _, err := repo.Mutate(context.Background(), mapID, designerID, func(l *hud.Layout) error {
		_, err := l.AddWidget(hud.AnchorBottomLeft, hud.WidgetMiniClock, reg)
		return err
	}); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	// Re-load and confirm the widget landed.
	got, err := repo.Get(context.Background(), mapID, designerID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stack, ok := got.Anchors[hud.AnchorBottomLeft]
	if !ok {
		t.Fatal("anchor missing")
	}
	if len(stack.Widgets) != 1 {
		t.Errorf("widget count = %d, want 1", len(stack.Widgets))
	}
}

func TestRepo_TenantIsolation(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()

	dA, mapA := seed(t, pool, "alice")
	dB, mapB := seed(t, pool, "bob")
	_ = mapB

	repo := &hud.Repo{Pool: pool}
	reg := hud.DefaultRegistry()

	// Alice mutates her own map (allowed).
	if _, err := repo.Mutate(context.Background(), mapA, dA, func(l *hud.Layout) error {
		_, err := l.AddWidget(hud.AnchorTopLeft, hud.WidgetMiniClock, reg)
		return err
	}); err != nil {
		t.Fatalf("alice mutate: %v", err)
	}
	// Bob tries to mutate Alice's map (denied).
	_, err := repo.Mutate(context.Background(), mapA, dB, func(l *hud.Layout) error {
		return nil
	})
	if !errors.Is(err, hud.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant access, got %v", err)
	}
	// Bob can read the public-player path (no owner filter), but NOT the
	// designer Get (which scopes by created_by).
	if _, err := repo.Get(context.Background(), mapA, dB); !errors.Is(err, hud.ErrNotFound) {
		t.Fatalf("expected ErrNotFound on cross-tenant Get, got %v", err)
	}
}

func TestRepo_GetForPlayer_ReturnsEmptyForUnknownMap(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()

	repo := &hud.Repo{Pool: pool}
	got, err := repo.GetForPlayer(context.Background(), 999_999_999)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.V != hud.LayoutVersion || len(got.Anchors) != 0 {
		t.Errorf("expected empty layout for unknown map, got %+v", got)
	}
}
