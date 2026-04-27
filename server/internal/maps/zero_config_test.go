// Boxland — zero-config procedural-mode behavior tests.
//
// The single rule for the new zero-config path: paint a few tiles, hit
// Generate, get a chunked-overlapping result whose dominant tile mix
// matches what you painted. No sample-patch row required.

package maps_test

import (
	"context"
	"testing"

	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/maps"
)

func TestPreview_AutoSamplesFromPaintedTiles(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, et1, et2, layerID := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	// Paint a 4x4 stripe (cols alternate et1 / et2). NO explicit
	// sample patch — the engine should auto-derive from the bounding
	// box.
	for y := int32(0); y < 4; y++ {
		for x := int32(0); x < 4; x++ {
			et := et1
			if x%2 == 1 {
				et = et2
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO map_tiles (map_id, layer_id, x, y, entity_type_id)
				VALUES ($1, $2, $3, $4, $5)
			`, mapID, layerID, x, y, et); err != nil {
				t.Fatalf("paint (%d,%d): %v", x, y, err)
			}
		}
	}

	// Map is 4x4 so it'll be a single chunk; that's fine — the test is
	// "did the engine pick up the painted style without an explicit
	// sample row?"
	res, err := svc.GenerateProceduralPreview(ctx, maps.ProceduralPreviewInput{
		MapID: mapID, Width: 4, Height: 4, Seed: 1,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Algorithm != "chunked-overlapping" {
		t.Errorf("algorithm = %q, want chunked-overlapping (auto-sample should have kicked in)", res.Algorithm)
	}
	if res.PatternCount < 2 {
		t.Errorf("PatternCount = %d, want >= 2", res.PatternCount)
	}
}

func TestPreview_NoPaintNoSample_FallsBackToSocket(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, _, _, _ := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	res, err := svc.GenerateProceduralPreview(ctx, maps.ProceduralPreviewInput{
		MapID: mapID, Width: 4, Height: 4, Seed: 1,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Algorithm != "chunked-socket" {
		t.Errorf("algorithm = %q, want chunked-socket on empty map", res.Algorithm)
	}
}

func TestPreview_ExcludedTileNeverAppearsInOutput(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, et1, et2, _ := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	// Exclude et2 from the procedural pool. Even on a blank map the
	// socket-fallback path should now never emit et2.
	if err := ents.SetProceduralInclude(ctx, et2, false); err != nil {
		t.Fatalf("exclude et2: %v", err)
	}

	res, err := svc.GenerateProceduralPreview(ctx, maps.ProceduralPreviewInput{
		MapID: mapID, Width: 4, Height: 4, Seed: 7,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, c := range res.Region.Cells {
		if int64(c.EntityType) == et2 {
			t.Errorf("excluded tile %d appeared at (%d,%d)", et2, c.X, c.Y)
		}
		if int64(c.EntityType) != et1 {
			t.Errorf("unexpected entity type %d at (%d,%d) — only et1 should remain after excluding et2",
				c.EntityType, c.X, c.Y)
		}
	}
}

func TestPreview_LargerMapUsesChunkedLayout(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, _, _, _ := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	// 64x48 — the customer's screenshot dims. pickChunkLayout should
	// return 32x24 chunks (2×2), not 1×1, so the engine actually
	// chunks. We can't observe internal chunking from the public API
	// directly, but we CAN observe that the output dims match exactly
	// (which is the chunked engine's invariant: chunkW * countX == W).
	res, err := svc.GenerateProceduralPreview(ctx, maps.ProceduralPreviewInput{
		MapID: mapID, Width: 64, Height: 48, Seed: 1,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Region.Width != 64 || res.Region.Height != 48 {
		t.Errorf("dims = %dx%d, want 64x48", res.Region.Width, res.Region.Height)
	}
	if len(res.Region.Cells) != 64*48 {
		t.Errorf("cell count = %d, want %d", len(res.Region.Cells), 64*48)
	}
}
