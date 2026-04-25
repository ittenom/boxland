package entities_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
)

func TestTileGroup_CreateInitializesEmptyLayout(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())

	tg, err := svc.CreateTileGroup(context.Background(), entities.CreateTileGroupInput{
		Name: "doorway", Width: 3, Height: 2, CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("CreateTileGroup: %v", err)
	}
	var got entities.Layout
	if err := json.Unmarshal(tg.LayoutJSON, &got); err != nil {
		t.Fatalf("decode layout: %v", err)
	}
	if len(got) != 2 || len(got[0]) != 3 {
		t.Errorf("expected 2x3 layout, got %dx%d", len(got), len(got[0]))
	}
	for _, row := range got {
		for _, v := range row {
			if v != 0 {
				t.Errorf("expected zero-init, got %d", v)
			}
		}
	}
}

func TestTileGroup_DimensionBoundsEnforced(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())

	for _, badIn := range []entities.CreateTileGroupInput{
		{Name: "z", Width: 0, Height: 1, CreatedBy: designerID},
		{Name: "z", Width: 1, Height: 0, CreatedBy: designerID},
		{Name: "z", Width: 17, Height: 1, CreatedBy: designerID},
	} {
		if _, err := svc.CreateTileGroup(context.Background(), badIn); err == nil {
			t.Errorf("expected dimension error for %+v", badIn)
		}
	}
}

func TestTileGroup_UpdateLayoutEnforcesDimensions(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	tg, _ := svc.CreateTileGroup(ctx, entities.CreateTileGroupInput{
		Name: "g", Width: 2, Height: 2, CreatedBy: designerID,
	})

	good := entities.Layout{{1, 2}, {3, 4}}
	if err := svc.UpdateTileGroupLayout(ctx, tg.ID, good); err != nil {
		t.Fatalf("good update failed: %v", err)
	}

	bad := entities.Layout{{1, 2, 3}, {4, 5, 6}} // wrong width
	if err := svc.UpdateTileGroupLayout(ctx, tg.ID, bad); !errors.Is(err, entities.ErrLayoutSize) {
		t.Errorf("got %v, want ErrLayoutSize", err)
	}
}

func TestTileGroup_DeleteAndNotFound(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	tg, _ := svc.CreateTileGroup(ctx, entities.CreateTileGroupInput{
		Name: "doomed", Width: 1, Height: 1, CreatedBy: designerID,
	})
	if err := svc.DeleteTileGroup(ctx, tg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.FindTileGroupByID(ctx, tg.ID); !errors.Is(err, entities.ErrTileGroupNotFound) {
		t.Errorf("got %v, want ErrTileGroupNotFound", err)
	}
}
