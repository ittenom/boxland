package maps_test

import (
	"context"
	"errors"
	"testing"

	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/maps"
	"boxland/server/internal/maps/wfc"
)

func TestProceduralPreview_NoTileKindsReturnsError(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	// Don't call resetDB — it seeds a tile-class entity, and the
	// scenario we want is "fresh project, no tiles yet."
	svc := maps.New(pool)
	_, err := svc.GenerateProceduralPreview(context.Background(), maps.ProceduralPreviewInput{
		Width: 4, Height: 4, Seed: 1,
	})
	if !errors.Is(err, maps.ErrNoTileKinds) {
		t.Fatalf("expected ErrNoTileKinds, got %v", err)
	}
}

func TestProceduralPreview_RejectsInvalidDimensions(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := maps.New(pool)
	_, err := svc.GenerateProceduralPreview(context.Background(), maps.ProceduralPreviewInput{
		Width: 0, Height: 4, Seed: 1,
	})
	if !errors.Is(err, wfc.ErrInvalidRegion) {
		t.Fatalf("expected ErrInvalidRegion, got %v", err)
	}
}

func TestProceduralPreview_DetectsTileClassEntityTypes(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, baseEtID := resetDB(t, pool) // creates 'wall' (entity_class='tile')
	_ = baseEtID
	ents := entities.New(pool, components.Default())

	// Mirror the upload-time tilemap workflow: auto-sliced cells are
	// minted with entity_class='tile' (no tag, no component). The
	// procedural query keys off the class column.
	floor, err := ents.Create(context.Background(), entities.CreateInput{
		Name: "floor", CreatedBy: designerID, EntityClass: entities.ClassTile,
	})
	if err != nil {
		t.Fatalf("create floor: %v", err)
	}

	sock, err := ents.CreateSocket(context.Background(), "open", 0xffffffff, designerID)
	if err != nil {
		t.Fatalf("create socket: %v", err)
	}
	if err := ents.SetTileEdges(context.Background(), baseEtID, &sock.ID, &sock.ID, &sock.ID, &sock.ID); err != nil {
		t.Fatalf("set wall edges: %v", err)
	}
	if err := ents.SetTileEdges(context.Background(), floor.ID, &sock.ID, &sock.ID, &sock.ID, &sock.ID); err != nil {
		t.Fatalf("set floor edges: %v", err)
	}

	svc := maps.New(pool)
	res, err := svc.GenerateProceduralPreview(context.Background(), maps.ProceduralPreviewInput{
		Width: 4, Height: 4, Seed: 7,
	})
	if err != nil {
		t.Fatalf("GenerateProceduralPreview: %v", err)
	}
	if res.TileSetSize != 2 {
		t.Fatalf("TileSetSize=%d, want 2 tile-tagged entity types", res.TileSetSize)
	}
}

func TestProceduralPreview_TileGroupProceduralToggles(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	ctx := context.Background()

	floor, err := ents.Create(ctx, entities.CreateInput{Name: "floor", CreatedBy: designerID, EntityClass: entities.ClassTile})
	if err != nil {
		t.Fatalf("create floor: %v", err)
	}
	_ = baseEtID
	sock, err := ents.CreateSocket(ctx, "open", 0xffffffff, designerID)
	if err != nil {
		t.Fatalf("create socket: %v", err)
	}
	_ = ents.SetTileEdges(ctx, baseEtID, &sock.ID, &sock.ID, &sock.ID, &sock.ID)
	_ = ents.SetTileEdges(ctx, floor.ID, &sock.ID, &sock.ID, &sock.ID, &sock.ID)

	tg, err := ents.CreateTileGroup(ctx, entities.CreateTileGroupInput{Name: "pair", Width: 2, Height: 1, CreatedBy: designerID})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := ents.UpdateTileGroupLayoutAndProcedural(ctx, tg.ID, entities.UpdateTileGroupLayoutInput{
		Layout:                       entities.Layout{{baseEtID, floor.ID}},
		ExcludeMembersFromProcedural: true,
		UseGroupInProcedural:         true,
	}); err != nil {
		t.Fatalf("update group: %v", err)
	}

	svc := maps.New(pool)
	res, err := svc.GenerateProceduralPreview(ctx, maps.ProceduralPreviewInput{Width: 4, Height: 2, Seed: 1})
	if err != nil {
		t.Fatalf("preview with group: %v", err)
	}
	if res.TileSetSize != 1 {
		t.Fatalf("TileSetSize=%d, want only atomic group candidate", res.TileSetSize)
	}
	if !containsHorizontalPair(res.Region, wfc.EntityTypeID(baseEtID), wfc.EntityTypeID(floor.ID)) {
		t.Fatalf("expected procedural output to contain intact tile group pair; cells=%v", res.Region.Cells)
	}

	if err := ents.UpdateTileGroupLayoutAndProcedural(ctx, tg.ID, entities.UpdateTileGroupLayoutInput{
		Layout:                       entities.Layout{{baseEtID, floor.ID}},
		ExcludeMembersFromProcedural: true,
		UseGroupInProcedural:         false,
	}); err != nil {
		t.Fatalf("disable group: %v", err)
	}
	_, err = svc.GenerateProceduralPreview(ctx, maps.ProceduralPreviewInput{Width: 4, Height: 2, Seed: 1})
	if !errors.Is(err, maps.ErrNoTileKinds) {
		t.Fatalf("disabled group with excluded members should leave no procedural candidates; got %v", err)
	}
}

