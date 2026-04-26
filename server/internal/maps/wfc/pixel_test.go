package wfc_test

import (
	"image"
	"image/color"
	"testing"

	"boxland/server/internal/maps/wfc"
)

// solid builds a single fingerprint where every sample == (r,g,b).
func solid(r, g, b uint8) wfc.EdgeFingerprint {
	var fp wfc.EdgeFingerprint
	for i := 0; i < wfc.EdgeSamples; i++ {
		fp[i] = [3]uint8{r, g, b}
	}
	return fp
}

func solidTile(et wfc.EntityTypeID, r, g, b uint8, weight float64) wfc.PixelTile {
	fp := solid(r, g, b)
	return wfc.PixelTile{
		EntityType:  et,
		Fingerprint: [4]wfc.EdgeFingerprint{fp, fp, fp, fp},
		Weight:      weight,
	}
}

func TestGeneratePixel_FillsAllCells(t *testing.T) {
	tiles := []wfc.PixelTile{
		solidTile(1, 30, 100, 30, 1),
		solidTile(2, 90, 60, 20, 1),
		solidTile(3, 60, 60, 60, 1),
	}
	ts := wfc.NewPixelTileSet(tiles, wfc.PixelTileSetOptions{})
	res, err := wfc.GeneratePixel(ts, wfc.GenerateOptions{Width: 6, Height: 4, Seed: 42})
	if err != nil {
		t.Fatalf("GeneratePixel: %v", err)
	}
	if len(res.Region.Cells) != 24 {
		t.Fatalf("expected 24 cells, got %d", len(res.Region.Cells))
	}
}

func TestGeneratePixel_DeterministicForSameSeed(t *testing.T) {
	tiles := []wfc.PixelTile{
		solidTile(1, 30, 100, 30, 1),
		solidTile(2, 90, 60, 20, 1),
		solidTile(3, 60, 60, 60, 1),
	}
	ts := wfc.NewPixelTileSet(tiles, wfc.PixelTileSetOptions{})
	a, err := wfc.GeneratePixel(ts, wfc.GenerateOptions{Width: 8, Height: 8, Seed: 99})
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := wfc.GeneratePixel(ts, wfc.GenerateOptions{Width: 8, Height: 8, Seed: 99})
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if len(a.Region.Cells) != len(b.Region.Cells) {
		t.Fatalf("cell count differs")
	}
	for i := range a.Region.Cells {
		if a.Region.Cells[i] != b.Region.Cells[i] {
			t.Fatalf("cell %d differs: %+v vs %+v", i, a.Region.Cells[i], b.Region.Cells[i])
		}
	}
}

func TestGeneratePixel_RespectsAnchors(t *testing.T) {
	tiles := []wfc.PixelTile{
		solidTile(1, 30, 100, 30, 1),
		solidTile(2, 90, 60, 20, 1),
		solidTile(3, 60, 60, 60, 1),
	}
	ts := wfc.NewPixelTileSet(tiles, wfc.PixelTileSetOptions{})
	res, err := wfc.GeneratePixel(ts, wfc.GenerateOptions{
		Width: 4, Height: 4, Seed: 7,
		Anchors: wfc.Anchors{Cells: []wfc.Cell{
			{X: 0, Y: 0, EntityType: 2},
			{X: 3, Y: 3, EntityType: 1},
		}},
	})
	if err != nil {
		t.Fatalf("GeneratePixel: %v", err)
	}
	if got := res.Region.Cells[0]; got.EntityType != 2 {
		t.Errorf("anchor at (0,0) not preserved: got entity %d", got.EntityType)
	}
	if got := res.Region.Cells[len(res.Region.Cells)-1]; got.EntityType != 1 {
		t.Errorf("anchor at (3,3) not preserved: got entity %d", got.EntityType)
	}
}

func TestGeneratePixel_NeverErrorsOnContradiction(t *testing.T) {
	// Two tiles whose edges look maximally different. The pixel engine
	// must still produce a region (no reseed budget); fallback path
	// fires for cells the propagation prunes empty.
	tiles := []wfc.PixelTile{
		solidTile(1, 0, 0, 0, 1),
		solidTile(2, 255, 255, 255, 1),
	}
	ts := wfc.NewPixelTileSet(tiles, wfc.PixelTileSetOptions{KeepBestK: 1})
	// KeepBestK=1 forces strict matching; black tiles can only neighbour
	// black tiles, white only white. With diagonal anchors of different
	// colours, propagation will collide somewhere.
	res, err := wfc.GeneratePixel(ts, wfc.GenerateOptions{
		Width: 3, Height: 3, Seed: 1,
		Anchors: wfc.Anchors{Cells: []wfc.Cell{
			{X: 0, Y: 0, EntityType: 1},
			{X: 2, Y: 2, EntityType: 2},
		}},
	})
	if err != nil {
		t.Fatalf("pixel engine errored on contradiction: %v", err)
	}
	if len(res.Region.Cells) != 9 {
		t.Fatalf("region should still have 9 cells, got %d", len(res.Region.Cells))
	}
}

