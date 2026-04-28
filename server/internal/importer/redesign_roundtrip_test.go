package importer_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	"boxland/server/internal/automations"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/exporter"
	"boxland/server/internal/folders"
	"boxland/server/internal/importer"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/tilemaps"
	"boxland/server/internal/worlds"
)

// redesign_roundtrip_test.go — proves the new .boxtilemap / .boxlevel /
// .boxworld zip kinds round-trip cleanly through the exporter +
// importer. Each test seeds a real database via testdb, exports a
// fresh artifact, deletes the source rows, then imports the bytes and
// verifies the resulting database state matches what the export
// captured.

// roundtripFixture builds a designer + every service the round-trip
// touches. Returned in a single struct because the tests below all
// need the same set.
type roundtripFixture struct {
	pool         *pgxpool.Pool
	designerID   int64
	assetSvc     *assets.Service
	entitySvc    *entities.Service
	tilemapSvc   *tilemaps.Service
	mapSvc       *mapsservice.Service
	levelSvc     *levels.Service
	worldSvc     *worlds.Service
	folderSvc    *folders.Service
	actionGroups *automations.GroupsRepo
	exp          *exporter.Service
	imp          *importer.Service
}

func newFixture(t *testing.T) *roundtripFixture {
	t.Helper()
	pool := testdb.New(t)
	if pool == nil {
		t.Skip("testdb unavailable")
	}
	ctx := context.Background()

	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(ctx, "roundtrip@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}

	asvc := assets.New(pool)
	esvc := entities.New(pool, components.Default())
	tsvc := tilemaps.New(pool, asvc, esvc)
	msvc := mapsservice.New(pool)
	lsvc := levels.New(pool)
	wsvc := worlds.New(pool)
	fsvc := folders.New(pool)
	agRepo := automations.NewGroupsRepo(pool)

	deps := exporter.Deps{
		Assets: asvc, Entities: esvc, Folders: fsvc,
		Tilemaps: tsvc, Maps: msvc, Levels: lsvc, Worlds: wsvc,
		ActionGroups: agRepo, ObjectStore: nil, BoxlandVersion: "test",
	}
	importDeps := importer.Deps{
		Assets: asvc, Entities: esvc, Folders: fsvc,
		Tilemaps: tsvc, Maps: msvc, Levels: lsvc, Worlds: wsvc,
		ActionGroups: agRepo, ObjectStore: nil,
	}
	return &roundtripFixture{
		pool: pool, designerID: d.ID,
		assetSvc: asvc, entitySvc: esvc, tilemapSvc: tsvc,
		mapSvc: msvc, levelSvc: lsvc, worldSvc: wsvc,
		folderSvc: fsvc, actionGroups: agRepo,
		exp: exporter.New(deps),
		imp: importer.New(importDeps),
	}
}

// makePNG builds a simple cols × rows tilemap PNG with one solid color
// per cell. Every cell is non-empty.
func makePNG(t *testing.T, cols, rows int) []byte {
	t.Helper()
	const ts = 32
	img := image.NewRGBA(image.Rect(0, 0, cols*ts, rows*ts))
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			col := color.RGBA{R: uint8(40 + c*30), G: uint8(40 + r*30), B: 100, A: 255}
			for y := r * ts; y < (r+1)*ts; y++ {
				for x := c * ts; x < (c+1)*ts; x++ {
					img.Set(x, y, col)
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// seedTilemap seeds an asset + tilemap on the supplied fixture.
func seedTilemap(t *testing.T, f *roundtripFixture, name string) *tilemaps.Tilemap {
	t.Helper()
	ctx := context.Background()
	body := makePNG(t, 2, 1)
	cells, meta, err := assets.SliceTileSheet(body)
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	a, err := f.assetSvc.Create(ctx, assets.CreateInput{
		Kind:                 assets.KindSpriteAnimated,
		Name:                 name + "-png",
		ContentAddressedPath: "rt/" + name + ".png",
		OriginalFormat:       "png",
		CreatedBy:            f.designerID,
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	tm, err := f.tilemapSvc.Create(ctx, tilemaps.CreateInput{
		Name: name, AssetID: a.ID, CreatedBy: f.designerID,
		Cells: cells, Meta: meta, PngBody: body,
	})
	if err != nil {
		t.Fatalf("create tilemap: %v", err)
	}
	return tm
}

// TestTilemap_RoundTrip — export a tilemap, delete it + its tile
// entities, import the bytes, verify the new tilemap row + cells +
// tile entities match the original shape.
func TestTilemap_RoundTrip(t *testing.T) {
	f := newFixture(t)
	defer f.pool.Close()
	ctx := context.Background()

	tm := seedTilemap(t, f, "Forest")
	originalNonEmpty := tm.NonEmptyCount

	// Export.
	zipBytes, err := f.exp.ExportTilemap(ctx, tm.ID, f.designerID)
	if err != nil {
		t.Fatalf("ExportTilemap: %v", err)
	}
	if len(zipBytes) == 0 {
		t.Fatal("empty zip")
	}

	// Delete the tilemap (and its tile entities — schema CASCADE).
	if err := f.tilemapSvc.Delete(ctx, tm.ID); err != nil {
		t.Fatalf("delete tilemap: %v", err)
	}
	// Verify it's gone.
	if _, err := f.tilemapSvc.FindByID(ctx, tm.ID); err == nil {
		t.Fatalf("tilemap %d should be gone", tm.ID)
	}

	// Import. The asset + tilemap survive verbatim because the asset
	// dedup key is the content_addressed_path, which is unchanged.
	res, err := f.imp.ImportTilemap(ctx, zipBytes, f.designerID, importer.PolicySkip)
	if err != nil {
		t.Fatalf("ImportTilemap: %v", err)
	}
	if res.TilemapsCreated != 1 {
		t.Errorf("TilemapsCreated = %d, want 1", res.TilemapsCreated)
	}
	if res.EntityTypesCreated < 2 {
		t.Errorf("EntityTypesCreated = %d, want >= 2", res.EntityTypesCreated)
	}

	// Verify the new tilemap exists with the same shape.
	all, err := f.tilemapSvc.List(ctx, tilemaps.ListOpts{Search: "Forest"})
	if err != nil || len(all) != 1 {
		t.Fatalf("list tilemaps: got %d (err=%v)", len(all), err)
	}
	got := all[0]
	if got.Cols != tm.Cols || got.Rows != tm.Rows || got.NonEmptyCount != originalNonEmpty {
		t.Errorf("shape mismatch: got %+v, want cols=%d rows=%d non_empty=%d",
			got, tm.Cols, tm.Rows, originalNonEmpty)
	}

	// Tile entities should be back, attached to the new tilemap.
	tileEntities, err := f.entitySvc.ListByClass(ctx, entities.ClassTile, entities.ListOpts{})
	if err != nil {
		t.Fatalf("list tile entities: %v", err)
	}
	if len(tileEntities) != int(originalNonEmpty) {
		t.Errorf("expected %d tile entities, got %d", originalNonEmpty, len(tileEntities))
	}
	for _, et := range tileEntities {
		if et.TilemapID == nil || *et.TilemapID != got.ID {
			t.Errorf("tile entity %d has tilemap_id=%v, want %d",
				et.ID, et.TilemapID, got.ID)
		}
	}
}

// TestLevel_RoundTrip — export a level, delete it (and its map +
// entity placements), import the bytes, verify the level reattaches
// to a fresh map and placements.
func TestLevel_RoundTrip(t *testing.T) {
	f := newFixture(t)
	defer f.pool.Close()
	ctx := context.Background()

	// Seed: tilemap (gives us tile entities), map with a tile placed,
	// level with one non-tile entity placement + one action group.
	tm := seedTilemap(t, f, "Plains")
	tileEntities, _ := f.entitySvc.ListByClass(ctx, entities.ClassTile, entities.ListOpts{})
	if len(tileEntities) == 0 {
		t.Fatal("no tile entities seeded")
	}
	tile := tileEntities[0]

	mp, err := f.mapSvc.Create(ctx, mapsservice.CreateInput{
		Name: "Plains-map", Width: 8, Height: 8, CreatedBy: f.designerID,
	})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	layers, _ := f.mapSvc.Layers(ctx, mp.ID)
	if err := f.mapSvc.PlaceTiles(ctx, []mapsservice.Tile{
		{MapID: mp.ID, LayerID: layers[0].ID, X: 1, Y: 1, EntityTypeID: tile.ID},
	}); err != nil {
		t.Fatalf("place tile: %v", err)
	}

	// One logic entity_type to place on the level.
	logic, err := f.entitySvc.Create(ctx, entities.CreateInput{
		Name: "Spawn point", EntityClass: entities.ClassLogic, CreatedBy: f.designerID,
	})
	if err != nil {
		t.Fatalf("create logic: %v", err)
	}

	lv, err := f.levelSvc.Create(ctx, levels.CreateInput{
		Name: "Plains-day", MapID: mp.ID, Public: true, CreatedBy: f.designerID,
	})
	if err != nil {
		t.Fatalf("create level: %v", err)
	}
	if _, err := f.levelSvc.PlaceEntity(ctx, levels.PlaceEntityInput{
		LevelID: lv.ID, EntityTypeID: logic.ID, X: 2, Y: 3,
	}); err != nil {
		t.Fatalf("place entity: %v", err)
	}
	if _, err := f.actionGroups.Upsert(ctx, lv.ID, "fanfare", []byte(`[]`)); err != nil {
		t.Fatalf("seed action group: %v", err)
	}

	// Export.
	zipBytes, err := f.exp.ExportLevel(ctx, lv.ID, f.designerID)
	if err != nil {
		t.Fatalf("ExportLevel: %v", err)
	}

	// Tear down: delete the level (placements + action groups
	// cascade), then the map + tilemap so the import has to recreate
	// everything.
	if err := f.levelSvc.Delete(ctx, lv.ID); err != nil {
		t.Fatalf("delete level: %v", err)
	}
	if err := f.mapSvc.Delete(ctx, mp.ID); err != nil {
		t.Fatalf("delete map: %v", err)
	}
	if err := f.tilemapSvc.Delete(ctx, tm.ID); err != nil {
		t.Fatalf("delete tilemap: %v", err)
	}
	// The logic entity is independent — leave it. Importing should
	// match it by name (skip + record id mapping) so the placement
	// reattaches.

	// Import.
	res, err := f.imp.ImportLevel(ctx, zipBytes, f.designerID, importer.PolicySkip)
	if err != nil {
		t.Fatalf("ImportLevel: %v", err)
	}
	if res.LevelsCreated != 1 {
		t.Errorf("LevelsCreated = %d, want 1", res.LevelsCreated)
	}
	if res.MapsCreated != 1 {
		t.Errorf("MapsCreated = %d, want 1", res.MapsCreated)
	}
	if res.TilemapsCreated != 1 {
		t.Errorf("TilemapsCreated = %d, want 1", res.TilemapsCreated)
	}

	// Find the imported level.
	got, err := f.levelSvc.List(ctx, levels.ListOpts{Search: "Plains-day"})
	if err != nil || len(got) != 1 {
		t.Fatalf("list levels post-import: got %d (err=%v)", len(got), err)
	}
	newLv := got[0]
	if !newLv.Public {
		t.Errorf("imported level should be public")
	}
	// Placements survived.
	placements, err := f.levelSvc.ListEntities(ctx, newLv.ID)
	if err != nil {
		t.Fatalf("list placements: %v", err)
	}
	if len(placements) != 1 {
		t.Errorf("expected 1 placement, got %d", len(placements))
	} else {
		if placements[0].X != 2 || placements[0].Y != 3 {
			t.Errorf("placement coords: %+v", placements[0])
		}
	}
	// Action group survived.
	groups, err := f.actionGroups.ListByLevel(ctx, newLv.ID)
	if err != nil {
		t.Fatalf("list action groups: %v", err)
	}
	if len(groups) != 1 || groups[0].Name != "fanfare" {
		t.Errorf("action groups: %+v", groups)
	}
}

// TestWorld_RoundTrip — export a 2-level world, tear everything down,
// import. Verify the world + both levels + start_level wiring are
// restored.
func TestWorld_RoundTrip(t *testing.T) {
	f := newFixture(t)
	defer f.pool.Close()
	ctx := context.Background()

	// Seed: shared tilemap + 2 maps + 2 levels under one world.
	tm := seedTilemap(t, f, "Realm")
	_ = tm

	mapA, _ := f.mapSvc.Create(ctx, mapsservice.CreateInput{
		Name: "RealmA", Width: 4, Height: 4, CreatedBy: f.designerID,
	})
	mapB, _ := f.mapSvc.Create(ctx, mapsservice.CreateInput{
		Name: "RealmB", Width: 4, Height: 4, CreatedBy: f.designerID,
	})

	w, err := f.worldSvc.Create(ctx, worlds.CreateInput{
		Name: "Realm world", CreatedBy: f.designerID,
	})
	if err != nil {
		t.Fatalf("create world: %v", err)
	}

	lvA, _ := f.levelSvc.Create(ctx, levels.CreateInput{
		Name: "Realm-A", MapID: mapA.ID, WorldID: &w.ID, Public: true, CreatedBy: f.designerID,
	})
	lvB, _ := f.levelSvc.Create(ctx, levels.CreateInput{
		Name: "Realm-B", MapID: mapB.ID, WorldID: &w.ID, CreatedBy: f.designerID,
	})
	startID := lvA.ID
	if err := f.worldSvc.SetStartLevel(ctx, w.ID, &startID); err != nil {
		t.Fatalf("set start: %v", err)
	}
	_ = lvB

	// Export.
	zipBytes, err := f.exp.ExportWorld(ctx, w.ID, f.designerID)
	if err != nil {
		t.Fatalf("ExportWorld: %v", err)
	}

	// Tear down.
	if err := f.worldSvc.Delete(ctx, w.ID); err != nil {
		t.Fatalf("delete world: %v", err)
	}
	// World delete sets levels.world_id = NULL but doesn't drop the
	// rows. To really exercise the importer we delete the levels +
	// maps too.
	for _, id := range []int64{lvA.ID, lvB.ID} {
		if err := f.levelSvc.Delete(ctx, id); err != nil {
			t.Fatalf("delete level: %v", err)
		}
	}
	for _, id := range []int64{mapA.ID, mapB.ID} {
		if err := f.mapSvc.Delete(ctx, id); err != nil {
			t.Fatalf("delete map: %v", err)
		}
	}

	// Import.
	res, err := f.imp.ImportWorld(ctx, zipBytes, f.designerID, importer.PolicySkip)
	if err != nil {
		t.Fatalf("ImportWorld: %v", err)
	}
	if res.WorldsCreated != 1 {
		t.Errorf("WorldsCreated = %d, want 1", res.WorldsCreated)
	}
	if res.LevelsCreated != 2 {
		t.Errorf("LevelsCreated = %d, want 2", res.LevelsCreated)
	}
	if res.MapsCreated != 2 {
		t.Errorf("MapsCreated = %d, want 2", res.MapsCreated)
	}

	// Find the imported world + verify start_level.
	imported, err := f.worldSvc.List(ctx, worlds.ListOpts{Search: "Realm world"})
	if err != nil || len(imported) != 1 {
		t.Fatalf("list worlds post-import: got %d (err=%v)", len(imported), err)
	}
	newW := imported[0]
	if newW.StartLevelID == nil {
		t.Errorf("StartLevelID = nil; expected start level wired")
	} else {
		// Verify the start level resolves to "Realm-A".
		startLv, err := f.levelSvc.FindByID(ctx, *newW.StartLevelID)
		if err != nil || startLv == nil || startLv.Name != "Realm-A" {
			t.Errorf("start level: err=%v level=%+v", err, startLv)
		}
	}

	// Both levels point at the imported world.
	allLvs, err := f.levelSvc.List(ctx, levels.ListOpts{WorldID: &newW.ID})
	if err != nil {
		t.Fatalf("list levels by world: %v", err)
	}
	if len(allLvs) != 2 {
		t.Errorf("expected 2 levels in world, got %d", len(allLvs))
	}
}