func containsHorizontalPair(r *wfc.Region, left, right wfc.EntityTypeID) bool {
	if r == nil {
		return false
	}
	for _, c := range r.Cells {
		if c.EntityType == left && r.At(c.X+1, c.Y) == right {
			return true
		}
	}
	return false
}

// (addTileComponent helper removed: paintable tiles are identified
// by entity_class='tile' in the redesigned schema, not by the Tile
// ECS component. The component still attaches at map-instance time
// when a tile is materialized into a level's runtime ECS, but the
// procedural query no longer reads it.)

func TestProceduralPreview_FillsRegionWithProjectTiles(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, baseEtID := resetDB(t, pool) // creates 'wall' (entity_class='tile')
	ents := entities.New(pool, components.Default())
	_ = baseEtID

	// Create a second tile-class entity-type so WFC has > 1 option.
	// resetDB already seeded the first one as ClassTile.
	floor, err := ents.Create(context.Background(), entities.CreateInput{
		Name: "floor", CreatedBy: designerID, EntityClass: entities.ClassTile,
	})
	if err != nil {
		t.Fatalf("create floor: %v", err)
	}

	// Create one socket; assign it to all 4 edges of both types so they
	// can sit anywhere next to each other (no contradictions possible).
	sock, err := ents.CreateSocket(context.Background(), "open", 0xffffffff, designerID)
	if err != nil {
		t.Fatalf("create socket: %v", err)
	}
	if err := ents.SetTileEdges(context.Background(), baseEtID, &sock.ID, &sock.ID, &sock.ID, &sock.ID); err != nil {
		t.Fatalf("set wall edges: %v", err)
	}
	if err := ents.SetTileEdges(context.Background(), floor.ID, &sock.ID, &sock.ID, &sock.ID, &sock.ID); err != nil {
		t.Fatalf("set floor edges: %v", err)
	}

	svc := maps.New(pool)
	res, err := svc.GenerateProceduralPreview(context.Background(), maps.ProceduralPreviewInput{
		Width: 8, Height: 8, Seed: 42,
	})
	if err != nil {
		t.Fatalf("GenerateProceduralPreview: %v", err)
	}
	if res.TileSetSize != 2 {
		t.Errorf("TileSetSize=%d, want 2", res.TileSetSize)
	}
	if res.Region == nil || len(res.Region.Cells) != 64 {
		t.Fatalf("expected 64 cells, got %v", res.Region)
	}
	for _, c := range res.Region.Cells {
		if c.EntityType != wfc.EntityTypeID(baseEtID) && c.EntityType != wfc.EntityTypeID(floor.ID) {
			t.Errorf("unexpected entity-type %d in output", c.EntityType)
		}
	}
}

