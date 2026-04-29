package ws

import (
	"context"
	"testing"

	flatbuffers "github.com/google/flatbuffers/go"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/proto"
	"boxland/server/internal/sim/editor"
)

// snapshotFixture wires up real services + a designer + map +
// level + tile + level entity_type so the snapshot encoder has
// real rows to render.
func snapshotFixture(t *testing.T) (deps EditorAuthoringDeps, mapID, levelID int64, tileTypeID, npcTypeID int64) {
	t.Helper()
	pool := testdb.New(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()

	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(ctx, "snapshot@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	mp := mapsservice.New(pool)
	m, err := mp.Create(ctx, mapsservice.CreateInput{Name: "snap-m", Width: 8, Height: 8, CreatedBy: d.ID})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	lvSvc := levels.New(pool)
	lv, err := lvSvc.Create(ctx, levels.CreateInput{Name: "snap-lv", MapID: m.ID, CreatedBy: d.ID})
	if err != nil {
		t.Fatalf("create level: %v", err)
	}
	es := entities.New(pool, components.Default())
	tile, err := es.Create(ctx, entities.CreateInput{Name: "grass", EntityClass: entities.ClassTile, CreatedBy: d.ID})
	if err != nil {
		t.Fatalf("create tile: %v", err)
	}
	npc, err := es.Create(ctx, entities.CreateInput{Name: "guard", EntityClass: entities.ClassNPC, CreatedBy: d.ID})
	if err != nil {
		t.Fatalf("create npc: %v", err)
	}
	as := assets.New(pool)
	deps = EditorAuthoringDeps{
		Sessions: editor.NewManager(),
		Levels:   lvSvc,
		Maps:     mp,
		Entities: es,
		Assets:   as,
	}
	return deps, m.ID, lv.ID, tile.ID, npc.ID
}

// TestBuildEditorSnapshot_LevelEditorBody covers a snapshot for
// the level editor: header fields, palette pulled from the npc/pc/
// logic classes, body populated with placements.
func TestBuildEditorSnapshot_LevelEditorBody(t *testing.T) {
	deps, _, levelID, _, npcTypeID := snapshotFixture(t)
	ctx := context.Background()

	// One placement so the body has something interesting.
	if _, err := deps.Levels.PlaceEntity(ctx, levels.PlaceEntityInput{
		LevelID: levelID, EntityTypeID: npcTypeID, X: 1, Y: 2,
	}); err != nil {
		t.Fatalf("place: %v", err)
	}

	bytes, err := buildEditorSnapshot(ctx, deps, editor.KindLevelEditor, levelID)
	if err != nil {
		t.Fatalf("buildEditorSnapshot: %v", err)
	}
	if !proto.EditorSnapshotBufferHasIdentifier(bytes) {
		t.Fatalf("snapshot missing %s file identifier", proto.EditorSnapshotIdentifier)
	}
	snap := proto.GetRootAsEditorSnapshot(bytes, 0)
	if snap.Kind() != proto.EditorKindLevelEditor {
		t.Errorf("kind: got %v want LevelEditor", snap.Kind())
	}
	body := snap.LevelEditorBody(nil)
	if body == nil {
		t.Fatalf("level body nil")
	}
	if body.PlacementsLength() != 1 {
		t.Errorf("placements: got %d, want 1", body.PlacementsLength())
	}
	// Palette should contain at least the npc.
	foundNPC := false
	for i := 0; i < snap.PaletteLength(); i++ {
		var pe proto.EditorPaletteEntry
		if !snap.Palette(&pe, i) {
			continue
		}
		if int64(pe.EntityTypeId()) == npcTypeID {
			foundNPC = true
			if string(pe.Class()) != "npc" {
				t.Errorf("class wrong: got %s want npc", pe.Class())
			}
			break
		}
	}
	if !foundNPC {
		t.Errorf("palette missing npc entity_type %d", npcTypeID)
	}
}

// TestBuildEditorSnapshot_MapmakerBody covers the mapmaker side:
// map dims + layers + tiles + locks on the body, palette pulled
// from the tile class.
func TestBuildEditorSnapshot_MapmakerBody(t *testing.T) {
	deps, mapID, _, tileTypeID, _ := snapshotFixture(t)
	ctx := context.Background()

	layers, _ := deps.Maps.Layers(ctx, mapID)
	if len(layers) == 0 {
		t.Fatal("no default layers")
	}
	if err := deps.Maps.PlaceTiles(ctx, []mapsservice.Tile{
		{MapID: mapID, LayerID: layers[0].ID, X: 0, Y: 0, EntityTypeID: tileTypeID},
		{MapID: mapID, LayerID: layers[0].ID, X: 1, Y: 0, EntityTypeID: tileTypeID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := deps.Maps.LockCells(ctx, []mapsservice.LockedCell{
		{MapID: mapID, LayerID: layers[0].ID, X: 0, Y: 0, EntityTypeID: tileTypeID},
	}); err != nil {
		t.Fatal(err)
	}

	bytes, err := buildEditorSnapshot(ctx, deps, editor.KindMapmaker, mapID)
	if err != nil {
		t.Fatalf("buildEditorSnapshot: %v", err)
	}
	if !proto.EditorSnapshotBufferHasIdentifier(bytes) {
		t.Fatalf("snapshot missing %s file identifier", proto.EditorSnapshotIdentifier)
	}
	snap := proto.GetRootAsEditorSnapshot(bytes, 0)
	if snap.Kind() != proto.EditorKindMapmaker {
		t.Errorf("kind: got %v want Mapmaker", snap.Kind())
	}
	body := snap.MapmakerBody(nil)
	if body == nil {
		t.Fatalf("mapmaker body nil")
	}
	if body.Width() != 8 || body.Height() != 8 {
		t.Errorf("dims: got %dx%d", body.Width(), body.Height())
	}
	if body.LayersLength() < 1 {
		t.Errorf("layers: got %d", body.LayersLength())
	}
	if body.TilesLength() != 2 {
		t.Errorf("tiles: got %d, want 2", body.TilesLength())
	}
	if body.LocksLength() != 1 {
		t.Errorf("locks: got %d, want 1", body.LocksLength())
	}
	// Palette should contain the tile type.
	foundTile := false
	for i := 0; i < snap.PaletteLength(); i++ {
		var pe proto.EditorPaletteEntry
		if !snap.Palette(&pe, i) {
			continue
		}
		if int64(pe.EntityTypeId()) == tileTypeID {
			foundTile = true
			if string(pe.Class()) != "tile" {
				t.Errorf("class wrong: got %s want tile", pe.Class())
			}
		}
	}
	if !foundTile {
		t.Errorf("palette missing tile entity_type %d", tileTypeID)
	}
}

// TestBuildEditorSnapshot_EmptyTheme verifies the snapshot ships
// an empty theme[] when no ClassUI types exist (the typical path
// before `boxland seed` runs).
func TestBuildEditorSnapshot_EmptyTheme(t *testing.T) {
	deps, _, levelID, _, _ := snapshotFixture(t)
	ctx := context.Background()
	bytes, err := buildEditorSnapshot(ctx, deps, editor.KindLevelEditor, levelID)
	if err != nil {
		t.Fatalf("buildEditorSnapshot: %v", err)
	}
	snap := proto.GetRootAsEditorSnapshot(bytes, 0)
	if snap.ThemeLength() != 0 {
		t.Errorf("theme: got %d, want 0 (no ClassUI types seeded)", snap.ThemeLength())
	}
}

func TestEncodeEditorDiff_IncludesEditorFileIdentifier(t *testing.T) {
	bytes, err := encodeEditorDiff(editor.Diff{
		Kind:      editor.DiffHistoryChanged,
		UndoDepth: 2,
		RedoDepth: 1,
	})
	if err != nil {
		t.Fatalf("encodeEditorDiff: %v", err)
	}
	if !flatbuffers.BufferHasIdentifier(bytes, proto.EditorSnapshotIdentifier) {
		t.Fatalf("diff missing %s file identifier", proto.EditorSnapshotIdentifier)
	}
	diff := proto.GetRootAsEditorDiff(bytes, 0)
	if diff.Kind() != proto.EditorDiffKindHistoryChanged {
		t.Fatalf("kind: got %v want HistoryChanged", diff.Kind())
	}
}
