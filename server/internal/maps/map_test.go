package maps_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/publishing/artifact"
)

// openTestPool returns an isolated, freshly-migrated DB. testdb.New wires its own t.Cleanup that drops the database when the test ends.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// resetDB creates per-test fixtures (a designer, etc.). The pool is already
// empty because testdb.New(t) returns a fresh database for every test.
func resetDB(t *testing.T, pool *pgxpool.Pool) (designerID, tileEntityID int64) {
	t.Helper()
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "map-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	ents := entities.New(pool, components.Default())
	// Per the holistic redesign, tile-paintable entities have
	// entity_class='tile'. The procedural / palette queries filter on
	// that column, so seed accordingly. (Previously this seed used
	// the legacy "tile" tag, which the queries also accepted; the
	// redesign drops the tag fallback.)
	et, err := ents.Create(context.Background(), entities.CreateInput{
		Name:        "wall",
		EntityClass: entities.ClassTile,
		CreatedBy:   d.ID,
	})
	if err != nil {
		t.Fatalf("create entity type: %v", err)
	}
	return d.ID, et.ID
}

func TestCreate_DefaultLayersInserted(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, _ := resetDB(t, pool)
	svc := maps.New(pool)

	m, err := svc.Create(context.Background(), maps.CreateInput{
		Name: "Tutorial Forest", Width: 64, Height: 48, CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.ID == 0 {
		t.Errorf("ID should be assigned")
	}

	layers, err := svc.Layers(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 3 {
		t.Errorf("expected 3 default layers, got %d (%+v)", len(layers), layers)
	}
	wantNames := map[string]string{"base": "tile", "decoration": "tile", "lighting": "lighting"}
	for _, l := range layers {
		if k, ok := wantNames[l.Name]; !ok || k != l.Kind {
			t.Errorf("unexpected layer %+v", l)
		}
	}
}

func TestCreate_DuplicateNameRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, _ := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()
	if _, err := svc.Create(ctx, maps.CreateInput{Name: "dup", Width: 4, Height: 4, CreatedBy: designerID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, maps.CreateInput{Name: "dup", Width: 4, Height: 4, CreatedBy: designerID}); !errors.Is(err, maps.ErrNameInUse) {
		t.Errorf("got %v, want ErrNameInUse", err)
	}
}

func TestCreate_DimensionValidation(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, _ := resetDB(t, pool)
	svc := maps.New(pool)
	if _, err := svc.Create(context.Background(), maps.CreateInput{
		Name: "bad", Width: 0, Height: 4, CreatedBy: designerID,
	}); err == nil {
		t.Error("expected validation error for width=0")
	}
}

func TestPlaceTiles_RoundTripAndChunkQuery(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, etID := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()

	m, _ := svc.Create(ctx, maps.CreateInput{
		Name: "tile-test", Width: 32, Height: 32, CreatedBy: designerID,
	})
	layers, _ := svc.Layers(ctx, m.ID)
	baseLayerID := layers[0].ID

	// Paint 4 tiles in a 2x2 cluster.
	want := []maps.Tile{
		{MapID: m.ID, LayerID: baseLayerID, X: 5, Y: 5, EntityTypeID: etID},
		{MapID: m.ID, LayerID: baseLayerID, X: 6, Y: 5, EntityTypeID: etID},
		{MapID: m.ID, LayerID: baseLayerID, X: 5, Y: 6, EntityTypeID: etID},
		{MapID: m.ID, LayerID: baseLayerID, X: 6, Y: 6, EntityTypeID: etID},
	}
	if err := svc.PlaceTiles(ctx, want); err != nil {
		t.Fatalf("PlaceTiles: %v", err)
	}

	got, err := svc.ChunkTiles(ctx, m.ID, 0, 0, 15, 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Errorf("ChunkTiles: got %d, want 4", len(got))
	}

	// Out-of-range chunk should be empty.
	out, _ := svc.ChunkTiles(ctx, m.ID, 100, 100, 200, 200)
	if len(out) != 0 {
		t.Errorf("out-of-range chunk: got %d, want 0", len(out))
	}
}

func TestPlaceTiles_RoundTripsRotationDegrees(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, etID := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()

	m, _ := svc.Create(ctx, maps.CreateInput{Name: "rot", Width: 4, Height: 4, CreatedBy: designerID})
	layers, _ := svc.Layers(ctx, m.ID)
	baseLayerID := layers[0].ID

	if err := svc.PlaceTiles(ctx, []maps.Tile{{
		MapID: m.ID, LayerID: baseLayerID, X: 1, Y: 2, EntityTypeID: etID, RotationDegrees: 90,
	}}); err != nil {
		t.Fatalf("PlaceTiles: %v", err)
	}

	got, err := svc.ChunkTiles(ctx, m.ID, 0, 0, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tiles, want 1", len(got))
	}
	if got[0].RotationDegrees != 90 {
		t.Fatalf("rotation: got %d, want 90", got[0].RotationDegrees)
	}
}

func TestPlaceTiles_RejectsInvalidRotationDegrees(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, etID := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()

	m, _ := svc.Create(ctx, maps.CreateInput{Name: "bad-rot", Width: 4, Height: 4, CreatedBy: designerID})
	layers, _ := svc.Layers(ctx, m.ID)
	baseLayerID := layers[0].ID

	err := svc.PlaceTiles(ctx, []maps.Tile{{
		MapID: m.ID, LayerID: baseLayerID, X: 1, Y: 2, EntityTypeID: etID, RotationDegrees: 45,
	}})
	if err == nil {
		t.Fatal("expected invalid rotation error")
	}
}

func TestPlaceTiles_OverwritesOnSameCell(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, etID := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()

	m, _ := svc.Create(ctx, maps.CreateInput{Name: "ov", Width: 4, Height: 4, CreatedBy: designerID})
	layers, _ := svc.Layers(ctx, m.ID)
	baseLayerID := layers[0].ID

	// First placement.
	tile := maps.Tile{MapID: m.ID, LayerID: baseLayerID, X: 0, Y: 0, EntityTypeID: etID}
	_ = svc.PlaceTiles(ctx, []maps.Tile{tile})

	// Second placement at same cell with an override.
	override := int16(99)
	tile.AnimOverride = &override
	_ = svc.PlaceTiles(ctx, []maps.Tile{tile})

	got, _ := svc.ChunkTiles(ctx, m.ID, 0, 0, 2, 2)
	if len(got) != 1 {
		t.Fatalf("expected 1 tile after overwrite, got %d", len(got))
	}
	if got[0].AnimOverride == nil || *got[0].AnimOverride != 99 {
		t.Errorf("override not applied: %+v", got[0].AnimOverride)
	}
}

func TestEraseTiles(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, etID := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()

	m, _ := svc.Create(ctx, maps.CreateInput{Name: "er", Width: 4, Height: 4, CreatedBy: designerID})
	layers, _ := svc.Layers(ctx, m.ID)
	baseLayerID := layers[0].ID

	_ = svc.PlaceTiles(ctx, []maps.Tile{
		{MapID: m.ID, LayerID: baseLayerID, X: 0, Y: 0, EntityTypeID: etID},
		{MapID: m.ID, LayerID: baseLayerID, X: 1, Y: 0, EntityTypeID: etID},
	})
	if err := svc.EraseTiles(ctx, m.ID, baseLayerID, [][2]int32{{0, 0}}); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.ChunkTiles(ctx, m.ID, 0, 0, 2, 2)
	if len(got) != 1 {
		t.Errorf("after erase: got %d, want 1", len(got))
	}
}

func TestPlaceLightingCells(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, _ := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()

	m, _ := svc.Create(ctx, maps.CreateInput{Name: "lt", Width: 4, Height: 4, CreatedBy: designerID})
	layers, _ := svc.Layers(ctx, m.ID)
	var lightID int64
	for _, l := range layers {
		if l.Kind == "lighting" {
			lightID = l.ID
		}
	}
	if lightID == 0 {
		t.Fatal("default lighting layer missing")
	}

	cells := []maps.LightingCell{
		{MapID: m.ID, LayerID: lightID, X: 0, Y: 0, Color: 0xff5e7eff, Intensity: 200},
		{MapID: m.ID, LayerID: lightID, X: 1, Y: 0, Color: 0x4ad7ffff, Intensity: 100},
	}
	if err := svc.PlaceLightingCells(ctx, cells); err != nil {
		t.Fatalf("PlaceLightingCells: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM map_lighting_cells WHERE map_id = $1`, m.ID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("got %d cells, want 2", n)
	}
}

func TestMapHandler_PublishUpdatesRow(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID, _ := resetDB(t, pool)
	svc := maps.New(pool)
	ctx := context.Background()

	live, _ := svc.Create(ctx, maps.CreateInput{
		Name: "old-map-name", Width: 8, Height: 8, CreatedBy: designerID,
	})

	seed := int64(42)
	draft := maps.MapDraft{
		Name: "new-map-name",
		Mode: "procedural",
		Seed: &seed,
	}
	body, _ := json.Marshal(draft)
	if _, err := pool.Exec(ctx,
		`INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by) VALUES ($1, $2, $3, $4)`,
		string(artifact.KindMap), live.ID, body, designerID,
	); err != nil {
		t.Fatal(err)
	}

	registry := artifact.NewRegistry()
	registry.Register(maps.NewHandler(svc))
	pipe := artifact.NewPipeline(pool, registry)

	if _, err := pipe.Run(ctx, designerID); err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}

	got, _ := svc.FindByID(ctx, live.ID)
	if got.Name != "new-map-name" || got.Mode != "procedural" || got.Seed == nil || *got.Seed != 42 {
		t.Errorf("draft did not apply: %+v", got)
	}
}

func TestMapHandler_RejectsBadEnum(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := maps.New(pool)
	h := maps.NewHandler(svc)

	body, _ := json.Marshal(maps.MapDraft{Name: "x", Mode: "weird"})
	if err := h.Validate(context.Background(), artifact.DraftRow{
		ArtifactKind: artifact.KindMap,
		DraftJSON:    body,
	}); err == nil {
		t.Error("expected validation error for bogus mode")
	}
}