func TestMaterializeProcedural_PersistsTilesAndUpdatesSeed(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	// resetDB already mints baseEtID with entity_class='tile'.
	floor, _ := ents.Create(context.Background(), entities.CreateInput{
		Name: "floor", CreatedBy: designerID, EntityClass: entities.ClassTile,
	})
	sock, _ := ents.CreateSocket(context.Background(), "open", 0xffffffff, designerID)
	_ = ents.SetTileEdges(context.Background(), baseEtID, &sock.ID, &sock.ID, &sock.ID, &sock.ID)
	_ = ents.SetTileEdges(context.Background(), floor.ID, &sock.ID, &sock.ID, &sock.ID, &sock.ID)

	svc := maps.New(pool)
	m, err := svc.Create(context.Background(), maps.CreateInput{
		Name: "world", Width: 6, Height: 6, CreatedBy: designerID,
		Mode: "procedural",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := svc.MaterializeProcedural(context.Background(), maps.MaterializeProceduralInput{
		MapID: m.ID, Seed: 12345,
	})
	if err != nil {
		t.Fatalf("MaterializeProcedural: %v", err)
	}
	if res.TilesWritten != 36 {
		t.Errorf("TilesWritten=%d, want 36", res.TilesWritten)
	}

	// Verify tiles persisted to map_tiles for the base layer.
	tiles, err := svc.ChunkTiles(context.Background(), m.ID, 0, 0, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tiles) != 36 {
		t.Errorf("persisted tiles=%d, want 36", len(tiles))
	}

	// Verify the map's seed column was updated.
	got, _ := svc.FindByID(context.Background(), m.ID)
	if got.Seed == nil || *got.Seed != 12345 {
		t.Errorf("seed not persisted: got %v, want 12345", got.Seed)
	}

	// Re-materialize with a new seed: replaces the layer.
	res2, err := svc.MaterializeProcedural(context.Background(), maps.MaterializeProceduralInput{
		MapID: m.ID, Seed: 999,
	})
	if err != nil {
		t.Fatalf("re-materialize: %v", err)
	}
	if res2.TilesWritten != 36 {
		t.Errorf("re-materialize tiles=%d, want 36", res2.TilesWritten)
	}
	tiles2, _ := svc.ChunkTiles(context.Background(), m.ID, 0, 0, 5, 5)
	if len(tiles2) != 36 {
		t.Errorf("re-materialize persisted tiles=%d, want 36 (old tiles should have been wiped)", len(tiles2))
	}
}

func TestMaterializeProcedural_RejectsAuthoredMaps(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, _ := resetDB(t, pool)
	svc := maps.New(pool)
	m, _ := svc.Create(context.Background(), maps.CreateInput{
		Name: "authored", Width: 4, Height: 4, CreatedBy: designerID,
		// Mode defaults to "authored".
	})
	_, err := svc.MaterializeProcedural(context.Background(), maps.MaterializeProceduralInput{
		MapID: m.ID, Seed: 1,
	})
	if !errors.Is(err, maps.ErrNotProcedural) {
		t.Fatalf("expected ErrNotProcedural, got %v", err)
	}
}

// (TestMaterializeProcedural_RejectsTransientMaps removed: persistence_mode
// no longer lives on maps. It moved to LEVELs in the holistic redesign;
// materialization always writes to map_tiles, which is the canonical
// authored geometry.)

func TestProceduralPreview_DeterministicForSameSeed(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())

	floor, _ := ents.Create(context.Background(), entities.CreateInput{
		Name: "floor", CreatedBy: designerID, EntityClass: entities.ClassTile,
	})
	sock, _ := ents.CreateSocket(context.Background(), "open", 0xffffffff, designerID)
	_ = ents.SetTileEdges(context.Background(), baseEtID, &sock.ID, &sock.ID, &sock.ID, &sock.ID)
	_ = ents.SetTileEdges(context.Background(), floor.ID, &sock.ID, &sock.ID, &sock.ID, &sock.ID)

	svc := maps.New(pool)
	in := maps.ProceduralPreviewInput{Width: 6, Height: 6, Seed: 31337}
	r1, err := svc.GenerateProceduralPreview(context.Background(), in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	r2, err := svc.GenerateProceduralPreview(context.Background(), in)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	for i := range r1.Region.Cells {
		if r1.Region.Cells[i] != r2.Region.Cells[i] {
			t.Fatalf("non-deterministic at cell %d: %v vs %v", i, r1.Region.Cells[i], r2.Region.Cells[i])
		}
	}
}