func TestNewPixelTileSet_EmptyOK(t *testing.T) {
	// Edge case: zero-tile palette. NewPixelTileSet must not panic;
	// GeneratePixel returns ErrEmptyPixelTileSet.
	ts := wfc.NewPixelTileSet(nil, wfc.PixelTileSetOptions{})
	_, err := wfc.GeneratePixel(ts, wfc.GenerateOptions{Width: 2, Height: 2, Seed: 1})
	if err == nil {
		t.Fatal("expected error from empty pixel tileset")
	}
}

func TestComputeFingerprint_AveragesEdges(t *testing.T) {
	// 4×4 image: top row red, bottom row blue, left col green, right col
	// uniform red. Fingerprint sampling should reflect those colours.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		img.Set(x, 0, color.RGBA{255, 0, 0, 255}) // top: red
		img.Set(x, 3, color.RGBA{0, 0, 255, 255}) // bottom: blue
	}
	for y := 1; y < 3; y++ {
		img.Set(0, y, color.RGBA{0, 255, 0, 255})    // left: green
		img.Set(3, y, color.RGBA{255, 0, 0, 255})    // right: red (matches top)
		img.Set(1, y, color.RGBA{128, 128, 128, 255})
		img.Set(2, y, color.RGBA{128, 128, 128, 255})
	}
	fp, err := wfc.ComputeFingerprint(img, image.Rect(0, 0, 4, 4))
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	// North edge averages should be very red.
	for s := 0; s < wfc.EdgeSamples; s++ {
		if fp[wfc.EdgeN][s][0] < 200 || fp[wfc.EdgeN][s][2] > 50 {
			t.Errorf("north sample %d not red-leaning: %+v", s, fp[wfc.EdgeN][s])
			break
		}
	}
	// South edge averages should be very blue.
	for s := 0; s < wfc.EdgeSamples; s++ {
		if fp[wfc.EdgeS][s][2] < 200 || fp[wfc.EdgeS][s][0] > 50 {
			t.Errorf("south sample %d not blue-leaning: %+v", s, fp[wfc.EdgeS][s])
			break
		}
	}
}

func TestComputeFingerprint_TransparentEdgeFallsBack(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	// Leave fully transparent. Should return all-zero fingerprints with
	// no panic.
	fp, err := wfc.ComputeFingerprint(img, image.Rect(0, 0, 4, 4))
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	if fp[wfc.EdgeN][0] != [3]uint8{} {
		t.Errorf("transparent edge should be zero, got %+v", fp[wfc.EdgeN][0])
	}
}

func TestFingerprintCache_LRUEviction(t *testing.T) {
	c := wfc.NewFingerprintCache(2)
	a := wfc.FingerprintKey{EntityTypeID: 1}
	b := wfc.FingerprintKey{EntityTypeID: 2}
	d := wfc.FingerprintKey{EntityTypeID: 3}
	c.Put(a, [4]wfc.EdgeFingerprint{solid(1, 0, 0)})
	c.Put(b, [4]wfc.EdgeFingerprint{solid(2, 0, 0)})
	if _, ok := c.Get(a); !ok {
		t.Fatal("expected a in cache")
	}
	c.Put(d, [4]wfc.EdgeFingerprint{solid(3, 0, 0)})
	// b was the LRU before the d insert; it should be evicted (a was
	// just touched by Get).
	if _, ok := c.Get(b); ok {
		t.Errorf("expected b evicted; cache len=%d", c.Len())
	}
	if _, ok := c.Get(a); !ok {
		t.Error("expected a still in cache")
	}
	if _, ok := c.Get(d); !ok {
		t.Error("expected d in cache")
	}
}

func TestFingerprintCache_VersionBumpInvalidates(t *testing.T) {
	c := wfc.NewFingerprintCache(8)
	old := wfc.FingerprintKey{EntityTypeID: 1, AssetVersion: 100}
	new_ := wfc.FingerprintKey{EntityTypeID: 1, AssetVersion: 200}
	c.Put(old, [4]wfc.EdgeFingerprint{solid(10, 10, 10)})
	if _, ok := c.Get(new_); ok {
		t.Fatal("new version should miss")
	}
	if _, ok := c.Get(old); !ok {
		t.Fatal("old version still cached")
	}
}

func TestCompositeFingerprint_AveragesParts(t *testing.T) {
	a := solid(0, 0, 0)
	b := solid(200, 200, 200)
	out := wfc.CompositeFingerprint([]wfc.EdgeFingerprint{a, b})
	for s := 0; s < wfc.EdgeSamples; s++ {
		if out[s][0] != 100 || out[s][1] != 100 || out[s][2] != 100 {
			t.Errorf("sample %d: got %+v want ~100,100,100", s, out[s])
		}
	}
}

func TestCompositeFingerprint_EmptyReturnsZero(t *testing.T) {
	out := wfc.CompositeFingerprint(nil)
	for s := 0; s < wfc.EdgeSamples; s++ {
		if out[s] != [3]uint8{} {
			t.Errorf("empty input should be zero, sample %d = %+v", s, out[s])
		}
	}
}
