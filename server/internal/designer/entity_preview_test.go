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
)

// TestEntityDetail_PreviewCarriesAtlasSlicingData regresses the
// "preview is blank" customer report. The detail modal canvas needs
// data-atlas-index, data-atlas-cols, and data-tile-size so the JS
// can crop the source sheet down to the entity's single cell. Without
// these, the overlay used to draw the whole 109-tile sheet shrunk
// to 64px and looked empty.
func TestEntityDetail_PreviewCarriesAtlasSlicingData(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)

	// Tile-sheet asset: 3 cols x 2 rows of 32px cells. The metadata
	// shape mirrors what the upload pipeline writes to assets.metadata_json.
	md := assets.TileSheetMetadata{
		TileSize: assets.TileSize, Cols: 3, Rows: 2, NonEmptyCount: 6,
		NonEmptyIndex: []int{0, 1, 2, 3, 4, 5},
	}
	mdRaw, _ := json.Marshal(md)
	sheet, err := deps.Assets.Create(ctx, assets.CreateInput{
		Kind:                 assets.KindSpriteAnimated,
		Name:                 "forest-sheet",
		ContentAddressedPath: "fs/forest.png",
		OriginalFormat:       "png",
		MetadataJSON:         mdRaw,
		CreatedBy:            designerID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Pick a non-zero atlas index so we know the canvas carries the
	// real value, not just a defaulted-to-0 attribute.
	et, err := deps.Entities.Create(ctx, entities.CreateInput{
		Name:          "forest-r1c2",
		SpriteAssetID: &sheet.ID,
		AtlasIndex:    5,
		CreatedBy:     designerID,
	})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := authedReq(http.MethodGet, "/design/entities/"+itoa(et.ID), tok, nil)
	req.Header.Set("HX-Request", "true")
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`data-atlas-index="5"`,
		`data-atlas-cols="3"`,
		`data-tile-size="32"`,
		`data-sprite-url="/design/assets/blob/`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in detail body; got:\n%s", want, body)
		}
	}
}

// TestEntityDetail_PreviewDefaultsForPlainSprite makes sure a
// non-tile-sheet sprite asset (no metadata_json) still renders sane
// data attributes — atlas-cols=1, tile-size=32, atlas-index=0 — so
// the JS treats the whole PNG as one cell instead of dividing by
// zero or skipping the draw.
func TestEntityDetail_PreviewDefaultsForPlainSprite(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)

	a, err := deps.Assets.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "hero", ContentAddressedPath: "h/hero.png",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	et, err := deps.Entities.Create(ctx, entities.CreateInput{
		Name: "hero", SpriteAssetID: &a.ID, CreatedBy: designerID,
	})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := authedReq(http.MethodGet, "/design/entities/"+itoa(et.ID), tok, nil)
	req.Header.Set("HX-Request", "true")
	srv.ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, want := range []string{
		`data-atlas-index="0"`,
		`data-atlas-cols="1"`,
		`data-tile-size="32"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in detail body", want)
		}
	}
}

// TestEntityDetail_PreviewWithoutSprite makes sure entity types that
// never picked a sprite still render the canvas with the safe fallback
// attributes — empty data-sprite-url, cols=1, size=32 — so the JS
// branches into "draw outline only" instead of throwing.
func TestEntityDetail_PreviewWithoutSprite(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)

	et, _ := deps.Entities.Create(ctx, entities.CreateInput{
		Name: "ghost", CreatedBy: designerID,
	})

	rr := httptest.NewRecorder()
	req := authedReq(http.MethodGet, "/design/entities/"+itoa(et.ID), tok, nil)
	req.Header.Set("HX-Request", "true")
	srv.ServeHTTP(rr, req)
	body := rr.Body.String()
	for _, want := range []string{
		`data-bx-collider-overlay`,
		`data-sprite-url=""`,
		`data-atlas-cols="1"`,
		`data-tile-size="32"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in detail body", want)
		}
	}
}
