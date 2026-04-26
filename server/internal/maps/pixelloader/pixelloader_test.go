package pixelloader_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"boxland/server/internal/maps/wfc"
)

// TestComputeFingerprint_RealPNG exercises the encode/decode/sample path
// against a 32x32 PNG so the integration with image/png is covered without
// needing a database. (The full Loader needs an asset row, so we test
// ComputeFingerprint directly here.)
func TestComputeFingerprint_RealPNG(t *testing.T) {
	const sz = 32
	img := image.NewRGBA(image.Rect(0, 0, sz, sz))
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			// Red diagonal: top-left → bottom-right.
			img.Set(x, y, color.RGBA{uint8(x * 8), 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	fp, err := wfc.ComputeFingerprint(decoded, image.Rect(0, 0, sz, sz))
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	// West edge sample 0 should be near (0,0,0); east edge near (255,0,0).
	if fp[wfc.EdgeW][0][0] > 50 {
		t.Errorf("west sample 0 too red: %+v", fp[wfc.EdgeW][0])
	}
	if fp[wfc.EdgeE][0][0] < 100 {
		t.Errorf("east sample 0 too dark: %+v", fp[wfc.EdgeE][0])
	}
}
