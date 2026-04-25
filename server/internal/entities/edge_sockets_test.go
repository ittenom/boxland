package entities_test

import (
	"context"
	"errors"
	"testing"

	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
)

func TestEdgeSocket_CRUD(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	field, err := svc.CreateSocket(ctx, "field", 0xff00ffff, designerID)
	if err != nil {
		t.Fatalf("CreateSocket: %v", err)
	}
	if field.ID == 0 {
		t.Errorf("ID should be assigned")
	}

	got, err := svc.FindSocketByName(ctx, "field")
	if err != nil {
		t.Fatalf("FindSocketByName: %v", err)
	}
	if got.ID != field.ID {
		t.Errorf("name lookup found wrong socket: %+v", got)
	}

	all, err := svc.ListSockets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "field" {
		t.Errorf("List: got %+v", all)
	}

	if err := svc.DeleteSocket(ctx, field.ID); err != nil {
		t.Fatalf("DeleteSocket: %v", err)
	}
	if _, err := svc.FindSocketByID(ctx, field.ID); !errors.Is(err, entities.ErrSocketNotFound) {
		t.Errorf("expected ErrSocketNotFound, got %v", err)
	}
}

func TestEdgeSocket_DuplicateNameRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	if _, err := svc.CreateSocket(ctx, "stone", 0, designerID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSocket(ctx, "stone", 0, designerID); !errors.Is(err, entities.ErrSocketNameInUse) {
		t.Errorf("got %v, want ErrSocketNameInUse", err)
	}
}

func TestSetTileEdges_RoundTrip(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	et, _ := svc.Create(ctx, entities.CreateInput{Name: "wall_n", CreatedBy: designerID})
	stone, _ := svc.CreateSocket(ctx, "stone", 0, designerID)
	field, _ := svc.CreateSocket(ctx, "field", 0, designerID)

	if err := svc.SetTileEdges(ctx, et.ID, &stone.ID, &field.ID, &field.ID, &stone.ID); err != nil {
		t.Fatalf("SetTileEdges: %v", err)
	}
	got, err := svc.TileEdges(ctx, et.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NorthSocketID == nil || *got.NorthSocketID != stone.ID {
		t.Errorf("north: got %v", got.NorthSocketID)
	}
	if got.EastSocketID == nil || *got.EastSocketID != field.ID {
		t.Errorf("east: got %v", got.EastSocketID)
	}
}

func TestSetTileEdges_NullsAreAllowed(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	et, _ := svc.Create(ctx, entities.CreateInput{Name: "halftile", CreatedBy: designerID})

	if err := svc.SetTileEdges(ctx, et.ID, nil, nil, nil, nil); err != nil {
		t.Fatalf("SetTileEdges with all nils: %v", err)
	}
	got, _ := svc.TileEdges(ctx, et.ID)
	if got.NorthSocketID != nil || got.EastSocketID != nil ||
		got.SouthSocketID != nil || got.WestSocketID != nil {
		t.Errorf("expected all nil sockets, got %+v", got)
	}
}

func TestSocketDelete_NullsAssignmentsButKeepsEntity(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	et, _ := svc.Create(ctx, entities.CreateInput{Name: "tile_x", CreatedBy: designerID})
	stone, _ := svc.CreateSocket(ctx, "stone", 0, designerID)
	_ = svc.SetTileEdges(ctx, et.ID, &stone.ID, &stone.ID, &stone.ID, &stone.ID)

	if err := svc.DeleteSocket(ctx, stone.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.TileEdges(ctx, et.ID)
	if got.NorthSocketID != nil {
		t.Errorf("expected ON DELETE SET NULL to clear; got %+v", got)
	}
	// Entity row still exists.
	if _, err := svc.FindByID(ctx, et.ID); err != nil {
		t.Errorf("entity should still exist after socket delete: %v", err)
	}
}
