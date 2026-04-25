package maps_test

import (
	"context"
	"encoding/json"
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
	resetDB(t, pool) // creates a designer + plain (non-tile) entity-type
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

// addTileComponent attaches the components.KindTile component to an entity
// type so it surfaces in the procedural tile-set query.
func addTileComponent(t *testing.T, ents *entities.Service, etID int64) {
	t.Helper()
	if err := ents.SetComponents(context.Background(), nil, etID, map[components.Kind]json.RawMessage{
		components.KindTile: []byte(`{"layer_id":0,"gx":0,"gy":0}`),
	}); err != nil {
		t.Fatalf("SetComponents: %v", err)
	}
}

func TestProceduralPreview_FillsRegionWithProjectTiles(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, baseEtID := resetDB(t, pool) // creates 'wall'
	ents := entities.New(pool, components.Default())

	// Make the 'wall' entity-type a tile-kind.
	addTileComponent(t, ents, baseEtID)

	// Create a second tile-kind entity-type so WFC has > 1 option.
	floor, err := ents.Create(context.Background(), entities.CreateInput{
		Name: "floor", CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("create floor: %v", err)
	}
	addTileComponent(t, ents, floor.ID)

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
	addTileComponent(t, ents, baseEtID)
	floor, _ := ents.Create(context.Background(), entities.CreateInput{Name: "floor", CreatedBy: designerID})
	addTileComponent(t, ents, floor.ID)
	sock, _ := ents.CreateSocket(context.Background(), "open", 0xffffffff, designerID)
	_ = ents.SetTileEdges(context.Background(), baseEtID, &sock.ID, &sock.ID, &sock.ID, &sock.ID)
	_ = ents.SetTileEdges(context.Background(), floor.ID, &sock.ID, &sock.ID, &sock.ID, &sock.ID)

	svc := maps.New(pool)
	m, err := svc.Create(context.Background(), maps.CreateInput{
		Name: "world", Width: 6, Height: 6, CreatedBy: designerID,
		Mode: "procedural", PersistenceMode: "persistent",
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

func TestMaterializeProcedural_RejectsTransientMaps(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, _ := resetDB(t, pool)
	svc := maps.New(pool)
	m, _ := svc.Create(context.Background(), maps.CreateInput{
		Name: "transient", Width: 4, Height: 4, CreatedBy: designerID,
		Mode: "procedural", PersistenceMode: "transient",
	})
	_, err := svc.MaterializeProcedural(context.Background(), maps.MaterializeProceduralInput{
		MapID: m.ID, Seed: 1,
	})
	if !errors.Is(err, maps.ErrNotPersistent) {
		t.Fatalf("expected ErrNotPersistent, got %v", err)
	}
}

func TestProceduralPreview_DeterministicForSameSeed(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	addTileComponent(t, ents, baseEtID)

	floor, _ := ents.Create(context.Background(), entities.CreateInput{Name: "floor", CreatedBy: designerID})
	addTileComponent(t, ents, floor.ID)
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
