package editor_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/sim/editor"
)

// mapFixture seeds a designer + map (with one tile layer) + tile
// entity_type so map-op tests have a runnable environment.
func mapFixture(t *testing.T) (pool *pgxpool.Pool, deps editor.Deps, designerID, mapID, layerID, tileTypeID int64) {
	t.Helper()
	pool = testdb.New(t)
	t.Cleanup(pool.Close)

	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "map-ops@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	mp := mapsservice.New(pool)
	m, err := mp.Create(context.Background(), mapsservice.CreateInput{Name: "m", Width: 16, Height: 16, CreatedBy: d.ID})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	// The default tile layer ships with maps but we explicitly
	// pull it for clarity.
	layers, err := mp.Layers(context.Background(), m.ID)
	if err != nil || len(layers) == 0 {
		t.Fatalf("expected default layer, got %v err=%v", layers, err)
	}
	es := entities.New(pool, components.Default())
	et, err := es.Create(context.Background(), entities.CreateInput{Name: "grass", EntityClass: entities.ClassTile, CreatedBy: d.ID})
	if err != nil {
		t.Fatalf("create tile entity_type: %v", err)
	}
	lvSvc := levels.New(pool)
	return pool, editor.Deps{Levels: lvSvc, Maps: mp}, d.ID, m.ID, layers[0].ID, et.ID
}

// PlaceTilesOp persists every tile + emits a headline diff for the
// first tile and ExtraDiffs for the rest.
func TestSession_PlaceTilesOp_RoundTrip(t *testing.T) {
	_, deps, _, mapID, layerID, etID := mapFixture(t)
	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindMapmaker, TargetID: mapID})

	tiles := []mapsservice.Tile{
		{MapID: mapID, LayerID: layerID, X: 0, Y: 0, EntityTypeID: etID},
		{MapID: mapID, LayerID: layerID, X: 1, Y: 0, EntityTypeID: etID},
		{MapID: mapID, LayerID: layerID, X: 2, Y: 0, EntityTypeID: etID},
	}
	op := &editor.PlaceTilesOp{MapID: mapID, Tiles: tiles}
	diff, err := ses.Apply(context.Background(), deps, op)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if diff.Kind != editor.DiffTilePlaced {
		t.Errorf("kind: got %d want TilePlaced", diff.Kind)
	}
	if got := op.ExtraDiffs(); len(got) != 2 {
		t.Errorf("ExtraDiffs: got %d, want 2", len(got))
	}

	// All three tiles should be present in the map.
	got, err := deps.Maps.ChunkTiles(context.Background(), mapID, 0, 0, 15, 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 tiles, got %d", len(got))
	}
}

// Undoing a place op on cells that were empty before erases them.
func TestSession_PlaceTilesOp_UndoRestoresEmpty(t *testing.T) {
	_, deps, _, mapID, layerID, etID := mapFixture(t)
	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindMapmaker, TargetID: mapID})
	ctx := context.Background()

	tiles := []mapsservice.Tile{
		{MapID: mapID, LayerID: layerID, X: 5, Y: 5, EntityTypeID: etID},
		{MapID: mapID, LayerID: layerID, X: 6, Y: 5, EntityTypeID: etID},
	}
	if _, err := ses.Apply(ctx, deps, &editor.PlaceTilesOp{MapID: mapID, Tiles: tiles}); err != nil {
		t.Fatal(err)
	}
	if _, err := ses.Undo(ctx, deps); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	got, _ := deps.Maps.ChunkTiles(ctx, mapID, 0, 0, 15, 15)
	if len(got) != 0 {
		t.Errorf("after undo: %d tiles, want 0", len(got))
	}
}

// Undoing a place op that *replaced* an existing tile restores
// the prior tile's entity_type, not just an empty cell.
func TestSession_PlaceTilesOp_UndoRestoresPriorTile(t *testing.T) {
	pool, deps, designerID, mapID, layerID, etID := mapFixture(t)
	es := entities.New(pool, components.Default())
	other, err := es.Create(context.Background(), entities.CreateInput{Name: "stone", EntityClass: entities.ClassTile, CreatedBy: designerID})
	if err != nil {
		t.Fatalf("create other entity_type: %v", err)
	}
	ctx := context.Background()

	// Seed the cell with `other` first (legacy direct path; we
	// need a starting tile that wasn't placed via the session).
	if err := deps.Maps.PlaceTiles(ctx, []mapsservice.Tile{
		{MapID: mapID, LayerID: layerID, X: 9, Y: 9, EntityTypeID: other.ID},
	}); err != nil {
		t.Fatal(err)
	}

	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindMapmaker, TargetID: mapID})
	if _, err := ses.Apply(ctx, deps, &editor.PlaceTilesOp{
		MapID: mapID,
		Tiles: []mapsservice.Tile{{MapID: mapID, LayerID: layerID, X: 9, Y: 9, EntityTypeID: etID}},
	}); err != nil {
		t.Fatal(err)
	}

	// Undo should restore `other`.
	if _, err := ses.Undo(ctx, deps); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	got, _ := deps.Maps.ChunkTiles(ctx, mapID, 9, 9, 9, 9)
	if len(got) != 1 || got[0].EntityTypeID != other.ID {
		t.Errorf("after undo: got %+v want a single tile of entity %d", got, other.ID)
	}
}

