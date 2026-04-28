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

// procFixture creates a procedural+persistent map with two tile-kind
// entity types (already socketed so generation always succeeds) and
// returns the map id, both entity-type ids, and the base tile layer id.
func procFixture(t *testing.T, ctx context.Context, designerID, baseEtID int64, ents *entities.Service, svc *maps.Service) (mapID int64, et1, et2, layerID int64) {
	t.Helper()
	// resetDB already mints baseEtID with entity_class='tile'; the
	// floor entity needs the same class so the procedural tile-set
	// query picks it up.
	_ = baseEtID
	floor, err := ents.Create(ctx, entities.CreateInput{
		Name: "floor", CreatedBy: designerID, EntityClass: entities.ClassTile,
	})
	if err != nil {
		t.Fatalf("create floor: %v", err)
	}
	sock, err := ents.CreateSocket(ctx, "open", 0xffffffff, designerID)
	if err != nil {
		t.Fatalf("create socket: %v", err)
	}
	if err := ents.SetTileEdges(ctx, baseEtID, &sock.ID, &sock.ID, &sock.ID, &sock.ID); err != nil {
		t.Fatalf("base edges: %v", err)
	}
	if err := ents.SetTileEdges(ctx, floor.ID, &sock.ID, &sock.ID, &sock.ID, &sock.ID); err != nil {
		t.Fatalf("floor edges: %v", err)
	}
	m, err := svc.Create(ctx, maps.CreateInput{
		Name: "proc-map", Width: 4, Height: 4,
		Mode:      "procedural",
		CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	layers, err := svc.Layers(ctx, m.ID)
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	for _, l := range layers {
		if l.Kind == "tile" {
			layerID = l.ID
			break
		}
	}
	return m.ID, baseEtID, floor.ID, layerID
}

func TestLockCells_PersistsAndRoundTrips(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, et1, et2, layerID := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	// Small batch path (< 32 cells).
	if err := svc.LockCells(ctx, []maps.LockedCell{
		{MapID: mapID, LayerID: layerID, X: 0, Y: 0, EntityTypeID: et1},
		{MapID: mapID, LayerID: layerID, X: 1, Y: 0, EntityTypeID: et2, RotationDegrees: 90},
	}); err != nil {
		t.Fatalf("LockCells: %v", err)
	}
	got, err := svc.LockedCells(ctx, mapID)
	if err != nil {
		t.Fatalf("LockedCells: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 locks, got %d", len(got))
	}
	if got[1].RotationDegrees != 90 {
		t.Errorf("rotation lost: %+v", got[1])
	}

	// Re-locking the same cell should overwrite.
	if err := svc.LockCells(ctx, []maps.LockedCell{
		{MapID: mapID, LayerID: layerID, X: 0, Y: 0, EntityTypeID: et2, RotationDegrees: 180},
	}); err != nil {
		t.Fatalf("re-lock: %v", err)
	}
	got, _ = svc.LockedCells(ctx, mapID)
	for _, c := range got {
		if c.X == 0 && c.Y == 0 {
			if c.EntityTypeID != et2 || c.RotationDegrees != 180 {
				t.Errorf("re-lock did not update: %+v", c)
			}
		}
	}

	// Count helper.
	n, err := svc.LockedCellCount(ctx, mapID)
	if err != nil || n != 2 {
		t.Errorf("LockedCellCount got %d (err %v), want 2", n, err)
	}

	// Unlock one.
	if err := svc.UnlockCells(ctx, mapID, layerID, [][2]int32{{1, 0}}); err != nil {
		t.Fatalf("UnlockCells: %v", err)
	}
	if n, _ := svc.LockedCellCount(ctx, mapID); n != 1 {
		t.Errorf("after unlock count = %d, want 1", n)
	}

	// Bulk path (>= 32 cells via CopyFrom).
	bulk := make([]maps.LockedCell, 0, 40)
	for i := int32(0); i < 40; i++ {
		bulk = append(bulk, maps.LockedCell{
			MapID: mapID, LayerID: layerID, X: i % 4, Y: 1 + i/4, EntityTypeID: et1,
		})
	}
	if err := svc.LockCells(ctx, bulk); err != nil {
		t.Fatalf("bulk LockCells: %v", err)
	}

	// ClearLockedCells wipes the lot.
	if err := svc.ClearLockedCells(ctx, mapID, 0); err != nil {
		t.Fatalf("ClearLockedCells: %v", err)
	}
	if n, _ := svc.LockedCellCount(ctx, mapID); n != 0 {
		t.Errorf("after clear count = %d, want 0", n)
	}
}

func TestLockCells_RejectsInvalidRotation(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, et1, _, layerID := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	err := svc.LockCells(ctx, []maps.LockedCell{
		{MapID: mapID, LayerID: layerID, X: 0, Y: 0, EntityTypeID: et1, RotationDegrees: 45},
	})
	if !errors.Is(err, maps.ErrLockedCellInvalid) {
		t.Fatalf("want ErrLockedCellInvalid, got %v", err)
	}
}

func TestLockCells_TenantIsolated(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapA, et1, _, layerA := procFixture(t, ctx, designerID, baseEtID, ents, svc)
	// Second map. Reuse the same designer + tiles; we just need a fresh maps.id.
	mB, err := svc.Create(ctx, maps.CreateInput{
		Name: "proc-map-2", Width: 4, Height: 4, Mode: "procedural",
		CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("create map B: %v", err)
	}
	if err := svc.LockCells(ctx, []maps.LockedCell{
		{MapID: mapA, LayerID: layerA, X: 0, Y: 0, EntityTypeID: et1},
	}); err != nil {
		t.Fatalf("lock A: %v", err)
	}
	got, err := svc.LockedCells(ctx, mB.ID)
	if err != nil {
		t.Fatalf("query B: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("locks bled across maps: %+v", got)
	}
}

func TestMaterialize_LockedCellsSurviveAndAnchor(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, et1, et2, layerID := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	// Lock a couple of specific cells.
	if err := svc.LockCells(ctx, []maps.LockedCell{
		{MapID: mapID, LayerID: layerID, X: 1, Y: 1, EntityTypeID: et1, RotationDegrees: 90},
		{MapID: mapID, LayerID: layerID, X: 2, Y: 2, EntityTypeID: et2, RotationDegrees: 180},
	}); err != nil {
		t.Fatalf("lock: %v", err)
	}

	res, err := svc.MaterializeProcedural(ctx, maps.MaterializeProceduralInput{MapID: mapID, Seed: 7, LayerID: layerID})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if res.TilesWritten != 16 {
		t.Errorf("wrote %d tiles, expected 16 (4x4)", res.TilesWritten)
	}

	// The persisted base layer should contain our locks at the exact
	// coordinates with the correct entity + rotation.
	tiles, err := svc.ChunkTiles(ctx, mapID, 0, 0, 3, 3)
	if err != nil {
		t.Fatalf("chunk tiles: %v", err)
	}
	found := map[[2]int32]maps.Tile{}
	for _, tt := range tiles {
		found[[2]int32{tt.X, tt.Y}] = tt
	}
	if got := found[[2]int32{1, 1}]; got.EntityTypeID != et1 || got.RotationDegrees != 90 {
		t.Errorf("lock at (1,1) not preserved: %+v", got)
	}
	if got := found[[2]int32{2, 2}]; got.EntityTypeID != et2 || got.RotationDegrees != 180 {
		t.Errorf("lock at (2,2) not preserved: %+v", got)
	}

	// Re-materializing with a different seed must still preserve the locks.
	if _, err := svc.MaterializeProcedural(ctx, maps.MaterializeProceduralInput{MapID: mapID, Seed: 9999, LayerID: layerID}); err != nil {
		t.Fatalf("re-materialize: %v", err)
	}
	tiles, _ = svc.ChunkTiles(ctx, mapID, 0, 0, 3, 3)
	found = map[[2]int32]maps.Tile{}
	for _, tt := range tiles {
		found[[2]int32{tt.X, tt.Y}] = tt
	}
	if got := found[[2]int32{1, 1}]; got.EntityTypeID != et1 || got.RotationDegrees != 90 {
		t.Errorf("after reroll lock at (1,1) lost: %+v", got)
	}
}

func TestPreview_MergesLockAnchors(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, et1, _, layerID := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	if err := svc.LockCells(ctx, []maps.LockedCell{
		{MapID: mapID, LayerID: layerID, X: 0, Y: 0, EntityTypeID: et1},
	}); err != nil {
		t.Fatalf("lock: %v", err)
	}
	res, err := svc.GenerateProceduralPreview(ctx, maps.ProceduralPreviewInput{
		MapID: mapID, Width: 4, Height: 4, Seed: 1,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Region.Cells[0].EntityType != wfc.EntityTypeID(et1) {
		t.Errorf("lock not honored at (0,0): %+v", res.Region.Cells[0])
	}
	if res.Algorithm != "chunked-socket" {
		t.Errorf("algorithm = %q, want chunked-socket", res.Algorithm)
	}
}

func TestPreview_OverlappingFallsBackWithoutSamplePatch(t *testing.T) {
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
	// No sample patch defined → falls back to socket so the designer
	// still sees output (with a slog warning, see runProcedural).
	if res.Algorithm != "chunked-socket" {
		t.Errorf("expected fallback to socket, got %q", res.Algorithm)
	}
}

func TestPreview_OverlappingUsesSamplePatch(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, et1, et2, layerID := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	// Paint a tiny stripe sample in the base layer (cols alternate
	// et1/et2). The sample patch row points at this 4x4 region.
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
				t.Fatalf("paint sample cell (%d,%d): %v", x, y, err)
			}
		}
	}
	if err := svc.UpsertSamplePatch(ctx, maps.SamplePatchInput{
		MapID: mapID, LayerID: layerID, X: 0, Y: 0, Width: 4, Height: 4,
	}); err != nil {
		t.Fatalf("upsert sample patch: %v", err)
	}

	res, err := svc.GenerateProceduralPreview(ctx, maps.ProceduralPreviewInput{
		MapID: mapID, Width: 6, Height: 6, Seed: 1,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Algorithm != "chunked-overlapping" {
		t.Errorf("algorithm = %q, want %q", res.Algorithm, "chunked-overlapping")
	}
	if res.PatternCount < 2 {
		t.Errorf("PatternCount = %d, want >= 2 for stripe sample", res.PatternCount)
	}
	if res.Region == nil || len(res.Region.Cells) != 36 {
		t.Errorf("region empty/wrong size: %+v", res.Region)
	}
	// The output should preserve the stripe — every horizontal
	// neighbour pair must differ.
	w := int(res.Region.Width)
	for y := 0; y < int(res.Region.Height); y++ {
		for x := 0; x < w-1; x++ {
			a := res.Region.Cells[y*w+x].EntityType
			b := res.Region.Cells[y*w+x+1].EntityType
			if a == b {
				t.Errorf("stripe broken at (%d,%d): both = %d", x, y, a)
			}
		}
	}
	_ = wfc.EntityTypeID(et1) // keep wfc import live alongside other tests
}

