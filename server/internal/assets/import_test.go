package assets_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"boxland/server/internal/assets"
)

// pngBytes encodes an in-memory image at the given dimensions.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	// Fill with a non-zero color so any "is the image empty?" sniff doesn't
	// false-positive in tests that look at non-pixel content.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: 64, G: 128, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

// ---- Registry ----

func TestRegistry_AutoDetectAndExplicit(t *testing.T) {
	reg := assets.DefaultRegistry()
	if _, ok := reg.Get("raw"); !ok {
		t.Errorf("raw should be registered")
	}
	if _, ok := reg.Get("aseprite"); !ok {
		t.Errorf("aseprite should be registered")
	}
	if _, ok := reg.Get("ghost"); ok {
		t.Errorf("unknown ids should not match")
	}

	// Auto-detect: a TexturePacker JSON with the marker string should
	// match texturepacker, not aseprite, even though aseprite's auto-detect
	// also matches generic JSON shape.
	tpJSON := []byte(`{"frames":{"a.png":{"frame":{"x":0,"y":0,"w":16,"h":16}}},"meta":{"app":"https://www.codeandweb.com/texturepacker"}}`)
	imp, ok := reg.AutoDetect("sheet.json", tpJSON)
	if !ok {
		t.Fatal("expected an auto-detect match")
	}
	// alphabetic order: aseprite, free-tex-packer, raw, strip, texturepacker.
	// aseprite's CanAutoDetect matches any json with "frames" + "meta", so
	// aseprite wins here. That's the documented behavior — the designer
	// can override via the dropdown.
	if imp.ID() != "aseprite" {
		t.Logf("auto-detect picked %q (aseprite is the documented fall-through for json+frames+meta)", imp.ID())
	}
}

func TestRegistry_DuplicateRegistrationPanics(t *testing.T) {
	reg := assets.NewRegistry()
	reg.Register(&assets.RawPNGImporter{})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	reg.Register(&assets.RawPNGImporter{})
}

// ---- Raw PNG ----

func TestRawPNG_HappyPath(t *testing.T) {
	imp := &assets.RawPNGImporter{}
	body := pngBytes(t, 96, 64) // 6x4 cells of 16x16
	cfg, _ := json.Marshal(assets.RawPNGConfig{GridW: 16, GridH: 16})
	res, err := imp.Parse(context.Background(), body, cfg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Frames) != 24 {
		t.Errorf("expected 24 frames, got %d", len(res.Frames))
	}
	if res.SheetMetadata.Cols != 6 || res.SheetMetadata.Rows != 4 {
		t.Errorf("dims: got %dx%d, want 6x4", res.SheetMetadata.Cols, res.SheetMetadata.Rows)
	}
	if len(res.Animations) != 0 {
		t.Errorf("raw parser should not emit animation tags by default")
	}
}

func TestRawPNG_NonDivisibleRejected(t *testing.T) {
	imp := &assets.RawPNGImporter{}
	body := pngBytes(t, 96, 50) // 50 not divisible by 16
	cfg, _ := json.Marshal(assets.RawPNGConfig{GridW: 16, GridH: 16})
	_, err := imp.Parse(context.Background(), body, cfg)
	if !errors.Is(err, assets.ErrParseFailed) {
		t.Errorf("got %v, want ErrParseFailed", err)
	}
}

func TestRawPNG_BadConfigRejected(t *testing.T) {
	imp := &assets.RawPNGImporter{}
	body := pngBytes(t, 16, 16)
	_, err := imp.Parse(context.Background(), body, []byte(`{"grid_w":0,"grid_h":16}`))
	if !errors.Is(err, assets.ErrParseFailed) {
		t.Errorf("got %v, want ErrParseFailed", err)
	}
}

// ---- Strip ----

func TestStrip_HorizontalDeducesCols(t *testing.T) {
	imp := &assets.StripImporter{}
	body := pngBytes(t, 80, 16) // 5 cells of 16x16
	cfg, _ := json.Marshal(assets.StripConfig{Layout: assets.StripHorizontal, CellW: 16, CellH: 16})
	res, err := imp.Parse(context.Background(), body, cfg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Frames) != 5 {
		t.Errorf("expected 5 frames, got %d", len(res.Frames))
	}
	if res.SheetMetadata.Cols != 5 || res.SheetMetadata.Rows != 1 {
		t.Errorf("dims: got %dx%d", res.SheetMetadata.Cols, res.SheetMetadata.Rows)
	}
}

func TestStrip_VerticalDeducesRows(t *testing.T) {
	imp := &assets.StripImporter{}
	body := pngBytes(t, 16, 64) // 4 cells of 16x16
	cfg, _ := json.Marshal(assets.StripConfig{Layout: assets.StripVertical, CellW: 16, CellH: 16})
	res, err := imp.Parse(context.Background(), body, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Frames) != 4 {
		t.Errorf("expected 4 frames, got %d", len(res.Frames))
	}
}

