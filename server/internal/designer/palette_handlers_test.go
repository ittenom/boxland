package designer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
	mapsservice "boxland/server/internal/maps"
)

// palette_handlers_test.go — JSON catalog endpoints feeding the
// Pixi-driven editor canvases. Same fixture shape as
// level_entities_handlers_test.go (reuse the shared helpers).

func TestGetLevelEntityTypes_ReturnsAllPlaceableClasses(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, levelID, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()

	// Seed one entity_type of each placeable class so the response
	// covers the union (the fixture only seeds a logic-class one).
	if _, err := deps.Entities.Create(ctx, entities.CreateInput{Name: "npc-villager", EntityClass: entities.ClassNPC, CreatedBy: 1}); err != nil {
		t.Fatalf("create npc: %v", err)
	}
	if _, err := deps.Entities.Create(ctx, entities.CreateInput{Name: "pc-hero", EntityClass: entities.ClassPC, CreatedBy: 1}); err != nil {
		t.Fatalf("create pc: %v", err)
	}
	// Tile-class should NOT appear in the response (placement catalog
	// is npc/pc/logic only).
	if _, err := deps.Entities.Create(ctx, entities.CreateInput{Name: "tile-grass", EntityClass: entities.ClassTile, CreatedBy: 1}); err != nil {
		t.Fatalf("create tile: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/levels/"+itoa(levelID)+"/entity-types", tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Entries []struct {
			ID    int64  `json:"id"`
			Name  string `json:"name"`
			Class string `json:"class"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	classes := map[string]bool{}
	names := map[string]bool{}
	for _, e := range resp.Entries {
		classes[e.Class] = true
		names[e.Name] = true
	}
	for _, want := range []string{"npc", "pc", "logic"} {
		if !classes[want] {
			t.Errorf("expected class %q in response, got classes=%v", want, classes)
		}
	}
	if classes["tile"] {
		t.Errorf("tile class should NOT appear in level entity-types: %v", classes)
	}
	if !names["npc-villager"] || !names["pc-hero"] || !names["spawn"] {
		t.Errorf("missing expected names: %v", names)
	}
	if names["tile-grass"] {
		t.Errorf("tile-grass should not appear in placement catalog")
	}
}

func TestGetLevelEntityTypes_NotFoundOnMissingLevel(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, _, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/levels/9999999/entity-types", tok, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing level should 404, got %d", rr.Code)
	}
}

func TestGetLevelEntityTypes_AuthRequired(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, _, levelID, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/design/levels/"+itoa(levelID)+"/entity-types", nil)
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound && rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauth should 302/401, got %d", rr.Code)
	}
}

// TestGetMapTileTypes_RoundTrips seeds a tile-class entity type with
// a real tile-sheet asset (cols=4, tileSize=32), places it on a map,
// and asserts the endpoint returns the atlas info the JS-side
// StaticAssetCatalog needs to render the tile.
func TestGetMapTileTypes_RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, _, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()

	// One real-shaped tile sheet asset + one tile-class entity type
	// pointing at cell index 5 within it.
	md, err := assets.MarshalTileSheetMetadata(assets.TileSheetMetadata{
		TileSize: assets.TileSize, Cols: 4, Rows: 2,
		NonEmptyCount: 8, NonEmptyIndex: []int{0, 1, 2, 3, 4, 5, 6, 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := deps.Assets.Create(ctx, assets.CreateInput{
		Kind: assets.KindSpriteAnimated, Name: "town-sheet",
		ContentAddressedPath: "assets/town.png", OriginalFormat: "png",
		MetadataJSON: md, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	et, err := deps.Entities.Create(ctx, entities.CreateInput{
		Name: "town-wall", EntityClass: entities.ClassTile,
		SpriteAssetID: &a.ID, AtlasIndex: 5, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// New map; place one tile of that type.
	m, err := deps.Maps.Create(ctx, mapsservice.CreateInput{
		Name: "tile-types-map", Width: 4, Height: 4, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	layers, err := deps.Maps.Layers(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.Maps.PlaceTiles(ctx, []mapsservice.Tile{
		{MapID: m.ID, LayerID: layers[0].ID, X: 1, Y: 2, EntityTypeID: et.ID},
	}); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/maps/"+itoa(m.ID)+"/tile-types", tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Entries []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Class      string `json:"class"`
			SpriteURL  string `json:"sprite_url"`
			AtlasIndex int32  `json:"atlas_index"`
			AtlasCols  int32  `json:"atlas_cols"`
			TileSize   int32  `json:"tile_size"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("expected 1 distinct tile type, got %d: %s", len(resp.Entries), rr.Body.String())
	}
	got := resp.Entries[0]
	if got.ID != et.ID || got.Name != "town-wall" || got.Class != "tile" {
		t.Errorf("unexpected entry: %+v", got)
	}
	if got.AtlasIndex != 5 || got.AtlasCols != 4 || got.TileSize != 32 {
		t.Errorf("atlas info wrong: %+v", got)
	}
	if !strings.HasPrefix(got.SpriteURL, "/design/assets/blob/") {
		t.Errorf("sprite_url should be /design/assets/blob/{id}, got %q", got.SpriteURL)
	}
}

func TestGetMapTileTypes_EmptyMap(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, _, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()

	m, err := deps.Maps.Create(ctx, mapsservice.CreateInput{
		Name: "empty-map", Width: 4, Height: 4, CreatedBy: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/maps/"+itoa(m.ID)+"/tile-types", tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"entries":[]`) {
		t.Errorf("empty map should return empty array, got %s", rr.Body.String())
	}
}

func TestGetMapTileTypes_NotFoundOnMissingMap(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, _, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/maps/9999999/tile-types", tok, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing map should 404, got %d", rr.Code)
	}
}
