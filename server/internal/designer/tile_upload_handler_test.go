package designer_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
	"boxland/server/internal/tilemaps"
)

// makeTileSheetPNG builds a 64x32 PNG (2x1 cells of 32x32). Both cells
// are non-empty so the auto-slicer creates two entity_types.
func makeTileSheetPNG(t *testing.T, cols, rows int) []byte {
	t.Helper()
	w, h := cols*32, rows*32
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			// Distinct color per cell so a regression that confuses
			// cell origins would be visible in a manual debug session.
			fill := color.NRGBA{
				R: uint8((c * 90) % 255),
				G: uint8((r * 90) % 255),
				B: 200, A: 255,
			}
			for y := r * 32; y < (r+1)*32; y++ {
				for x := c * 32; x < (c+1)*32; x++ {
					img.SetNRGBA(x, y, fill)
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// multipartUploadWithKind builds a multipart body that includes both a
// "files" PNG and a "kind" form field — same shape the real upload
// modal sends. The legacy "file" field path is still tested via the
// existing TestAssetUpload_ViaHTMX_ReturnsToast helper.
func multipartUploadWithKind(t *testing.T, body []byte, filename, kind string) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	if kind != "" {
		if err := mw.WriteField("kind", kind); err != nil {
			t.Fatal(err)
		}
	}
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="files"; filename="` + filename + `"`}
	hdr["Content-Type"] = []string{"image/png"}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(part, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

// TestAssetUpload_KindFromFormHonored covers the regression that was
// dropping the modal's <select name="kind"> on the floor: the upload
// handler used to read kind from r.URL.Query() only, so a tile upload
// silently became a sprite. With the form-aware lookup, the asset is
// now created as the requested kind.
func TestAssetUpload_KindFromFormHonored(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)

	body, ct := multipartUploadWithKind(t, makeTileSheetPNG(t, 1, 1), "wall.png", assets.KindOverrideTilemap)
	tok, _ := deps.Auth.OpenSession(context.Background(), designerID, "ua", nil)
	req := authedReq(http.MethodPost, "/design/assets/upload", tok, body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("HX-Request", "true")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}

	// The asset row should be tagged tile, not sprite.
	list, err := deps.Assets.List(context.Background(), assets.ListOpts{})
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 asset; got %d", len(list))
	}
	if list[0].Kind != assets.KindSpriteAnimated {
		t.Errorf("asset kind = %q; want %q (form-field kind=tilemap must override sniff)", list[0].Kind, assets.KindSpriteAnimated)
	}
}

// TestAssetUpload_TilemapAutoCreates is the headline regression test:
// uploading an N-cell tile sheet should produce
//   1) one `assets` row (kind=sprite_animated)
//   2) one `tilemaps` row pointing at that asset
//   3) N tile-class `entity_types` rows linked to the tilemap, each
//      with a distinct atlas_index in [0..N-1]
//
// in a single HTTP request — no manual "create tilemap" step. The
// caller-visible toast advertises the slice count so the designer
// can confirm at a glance that the tilemap landed.
func TestAssetUpload_TilemapAutoCreates(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()

	// 3 cols x 2 rows = 6 cells, all non-empty.
	body, ct := multipartUploadWithKind(t, makeTileSheetPNG(t, 3, 2), "town.png", assets.KindOverrideTilemap)
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)
	req := authedReq(http.MethodPost, "/design/assets/upload", tok, body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("HX-Request", "true")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}

	// (1) Asset row exists and is the new sprite_animated kind.
	list, err := deps.Assets.List(ctx, assets.ListOpts{})
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 asset; got %d", len(list))
	}
	if list[0].Kind != assets.KindSpriteAnimated {
		t.Errorf("asset kind = %q; want %q", list[0].Kind, assets.KindSpriteAnimated)
	}

	// (2) Tilemap row exists, pointing at the asset.
	tms, err := deps.Tilemaps.List(ctx, tilemaps.ListOpts{Limit: 32})
	if err != nil {
		t.Fatalf("list tilemaps: %v", err)
	}
	if len(tms) != 1 {
		t.Fatalf("expected 1 tilemap row; got %d", len(tms))
	}
	if tms[0].AssetID != list[0].ID {
		t.Errorf("tilemap.asset_id = %d; want %d", tms[0].AssetID, list[0].ID)
	}
	if tms[0].NonEmptyCount != 6 {
		t.Errorf("tilemap.non_empty_count = %d; want 6", tms[0].NonEmptyCount)
	}

	// (3) Six tile-class entity_types, each linked to the tilemap.
	ets, err := deps.Entities.ListByClass(ctx, entities.ClassTile, entities.ListOpts{Limit: 100})
	if err != nil {
		t.Fatalf("list tile entities: %v", err)
	}
	if len(ets) != 6 {
		t.Fatalf("expected 6 tile entities; got %d", len(ets))
	}
	seen := make(map[int32]bool, len(ets))
	for _, e := range ets {
		if e.SpriteAssetID == nil {
			t.Errorf("entity %d has no sprite asset", e.ID)
			continue
		}
		if e.TilemapID == nil || *e.TilemapID != tms[0].ID {
			t.Errorf("entity %d tilemap_id = %v; want %d", e.ID, e.TilemapID, tms[0].ID)
		}
		if seen[e.AtlasIndex] {
			t.Errorf("duplicate atlas_index %d", e.AtlasIndex)
		}
		seen[e.AtlasIndex] = true
	}
	for i := int32(0); i < 6; i++ {
		if !seen[i] {
			t.Errorf("missing atlas_index %d", i)
		}
	}

	// Toast advertises the slice count.
	if !strings.Contains(rr.Body.String(), "6 tiles") {
		t.Errorf("expected '6 tiles' in toast; body=%s", rr.Body.String())
	}
}

// TestAssetUpload_TilemapReuploadIsIdempotent — re-uploading the same
// bytes shouldn't double the tilemap or the tile-entity set.
// Designers hit this iterating in Aseprite + dragging back in.
func TestAssetUpload_TilemapReuploadIsIdempotent(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)

	pngBytes := makeTileSheetPNG(t, 2, 2) // 4 cells

	for i := 0; i < 2; i++ {
		body, ct := multipartUploadWithKind(t, pngBytes, "sheet.png", assets.KindOverrideTilemap)
		req := authedReq(http.MethodPost, "/design/assets/upload", tok, body)
		req.Header.Set("Content-Type", ct)
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("upload %d: status %d, body=%s", i, rr.Code, rr.Body.String())
		}
	}

	tms, _ := deps.Tilemaps.List(ctx, tilemaps.ListOpts{Limit: 32})
	if len(tms) != 1 {
		t.Errorf("re-upload should keep one tilemap; got %d", len(tms))
	}
	ets, _ := deps.Entities.ListByClass(ctx, entities.ClassTile, entities.ListOpts{Limit: 100})
	if len(ets) != 4 {
		t.Errorf("re-upload should be idempotent; got %d tile entities, want 4", len(ets))
	}
}

// TestAssetUpload_TilemapSkipsTransparentCells covers the user-
// approved policy: fully-transparent cells are dropped from the
// fan-out so the palette stays clean for sparse sheets.
func TestAssetUpload_TilemapSkipsTransparentCells(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)

	// 2x2 sheet; only top-left cell is opaque.
	w, h := 64, 64
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	body, ct := multipartUploadWithKind(t, buf.Bytes(), "sparse.png", assets.KindOverrideTilemap)
	req := authedReq(http.MethodPost, "/design/assets/upload", tok, body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}

	ets, _ := deps.Entities.ListByClass(ctx, entities.ClassTile, entities.ListOpts{Limit: 100})
	if len(ets) != 1 {
		t.Errorf("expected 1 tile entity (only top-left cell is non-empty); got %d", len(ets))
	}
	if len(ets) == 1 && ets[0].AtlasIndex != 0 {
		t.Errorf("expected atlas_index=0; got %d", ets[0].AtlasIndex)
	}
}
