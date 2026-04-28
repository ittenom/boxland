package tilemaps_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/tilemaps"
)

func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// fixture creates a designer + the service trio.
func fixture(t *testing.T, pool *pgxpool.Pool) (
	designerID int64,
	asvc *assets.Service,
	esvc *entities.Service,
	tsvc *tilemaps.Service,
) {
	t.Helper()
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "tilemaps-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	asvc = assets.New(pool)
	esvc = entities.New(pool, components.Default())
	tsvc = tilemaps.New(pool, asvc, esvc)
	return d.ID, asvc, esvc, tsvc
}

// makePNG builds a cols×rows tilemap PNG with one solid color per
// non-empty cell. emptyAt(col,row) returning true makes that cell
// fully transparent; otherwise the cell is filled with a deterministic
// distinct color so pixel hashes differ between cells.
func makePNG(t *testing.T, cols, rows int, emptyAt func(c, r int) bool) []byte {
	t.Helper()
	const ts = 32
	img := image.NewRGBA(image.Rect(0, 0, cols*ts, rows*ts))
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if emptyAt != nil && emptyAt(c, r) {
				continue
			}
			// Pick a deterministic color so different cells hash differently.
			col := color.RGBA{
				R: uint8(40 + c*30),
				G: uint8(40 + r*30),
				B: uint8(80 + (c+r)*10),
				A: 255,
			}
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

func TestCreate_HappyPath2x2NoneEmpty(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, asvc, _, tsvc := fixture(t, pool)
	ctx := context.Background()

	body := makePNG(t, 2, 2, nil)
	cells, meta, err := assets.SliceTileSheet(body)
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	a, err := asvc.Create(ctx, assets.CreateInput{
		Kind:                 assets.KindSpriteAnimated,
		Name:                 "forest.png",
		ContentAddressedPath: "tm/forest.png",
		OriginalFormat:       "png",
		CreatedBy:            dID,
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}

	tm, err := tsvc.Create(ctx, tilemaps.CreateInput{
		Name:      "Forest",
		AssetID:   a.ID,
		CreatedBy: dID,
		Cells:     cells,
		Meta:      meta,
		PngBody:   body,
	})
	if err != nil {
		t.Fatalf("Create tilemap: %v", err)
	}
	if tm.Cols != 2 || tm.Rows != 2 || tm.NonEmptyCount != 4 {
		t.Errorf("got %+v", tm)
	}

	got, err := tsvc.Cells(ctx, tm.ID)
	if err != nil {
		t.Fatalf("Cells: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 cells, got %d", len(got))
	}
	// Every cell should have a unique pixel hash (different colors above).
	seen := map[[32]byte]bool{}
	for _, c := range got {
		if seen[c.PixelHash] {
			t.Errorf("pixel hashes should be distinct: collision at (%d,%d)", c.CellCol, c.CellRow)
		}
		seen[c.PixelHash] = true
	}
}

func TestCreate_SkipsEmptyCells(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, asvc, _, tsvc := fixture(t, pool)
	ctx := context.Background()

	// 3×1 strip with the middle cell empty.
	body := makePNG(t, 3, 1, func(c, r int) bool { return c == 1 })
	cells, meta, err := assets.SliceTileSheet(body)
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	a, _ := asvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSpriteAnimated, Name: "strip.png", ContentAddressedPath: "tm/strip.png",
		OriginalFormat: "png", CreatedBy: dID,
	})
	tm, err := tsvc.Create(ctx, tilemaps.CreateInput{
		Name: "Strip", AssetID: a.ID, CreatedBy: dID,
		Cells: cells, Meta: meta, PngBody: body,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tm.NonEmptyCount != 2 {
		t.Errorf("expected 2 non-empty, got %d", tm.NonEmptyCount)
	}
	rows, _ := tsvc.Cells(ctx, tm.ID)
	if len(rows) != 2 {
		t.Fatalf("expected 2 cell rows, got %d", len(rows))
	}
	// Empty middle cell should not appear.
	for _, c := range rows {
		if c.CellCol == 1 {
			t.Errorf("empty cell should not have a tilemap_tiles row, got %+v", c)
		}
	}
}

func TestCreate_FansOutTileEntityTypes(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, asvc, esvc, tsvc := fixture(t, pool)
	ctx := context.Background()

	body := makePNG(t, 2, 1, nil)
	cells, meta, _ := assets.SliceTileSheet(body)
	a, _ := asvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSpriteAnimated, Name: "x.png", ContentAddressedPath: "x.png",
		OriginalFormat: "png", CreatedBy: dID,
	})
	tm, err := tsvc.Create(ctx, tilemaps.CreateInput{
		Name: "X", AssetID: a.ID, CreatedBy: dID,
		Cells: cells, Meta: meta, PngBody: body,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := esvc.ListByClass(ctx, entities.ClassTile, entities.ListOpts{})
	if err != nil {
		t.Fatalf("list tile entities: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tile entities, got %d", len(got))
	}
	for _, et := range got {
		if et.EntityClass != entities.ClassTile {
			t.Errorf("class = %q, want tile", et.EntityClass)
		}
		if et.TilemapID == nil || *et.TilemapID != tm.ID {
			t.Errorf("tilemap_id = %v, want %d", et.TilemapID, tm.ID)
		}
		if et.SpriteAssetID == nil || *et.SpriteAssetID != a.ID {
			t.Errorf("sprite_asset_id = %v, want %d", et.SpriteAssetID, a.ID)
		}
	}
}

func TestAdjacencyGraph_FullGrid(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, asvc, _, tsvc := fixture(t, pool)
	ctx := context.Background()

	body := makePNG(t, 2, 2, nil)
	cells, meta, _ := assets.SliceTileSheet(body)
	a, _ := asvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSpriteAnimated, Name: "g.png", ContentAddressedPath: "g.png",
		OriginalFormat: "png", CreatedBy: dID,
	})
	tm, err := tsvc.Create(ctx, tilemaps.CreateInput{
		Name: "G", AssetID: a.ID, CreatedBy: dID,
		Cells: cells, Meta: meta, PngBody: body,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	graph, err := tsvc.AdjacencyGraph(ctx, tm.ID)
	if err != nil {
		t.Fatalf("AdjacencyGraph: %v", err)
	}
	// 2×2 grid: 4 cells × (up to 2 internal neighbors each, but reported
	// per direction so each pair is counted twice). Total internal edges
	// = 4 horizontal + 4 vertical = 8.
	if len(graph) != 8 {
		t.Errorf("expected 8 adjacencies in 2x2 grid, got %d (%+v)", len(graph), graph)
	}
}

func TestAdjacencyGraph_HoleHasNoNeighbors(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, asvc, _, tsvc := fixture(t, pool)
	ctx := context.Background()

	// 3×1 strip with middle empty: only the two outer cells have any
	// tile_tiles rows, and they're NOT adjacent (the middle is empty).
	body := makePNG(t, 3, 1, func(c, r int) bool { return c == 1 })
	cells, meta, _ := assets.SliceTileSheet(body)
	a, _ := asvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSpriteAnimated, Name: "h.png", ContentAddressedPath: "h.png",
		OriginalFormat: "png", CreatedBy: dID,
	})
	tm, _ := tsvc.Create(ctx, tilemaps.CreateInput{
		Name: "H", AssetID: a.ID, CreatedBy: dID,
		Cells: cells, Meta: meta, PngBody: body,
	})
	graph, err := tsvc.AdjacencyGraph(ctx, tm.ID)
	if err != nil {
		t.Fatalf("AdjacencyGraph: %v", err)
	}
	if len(graph) != 0 {
		t.Errorf("expected 0 adjacencies (hole separates the cells), got %d", len(graph))
	}
}

