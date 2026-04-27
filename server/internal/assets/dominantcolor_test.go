package assets

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// solidImage builds a 16×16 RGBA filled with one color, optionally with
// a transparent corner so the alpha skip path is exercised.
func solidImage(c color.RGBA, transparentCorner bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if transparentCorner && x < 4 && y < 4 {
				img.Set(x, y, color.RGBA{0, 0, 0, 0})
				continue
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func TestDominantFromImage_SolidRed(t *testing.T) {
	got, ok := dominantFromImage(solidImage(color.RGBA{200, 30, 30, 255}, false))
	if !ok {
		t.Fatal("expected ok = true")
	}
	// 200 → bucket 25, center 25*8+4 = 204
	// 30  → bucket 3,  center 3*8+4  = 28
	want := uint32(204)<<16 | uint32(28)<<8 | uint32(28)
	if got != want {
		t.Errorf("got %06X, want %06X", got, want)
	}
}

func TestDominantFromImage_TransparentSkipped(t *testing.T) {
	// Same red, but with a transparent corner. Result should match
	// the solid-red case.
	got, ok := dominantFromImage(solidImage(color.RGBA{200, 30, 30, 255}, true))
	if !ok {
		t.Fatal("expected ok = true")
	}
	want := uint32(204)<<16 | uint32(28)<<8 | uint32(28)
	if got != want {
		t.Errorf("got %06X, want %06X", got, want)
	}
}

func TestDominantFromImage_FullyTransparent(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	// Default zero-value RGBA = transparent black; nothing painted.
	if _, ok := dominantFromImage(img); ok {
		t.Errorf("expected ok = false for fully transparent image")
	}
}

func TestDominantFromImage_TwoColorMajority(t *testing.T) {
	// 12×16 green block + 4×16 blue block; green should win.
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	green := color.RGBA{60, 200, 60, 255}
	blue := color.RGBA{60, 60, 200, 255}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if x < 12 {
				img.Set(x, y, green)
			} else {
				img.Set(x, y, blue)
			}
		}
	}
	got, ok := dominantFromImage(img)
	if !ok {
		t.Fatal("expected ok = true")
	}
	// Green: 60→bucket 7, center 60; 200→bucket 25, center 204
	want := uint32(60)<<16 | uint32(204)<<8 | uint32(60)
	if got != want {
		t.Errorf("got %06X, want %06X", got, want)
	}
}

func TestComputeDominantColor_RoundTripPNG(t *testing.T) {
	// Encode a solid magenta image, decode back through the public
	// entry point (PNG bytes → uint32). Ensures the png.Decode +
	// alpha-shift logic works end-to-end.
	src := solidImage(color.RGBA{220, 60, 220, 255}, false)
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, ok := ComputeDominantColor(buf.Bytes())
	if !ok {
		t.Fatal("expected ok = true")
	}
	// 220 → bucket 27, center 220
	// 60  → bucket 7,  center 60
	want := uint32(220)<<16 | uint32(60)<<8 | uint32(220)
	if got != want {
		t.Errorf("got %06X, want %06X", got, want)
	}
}

func TestComputeDominantColor_BadBytes(t *testing.T) {
	if _, ok := ComputeDominantColor([]byte("not a PNG")); ok {
		t.Errorf("expected ok = false on bad PNG bytes")
	}
}

func TestFormatHex(t *testing.T) {
	cases := map[uint32]string{
		0x000000: "#000000",
		0xFF00AA: "#FF00AA",
		0x00FF00: "#00FF00",
	}
	for in, want := range cases {
		if got := FormatHex(in); got != want {
			t.Errorf("FormatHex(%06X) = %s, want %s", in, got, want)
		}
	}
}
