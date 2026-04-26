package assets_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"boxland/server/internal/assets"
)

// makePNG renders a w×h fully-opaque PNG (color doesn't matter; the
// importer reads dimensions only).
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestSynthesizeWalkAnimations_4Rows(t *testing.T) {
	got := assets.SynthesizeWalkAnimations(4, 4) // 4 cols × 4 rows
	if len(got) != 5 {
		t.Fatalf("got %d, want 5 (N/E/S/W + idle)", len(got))
	}
	want := map[string][2]int{
		assets.AnimWalkN: {0, 3},
		assets.AnimWalkE: {4, 7},
		assets.AnimWalkS: {8, 11},
		assets.AnimWalkW: {12, 15},
		assets.AnimIdle:  {8, 8}, // first frame of south row
	}
	for _, a := range got {
		w, ok := want[a.Name]
		if !ok {
			t.Errorf("unexpected animation %q", a.Name)
			continue
		}
		if a.FrameFrom != w[0] || a.FrameTo != w[1] {
			t.Errorf("%s: got [%d,%d], want [%d,%d]", a.Name, a.FrameFrom, a.FrameTo, w[0], w[1])
		}
	}
}

func TestSynthesizeWalkAnimations_SingleRowStrip(t *testing.T) {
	got := assets.SynthesizeWalkAnimations(6, 1)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 (walk + idle)", len(got))
	}
	if got[0].Name != assets.AnimWalk || got[0].FrameTo != 5 {
		t.Errorf("walk wrong: %+v", got[0])
	}
}

func TestSynthesizeWalkAnimations_OddShape(t *testing.T) {
	if got := assets.SynthesizeWalkAnimations(3, 2); len(got) != 0 {
		t.Errorf("3×2 should not synthesize anything; got %d", len(got))
	}
}

func TestDefaultSpriteImport_AutoSlicesPlainPNG(t *testing.T) {
	body := makePNG(t, 128, 128) // 4×4 grid at 32px
	res, err := assets.DefaultSpriteImport(context.Background(), nil, "hero.png", body, nil, assets.AutoSliceConfig{})
	if err != nil {
		t.Fatalf("auto-import: %v", err)
	}
	if res.SheetMetadata.Cols != 4 || res.SheetMetadata.Rows != 4 {
		t.Errorf("grid: got %dx%d, want 4x4", res.SheetMetadata.Cols, res.SheetMetadata.Rows)
	}
	// Walk + idle synthesized.
	names := make(map[string]bool, len(res.Animations))
	for _, a := range res.Animations {
		names[a.Name] = true
	}
	for _, want := range []string{
		assets.AnimWalkN, assets.AnimWalkE, assets.AnimWalkS, assets.AnimWalkW, assets.AnimIdle,
	} {
		if !names[want] {
			t.Errorf("missing synthesized animation %q (got %v)", want, names)
		}
	}
}

func TestDefaultSpriteImport_NonDivisibleErrors(t *testing.T) {
	body := makePNG(t, 100, 32) // 100 not divisible by 32
	_, err := assets.DefaultSpriteImport(context.Background(), nil, "x.png", body, nil, assets.AutoSliceConfig{})
	if err == nil {
		t.Errorf("expected ErrParseFailed for non-divisible image")
	}
}

func TestDefaultSpriteImport_EnsuresIdleEvenWhenSynthesizerProducedNone(t *testing.T) {
	// 32x96 = 1 col × 3 rows. SynthesizeWalkAnimations returns nil for
	// "1 col, 3 rows" (no recognized layout) — but the helper must
	// still tack on an `idle` so the renderer has something to draw.
	body := makePNG(t, 32, 96)
	res, err := assets.DefaultSpriteImport(context.Background(), nil, "blob.png", body, nil, assets.AutoSliceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Animations) != 1 || res.Animations[0].Name != assets.AnimIdle {
		t.Errorf("expected exactly one synthesized idle; got %+v", res.Animations)
	}
}

func TestDefaultSpriteImport_SidecarPathRunsAseprite(t *testing.T) {
	body := makePNG(t, 64, 64)
	sidecar := []byte(`{
		"frames": [
			{"filename": "f0", "frame": {"x": 0, "y": 0, "w": 32, "h": 32}, "duration": 100},
			{"filename": "f1", "frame": {"x": 32, "y": 0, "w": 32, "h": 32}, "duration": 100}
		],
		"meta": {"frameTags": [{"name": "walk_east", "from": 0, "to": 1, "direction": "forward"}]}
	}`)
	reg := assets.DefaultRegistry()
	res, err := assets.DefaultSpriteImport(context.Background(), reg, "hero.json", body, sidecar, assets.AutoSliceConfig{})
	if err != nil {
		t.Fatalf("sidecar import: %v", err)
	}
	found := false
	for _, a := range res.Animations {
		if a.Name == "walk_east" {
			found = true
		}
	}
	if !found {
		t.Errorf("Aseprite sidecar should have produced walk_east; got %+v", res.Animations)
	}
}