// EraseTilesOp + Undo restores the erased rows.
func TestSession_EraseTilesOp_UndoRestores(t *testing.T) {
	_, deps, _, mapID, layerID, etID := mapFixture(t)
	ctx := context.Background()
	if err := deps.Maps.PlaceTiles(ctx, []mapsservice.Tile{
		{MapID: mapID, LayerID: layerID, X: 1, Y: 1, EntityTypeID: etID},
		{MapID: mapID, LayerID: layerID, X: 2, Y: 1, EntityTypeID: etID},
	}); err != nil {
		t.Fatal(err)
	}
	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindMapmaker, TargetID: mapID})

	if _, err := ses.Apply(ctx, deps, &editor.EraseTilesOp{
		MapID: mapID, LayerID: layerID,
		Points: [][2]int32{{1, 1}, {2, 1}},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := deps.Maps.ChunkTiles(ctx, mapID, 0, 0, 15, 15)
	if len(got) != 0 {
		t.Fatalf("after erase: %d, want 0", len(got))
	}

	if _, err := ses.Undo(ctx, deps); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	got, _ = deps.Maps.ChunkTiles(ctx, mapID, 0, 0, 15, 15)
	if len(got) != 2 {
		t.Errorf("after undo erase: %d, want 2", len(got))
	}
}

// LockTilesOp + UnlockTilesOp round-trip via session, with undo
// restoring the prior lock state.
func TestSession_LockUnlock_RoundTrip(t *testing.T) {
	_, deps, _, mapID, layerID, etID := mapFixture(t)
	ctx := context.Background()
	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindMapmaker, TargetID: mapID})

	// Lock two cells.
	if _, err := ses.Apply(ctx, deps, &editor.LockTilesOp{
		MapID: mapID,
		Cells: []mapsservice.LockedCell{
			{MapID: mapID, LayerID: layerID, X: 4, Y: 4, EntityTypeID: etID},
			{MapID: mapID, LayerID: layerID, X: 5, Y: 4, EntityTypeID: etID},
		},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := deps.Maps.LockedCells(ctx, mapID)
	if len(got) != 2 {
		t.Fatalf("after lock: %d, want 2", len(got))
	}

	// Unlock one.
	if _, err := ses.Apply(ctx, deps, &editor.UnlockTilesOp{
		MapID: mapID, LayerID: layerID,
		Points: [][2]int32{{4, 4}},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = deps.Maps.LockedCells(ctx, mapID)
	if len(got) != 1 {
		t.Fatalf("after unlock: %d, want 1", len(got))
	}

	// Undo unlock => 2 again.
	if _, err := ses.Undo(ctx, deps); err != nil {
		t.Fatal(err)
	}
	got, _ = deps.Maps.LockedCells(ctx, mapID)
	if len(got) != 2 {
		t.Errorf("after undo unlock: %d, want 2", len(got))
	}
}

// Subscribers receive one diff per cell (headline + ExtraDiffs).
func TestSession_PlaceTilesOp_BroadcastsPerCell(t *testing.T) {
	_, deps, _, mapID, layerID, etID := mapFixture(t)
	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindMapmaker, TargetID: mapID})
	sink := make(chan editor.Diff, 16)
	ses.Subscribe(1, sink)

	tiles := []mapsservice.Tile{
		{MapID: mapID, LayerID: layerID, X: 0, Y: 0, EntityTypeID: etID},
		{MapID: mapID, LayerID: layerID, X: 1, Y: 0, EntityTypeID: etID},
		{MapID: mapID, LayerID: layerID, X: 2, Y: 0, EntityTypeID: etID},
	}
	if _, err := ses.Apply(context.Background(), deps, &editor.PlaceTilesOp{MapID: mapID, Tiles: tiles}); err != nil {
		t.Fatal(err)
	}
	close(sink)
	count := 0
	for d := range sink {
		if d.Kind != editor.DiffTilePlaced {
			t.Errorf("unexpected diff kind: %v", d.Kind)
		}
		count++
	}
	if count != 3 {
		t.Errorf("broadcast count: got %d, want 3", count)
	}
}
