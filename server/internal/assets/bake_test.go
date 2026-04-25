package assets_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
)

// makeTwoColorPNG builds a 4x4 PNG with two distinct colors so a recipe
// has something meaningful to swap.
func makeTwoColorPNG(t *testing.T, a, b color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if (x+y)%2 == 0 {
				img.Set(x, y, a)
			} else {
				img.Set(x, y, b)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// seedPaletteVariant inserts a palette_variants row directly so the bake
// test doesn't need the full palette-variant CRUD surface (lands later).
func seedPaletteVariant(t *testing.T, pool *pgxpool.Pool, assetID int64, recipeJSON string) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO palette_variants (asset_id, name, source_to_dest_json)
		VALUES ($1, 'red-team', $2::jsonb) RETURNING id
	`, assetID, recipeJSON).Scan(&id)
	if err != nil {
		t.Fatalf("seed palette variant: %v", err)
	}
	return id
}

func TestBake_RemapsColorsAndIdempotent(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)
	job := assets.NewBakeJob(pool, store, svc)
	ctx := context.Background()

	// Source: 2-color PNG (red + blue), 4x4.
	red := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	blue := color.NRGBA{R: 0, G: 0, B: 255, A: 255}
	body := makeTwoColorPNG(t, red, blue)

	// Upload it as a sprite.
	uploaded, err := svc.Upload(ctx, makeUploadRequest(t, "team.png", body, "image/png"), store, designerID, "")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	// Seed a recipe: swap red -> green. Use decimal strings (per the
	// recipe schema docs in bake.go).
	red32 := uint32(255)<<24 | uint32(0)<<16 | uint32(0)<<8 | 255
	green32 := uint32(0)<<24 | uint32(255)<<16 | uint32(0)<<8 | 255
	recipeJSON := `{"` + uintStr(red32) + `": ` + uintStr(green32) + `}`
	_ = seedPaletteVariant(t, pool, uploaded.Asset.ID, recipeJSON)

	results, err := job.BakeForAsset(ctx, uploaded.Asset.ID)
	if err != nil {
		t.Fatalf("BakeForAsset: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Reused {
		t.Errorf("first bake should not be a reuse")
	}
	if results[0].BakedContentPath == "" {
		t.Errorf("baked content path missing")
	}

	// Re-bake: should be a reuse, content path identical.
	results2, err := job.BakeForAsset(ctx, uploaded.Asset.ID)
	if err != nil {
		t.Fatalf("BakeForAsset 2: %v", err)
	}
	if !results2[0].Reused {
		t.Errorf("second bake should be Reused=true")
	}
	if results2[0].BakedContentPath != results[0].BakedContentPath {
		t.Errorf("content path drift: %q vs %q",
			results[0].BakedContentPath, results2[0].BakedContentPath)
	}

	// Verify the asset_variants row is in 'baked' status.
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM asset_variants WHERE id = $1`, results[0].AssetVariantID,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "baked" {
		t.Errorf("status: got %q, want baked", status)
	}

	// Pull the baked PNG and verify the swap actually happened.
	rc, err := store.Get(ctx, results[0].BakedContentPath)
	if err != nil {
		t.Fatalf("Get baked: %v", err)
	}
	defer rc.Close()
	bakedImg, err := png.Decode(rc)
	if err != nil {
		t.Fatalf("decode baked: %v", err)
	}
	// Pixel (0,0) was red -> should now be green.
	got := color.NRGBAModel.Convert(bakedImg.At(0, 0)).(color.NRGBA)
	if got.R != 0 || got.G != 255 || got.B != 0 {
		t.Errorf("pixel (0,0): got %+v, want green", got)
	}
	// Pixel (1,0) was blue -> should still be blue.
	gotBlue := color.NRGBAModel.Convert(bakedImg.At(1, 0)).(color.NRGBA)
	if gotBlue.B != 255 || gotBlue.R != 0 || gotBlue.G != 0 {
		t.Errorf("pixel (1,0): got %+v, want blue (untouched)", gotBlue)
	}
}

func TestBake_MultipleRecipesRunInParallel(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)
	job := assets.NewBakeJob(pool, store, svc)
	ctx := context.Background()

	red := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	blue := color.NRGBA{R: 0, G: 0, B: 255, A: 255}
	asset, _ := svc.Upload(ctx,
		makeUploadRequest(t, "team.png", makeTwoColorPNG(t, red, blue), "image/png"),
		store, designerID, "")

	red32 := uint32(255)<<24 | uint32(0)<<16 | uint32(0)<<8 | 255
	for i, dest := range []uint32{
		uint32(0)<<24 | uint32(255)<<16 | uint32(0)<<8 | 255,   // green
		uint32(255)<<24 | uint32(255)<<16 | uint32(0)<<8 | 255, // yellow
		uint32(0)<<24 | uint32(0)<<16 | uint32(0)<<8 | 255,     // black
	} {
		recipe := `{"` + uintStr(red32) + `": ` + uintStr(dest) + `}`
		_, err := pool.Exec(ctx,
			`INSERT INTO palette_variants (asset_id, name, source_to_dest_json) VALUES ($1, $2, $3::jsonb)`,
			asset.Asset.ID, "team-"+string(rune('a'+i)), recipe)
		if err != nil {
			t.Fatalf("seed recipe %d: %v", i, err)
		}
	}

	results, err := job.BakeForAsset(ctx, asset.Asset.ID)
	if err != nil {
		t.Fatalf("BakeForAsset: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// Each variant should have produced a distinct content path.
	seen := map[string]bool{}
	for _, r := range results {
		if seen[r.BakedContentPath] {
			t.Errorf("duplicate baked path %q across variants", r.BakedContentPath)
		}
		seen[r.BakedContentPath] = true
	}
}

func TestBake_NoRecipesReturnsEmpty(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)
	job := assets.NewBakeJob(pool, store, svc)
	ctx := context.Background()

	asset, _ := svc.Upload(ctx, makeUploadRequest(t, "x.png", pngOf(t, 4, 4), "image/png"), store, designerID, "")

	res, err := job.BakeForAsset(ctx, asset.Asset.ID)
	if err != nil {
		t.Fatalf("BakeForAsset: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected 0 results, got %d", len(res))
	}
}

func TestBake_AudioAssetRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)
	job := assets.NewBakeJob(pool, store, svc)
	ctx := context.Background()

	wav := makeTestWAV(t, 8000, 1, 8, 100)
	asset, _ := svc.Upload(ctx, makeUploadRequest(t, "ping.wav", wav, "audio/wav"), store, designerID, "")

	if _, err := job.BakeForAsset(ctx, asset.Asset.ID); err == nil {
		t.Error("expected error baking an audio asset")
	}
}

func uintStr(v uint32) string {
	if v == 0 {
		return "0"
	}
	out := ""
	for v > 0 {
		d := byte(v % 10)
		out = string(rune('0'+d)) + out
		v /= 10
	}
	return out
}