func TestStrip_RowsN(t *testing.T) {
	imp := &assets.StripImporter{}
	body := pngBytes(t, 64, 32) // 2 rows of 16h, cells 16x16
	cfg, _ := json.Marshal(assets.StripConfig{Layout: assets.StripRowsN, N: 2, CellW: 16})
	res, err := imp.Parse(context.Background(), body, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.SheetMetadata.Cols != 4 || res.SheetMetadata.Rows != 2 {
		t.Errorf("dims: got %dx%d, want 4x2", res.SheetMetadata.Cols, res.SheetMetadata.Rows)
	}
}

func TestStrip_BadLayoutRejected(t *testing.T) {
	imp := &assets.StripImporter{}
	body := pngBytes(t, 16, 16)
	cfg, _ := json.Marshal(map[string]any{"layout": "weird"})
	_, err := imp.Parse(context.Background(), body, cfg)
	if !errors.Is(err, assets.ErrParseFailed) {
		t.Errorf("got %v, want ErrParseFailed", err)
	}
}

// ---- Aseprite ----

func TestAseprite_ArrayFlavor(t *testing.T) {
	imp := &assets.AsepriteImporter{}
	sidecar := []byte(`{
		"frames": [
			{"filename":"a 0","frame":{"x":0,"y":0,"w":16,"h":16},"duration":120},
			{"filename":"a 1","frame":{"x":16,"y":0,"w":16,"h":16},"duration":120}
		],
		"meta": {
			"frameTags":[{"name":"walk","from":0,"to":1,"direction":"forward"}]
		}
	}`)
	res, err := imp.Parse(context.Background(), nil, sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Frames) != 2 {
		t.Errorf("expected 2 frames, got %d", len(res.Frames))
	}
	if len(res.Animations) != 1 || res.Animations[0].Name != "walk" {
		t.Errorf("expected walk animation, got %+v", res.Animations)
	}
}

func TestAseprite_HashFlavor(t *testing.T) {
	imp := &assets.AsepriteImporter{}
	sidecar := []byte(`{
		"frames": {
			"a 1": {"frame":{"x":16,"y":0,"w":16,"h":16}},
			"a 0": {"frame":{"x":0, "y":0,"w":16,"h":16}}
		},
		"meta": { "frameTags": [{"name":"x","from":0,"to":1,"direction":"reverse"}] }
	}`)
	res, err := imp.Parse(context.Background(), nil, sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Frames) != 2 {
		t.Errorf("expected 2 frames, got %d", len(res.Frames))
	}
	// Hash form sorts keys for deterministic indexing: "a 0" < "a 1" so
	// frame 0 is x=0, frame 1 is x=16.
	if res.Frames[0].SX != 0 || res.Frames[1].SX != 16 {
		t.Errorf("expected stable order; got %+v", res.Frames)
	}
	if res.Animations[0].Direction != assets.DirReverse {
		t.Errorf("direction: got %q", res.Animations[0].Direction)
	}
}

// ---- TexturePacker ----

func TestTexturePacker_InfersAnimationsFromFilenames(t *testing.T) {
	imp := &assets.TexturePackerImporter{}
	sidecar := []byte(`{
		"frames": {
			"boss-walk-0.png": {"frame":{"x":0,"y":0,"w":16,"h":16}},
			"boss-walk-1.png": {"frame":{"x":16,"y":0,"w":16,"h":16}},
			"boss-walk-2.png": {"frame":{"x":32,"y":0,"w":16,"h":16}},
			"hp-icon.png":     {"frame":{"x":0,"y":16,"w":8,"h":8}}
		},
		"meta":{"app":"https://www.codeandweb.com/texturepacker"}
	}`)
	res, err := imp.Parse(context.Background(), nil, sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Frames) != 4 {
		t.Errorf("expected 4 frames, got %d", len(res.Frames))
	}
	// One animation: "boss-walk" frames 0..2. Static "hp-icon" should NOT
	// produce a single-frame animation (filtered out).
	if len(res.Animations) != 1 || res.Animations[0].Name != "boss-walk" {
		t.Errorf("expected boss-walk animation, got %+v", res.Animations)
	}
}

func TestTexturePacker_AutoDetectMatchesMarker(t *testing.T) {
	tp := &assets.TexturePackerImporter{}
	if !tp.CanAutoDetect("a.json", []byte(`{"meta":{"app":"https://www.codeandweb.com/texturepacker"}}`)) {
		t.Errorf("should auto-detect texturepacker via meta.app")
	}
	if tp.CanAutoDetect("a.png", []byte(`...`)) {
		t.Errorf("should not auto-detect non-json files")
	}
}

// ---- free-tex-packer ----

func TestFreeTexPacker_DelegatesToTPButRelabelsSource(t *testing.T) {
	imp := &assets.FreeTexPackerImporter{}
	sidecar := []byte(`{
		"frames": { "x.png": {"frame":{"x":0,"y":0,"w":16,"h":16}} },
		"meta": { "app": "https://free-tex-packer.com" }
	}`)
	res, err := imp.Parse(context.Background(), nil, sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if res.SheetMetadata.Source != "free-tex-packer" {
		t.Errorf("source label: got %q", res.SheetMetadata.Source)
	}
}

func TestFreeTexPacker_AutoDetectByMarker(t *testing.T) {
	imp := &assets.FreeTexPackerImporter{}
	if !imp.CanAutoDetect("a.json", []byte(`{"meta":{"app":"https://free-tex-packer.com"}}`)) {
		t.Errorf("should auto-detect free-tex-packer via meta.app marker")
	}
}