func TestCreate_AssetAlreadyUsedRejected(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, asvc, _, tsvc := fixture(t, pool)
	ctx := context.Background()

	body := makePNG(t, 2, 1, nil)
	cells, meta, _ := assets.SliceTileSheet(body)
	a, _ := asvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSpriteAnimated, Name: "u.png", ContentAddressedPath: "u.png",
		OriginalFormat: "png", CreatedBy: dID,
	})
	if _, err := tsvc.Create(ctx, tilemaps.CreateInput{
		Name: "First", AssetID: a.ID, CreatedBy: dID,
		Cells: cells, Meta: meta, PngBody: body,
	}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := tsvc.Create(ctx, tilemaps.CreateInput{
		Name: "Second", AssetID: a.ID, CreatedBy: dID,
		Cells: cells, Meta: meta, PngBody: body,
	})
	if err == nil {
		t.Fatal("expected error on duplicate asset_id")
	}
}

func TestFindByAssetID(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	dID, asvc, _, tsvc := fixture(t, pool)
	ctx := context.Background()

	body := makePNG(t, 2, 1, nil)
	cells, meta, _ := assets.SliceTileSheet(body)
	a, _ := asvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSpriteAnimated, Name: "f.png", ContentAddressedPath: "f.png",
		OriginalFormat: "png", CreatedBy: dID,
	})
	tm, _ := tsvc.Create(ctx, tilemaps.CreateInput{
		Name: "F", AssetID: a.ID, CreatedBy: dID,
		Cells: cells, Meta: meta, PngBody: body,
	})
	got, err := tsvc.FindByAssetID(ctx, a.ID)
	if err != nil {
		t.Fatalf("FindByAssetID: %v", err)
	}
	if got.ID != tm.ID {
		t.Errorf("got %d, want %d", got.ID, tm.ID)
	}
}
