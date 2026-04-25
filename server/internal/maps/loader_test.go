package maps_test

import (
	"context"
	"testing"

	"boxland/server/internal/maps"
	"boxland/server/internal/sim/ecs"
)

// stubLookup returns canned metadata for any tile entity type id, so the
// loader's database-side test doesn't depend on the entities service.
type stubLookup struct{ count int }

func (s *stubLookup) EntityTypeMeta(_ context.Context, id int64) (*maps.EntityTypeMeta, error) {
	s.count++
	return &maps.EntityTypeMeta{
		ID:                   id,
		ColliderW:            32,
		ColliderH:            32,
		ColliderAnchorX:      16,
		ColliderAnchorY:      16,
		DefaultCollisionMask: 1,
	}, nil
}

func TestLoadChunk_MaterializesTilesAsECSEntities(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, etID := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()

	m, _ := svc.Create(ctx, maps.CreateInput{
		Name: "load-test", Width: 32, Height: 32, CreatedBy: designerID,
	})
	layers, _ := svc.Layers(ctx, m.ID)
	baseLayerID := layers[0].ID

	// Paint a 3x3 cluster.
	tiles := make([]maps.Tile, 0, 9)
	for x := int32(2); x <= 4; x++ {
		for y := int32(2); y <= 4; y++ {
			tiles = append(tiles, maps.Tile{
				MapID: m.ID, LayerID: baseLayerID, X: x, Y: y, EntityTypeID: etID,
			})
		}
	}
	if err := svc.PlaceTiles(ctx, tiles); err != nil {
		t.Fatal(err)
	}

	world := ecs.NewWorld()
	lookup := &stubLookup{}
	res, err := svc.LoadChunk(ctx, world, lookup, m.ID, 0, 0, 15, 15)
	if err != nil {
		t.Fatalf("LoadChunk: %v", err)
	}
	if res.TilesSpawned != 9 {
		t.Errorf("spawned: got %d, want 9", res.TilesSpawned)
	}
	stores := world.Stores()
	if stores.Tile.Len() != 9 {
		t.Errorf("Tile store: got %d, want 9", stores.Tile.Len())
	}
	if stores.Static.Len() != 9 {
		t.Errorf("Static store: got %d, want 9", stores.Static.Len())
	}
	if stores.Sprite.Len() != 9 {
		t.Errorf("Sprite store: got %d, want 9", stores.Sprite.Len())
	}
	if stores.Collider.Len() != 9 {
		t.Errorf("Collider store: got %d, want 9", stores.Collider.Len())
	}
	// Lookup cache: identical entity types only fetch once.
	if lookup.count != 1 {
		t.Errorf("lookup hits: got %d, want 1 (cache miss only on first tile)", lookup.count)
	}
}

func TestLoadChunk_HonorsTileOverrides(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, etID := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()

	m, _ := svc.Create(ctx, maps.CreateInput{Name: "ov-test", Width: 4, Height: 4, CreatedBy: designerID})
	layers, _ := svc.Layers(ctx, m.ID)
	baseLayerID := layers[0].ID

	maskOverride := int64(0x0a)
	animOverride := int16(7)
	tile := maps.Tile{
		MapID:                  m.ID,
		LayerID:                baseLayerID,
		X:                      0, Y: 0,
		EntityTypeID:           etID,
		AnimOverride:           &animOverride,
		CollisionMaskOverride:  &maskOverride,
	}
	_ = svc.PlaceTiles(ctx, []maps.Tile{tile})

	world := ecs.NewWorld()
	if _, err := svc.LoadChunk(ctx, world, &stubLookup{}, m.ID, 0, 0, 4, 4); err != nil {
		t.Fatal(err)
	}
	owners := world.Stores().Collider.Owners()
	if len(owners) != 1 {
		t.Fatalf("expected 1 collider, got %d", len(owners))
	}
	c, _ := world.Stores().Collider.Get(owners[0])
	if c.Mask != 0x0a {
		t.Errorf("collider mask: got 0x%x, want 0x0a", c.Mask)
	}
	sp, _ := world.Stores().Sprite.Get(owners[0])
	if sp.AnimID != 7 {
		t.Errorf("sprite anim: got %d, want 7 (override)", sp.AnimID)
	}
}

func TestLoadChunk_SkipsTilesOutsideRange(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, etID := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()

	m, _ := svc.Create(ctx, maps.CreateInput{Name: "skip", Width: 100, Height: 100, CreatedBy: designerID})
	layers, _ := svc.Layers(ctx, m.ID)
	baseLayerID := layers[0].ID

	_ = svc.PlaceTiles(ctx, []maps.Tile{
		{MapID: m.ID, LayerID: baseLayerID, X: 0, Y: 0, EntityTypeID: etID},
		{MapID: m.ID, LayerID: baseLayerID, X: 50, Y: 50, EntityTypeID: etID},
	})

	world := ecs.NewWorld()
	res, _ := svc.LoadChunk(ctx, world, &stubLookup{}, m.ID, 0, 0, 15, 15)
	if res.TilesSpawned != 1 {
		t.Errorf("expected 1 tile in chunk, got %d", res.TilesSpawned)
	}
}
