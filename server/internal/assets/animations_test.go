package assets_test

import (
	"context"
	"errors"
	"testing"

	"boxland/server/internal/assets"
)

func TestReplaceAnimations_HappyPath(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	a, err := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "hero", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	in := []assets.Animation{
		{Name: "walk_north", FrameFrom: 0, FrameTo: 3, FPS: 8, Direction: assets.DirForward},
		{Name: "walk_east", FrameFrom: 4, FrameTo: 7, FPS: 8, Direction: assets.DirForward},
		{Name: "idle", FrameFrom: 0, FrameTo: 0, FPS: 1, Direction: assets.DirForward},
	}
	if err := svc.ReplaceAnimations(ctx, a.ID, in); err != nil {
		t.Fatalf("ReplaceAnimations: %v", err)
	}
	got, err := svc.ListAnimations(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	// Stable order: by name ASC.
	wantOrder := []string{"idle", "walk_east", "walk_north"}
	for i, r := range got {
		if r.Name != wantOrder[i] {
			t.Errorf("row %d: got %q, want %q", i, r.Name, wantOrder[i])
		}
		if r.AssetID != a.ID {
			t.Errorf("row %d: AssetID = %d, want %d", i, r.AssetID, a.ID)
		}
	}
}

func TestReplaceAnimations_IsIdempotent(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	a, _ := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "hero", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})

	first := []assets.Animation{
		{Name: "walk_north", FrameFrom: 0, FrameTo: 3, FPS: 8},
		{Name: "attack", FrameFrom: 8, FrameTo: 11, FPS: 12},
	}
	if err := svc.ReplaceAnimations(ctx, a.ID, first); err != nil {
		t.Fatal(err)
	}
	// Replacement: drops `attack`, keeps `walk_north` (with new fps),
	// adds `idle`.
	second := []assets.Animation{
		{Name: "walk_north", FrameFrom: 0, FrameTo: 3, FPS: 10},
		{Name: "idle", FrameFrom: 0, FrameTo: 0, FPS: 1},
	}
	if err := svc.ReplaceAnimations(ctx, a.ID, second); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.ListAnimations(ctx, a.ID)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 after replacement", len(got))
	}
	for _, r := range got {
		if r.Name == "attack" {
			t.Errorf("attack should have been dropped")
		}
		if r.Name == "walk_north" && r.FPS != 10 {
			t.Errorf("walk_north: fps got %d, want 10 (overwrite)", r.FPS)
		}
	}
}

func TestReplaceAnimations_DedupsCollidingNames(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	a, _ := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "hero", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	// Same name twice; later wins. Importers occasionally do this on
	// malformed sidecars; the unique constraint would otherwise fail
	// the entire batch insert.
	in := []assets.Animation{
		{Name: "walk", FrameFrom: 0, FrameTo: 3, FPS: 8},
		{Name: "WALK", FrameFrom: 0, FrameTo: 7, FPS: 12},
	}
	if err := svc.ReplaceAnimations(ctx, a.ID, in); err != nil {
		t.Fatalf("dedup should succeed: %v", err)
	}
	got, _ := svc.ListAnimations(ctx, a.ID)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 after dedup", len(got))
	}
	if got[0].FrameTo != 7 || got[0].FPS != 12 {
		t.Errorf("last-wins lost: %+v", got[0])
	}
}

func TestReplaceAnimations_EmptyClearsExisting(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	a, _ := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "hero", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	_ = svc.ReplaceAnimations(ctx, a.ID, []assets.Animation{
		{Name: "walk", FrameFrom: 0, FrameTo: 3, FPS: 8},
	})
	if err := svc.ReplaceAnimations(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.ListAnimations(ctx, a.ID)
	if len(got) != 0 {
		t.Errorf("nil should clear; got %d rows", len(got))
	}
}

func TestReplaceAnimations_RejectsBadInputs(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()
	a, _ := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "hero", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	cases := []struct {
		name string
		in   []assets.Animation
	}{
		{"empty name", []assets.Animation{{Name: "", FrameFrom: 0, FrameTo: 1, FPS: 8}}},
		{"bad range", []assets.Animation{{Name: "x", FrameFrom: 5, FrameTo: 0, FPS: 8}}},
		{"fps too high", []assets.Animation{{Name: "x", FrameFrom: 0, FrameTo: 1, FPS: 999}}},
		{"fps zero", []assets.Animation{{Name: "x", FrameFrom: 0, FrameTo: 1, FPS: 0}}},
		{"unknown direction", []assets.Animation{{Name: "x", FrameFrom: 0, FrameTo: 1, FPS: 8, Direction: "wobble"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := svc.ReplaceAnimations(ctx, a.ID, c.in); err == nil {
				t.Errorf("expected error for %s", c.name)
			}
		})
	}
}

func TestListAnimationsByAssetIDs_NoNPlusOne(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	a1, _ := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "hero", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	a2, _ := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "boss", ContentAddressedPath: "p2",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	_ = svc.ReplaceAnimations(ctx, a1.ID, []assets.Animation{
		{Name: "walk_east", FrameFrom: 0, FrameTo: 3, FPS: 8},
	})
	_ = svc.ReplaceAnimations(ctx, a2.ID, []assets.Animation{
		{Name: "attack", FrameFrom: 0, FrameTo: 5, FPS: 10},
	})
	got, err := svc.ListAnimationsByAssetIDs(ctx, []int64{a1.ID, a2.ID, 999_999})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 keys, got %d", len(got))
	}
	if rows := got[a1.ID]; len(rows) != 1 || rows[0].Name != "walk_east" {
		t.Errorf("a1 rows wrong: %+v", rows)
	}
	if rows := got[a2.ID]; len(rows) != 1 || rows[0].Name != "attack" {
		t.Errorf("a2 rows wrong: %+v", rows)
	}
	// Empty input is cheap + correct.
	empty, err := svc.ListAnimationsByAssetIDs(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("empty input: got (%v, %v)", empty, err)
	}
}

func TestFindAnimationByName(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()
	a, _ := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "hero", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	_ = svc.ReplaceAnimations(ctx, a.ID, []assets.Animation{
		{Name: "walk_east", FrameFrom: 4, FrameTo: 7, FPS: 8},
	})
	got, err := svc.FindAnimationByName(ctx, a.ID, "WALK_EAST")
	if err != nil {
		t.Fatal(err)
	}
	if got.FrameFrom != 4 {
		t.Errorf("got FrameFrom %d, want 4", got.FrameFrom)
	}
	if _, err := svc.FindAnimationByName(ctx, a.ID, "missing"); !errors.Is(err, assets.ErrAssetNotFound) {
		t.Errorf("missing: got %v, want ErrAssetNotFound", err)
	}
}
