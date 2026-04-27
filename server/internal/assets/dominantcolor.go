package assets

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
)

// ComputeDominantColor returns a packed 0xRRGGBB value representing the
// most-common non-transparent color in the image, quantized to 32-step
// buckets per channel.
//
// Designed for 32×32 pixel-art tiles + small sprite sheets:
//   - PNG-only (the only kind=sprite/tile/ui_panel format we accept).
//   - Walks pixels at stride 2 in both axes for an 8× speedup. Pixel
//     art has large flat regions, so this is lossless in practice.
//   - Buckets non-transparent (alpha >= 128) pixels into a 32×32×32
//     RGB cube; returns the cube center of the heaviest bucket.
//   - O(width*height) time, O(1) extra memory for the cube (32³ = 32 KiB
//     of int32 counters on the stack).
//
// Returns (0, false) for fully transparent images, decode failures,
// or zero-pixel inputs. Callers persist 0 and try again later if a
// future version of the asset has actual pixels.
func ComputeDominantColor(body []byte) (uint32, bool) {
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		return 0, false
	}
	return dominantFromImage(img)
}

// dominantFromImage is split from ComputeDominantColor so tests can
// exercise the bucketing math against synthetic image.RGBAs without
// re-encoding to PNG bytes every time.
func dominantFromImage(img image.Image) (uint32, bool) {
	bounds := img.Bounds()
	if bounds.Empty() {
		return 0, false
	}

	// 32 steps per channel = 5 bits = 32^3 = 32 768 buckets.
	const steps = 32
	const shift = 3 // 256 / 32 = 8 = 1<<3
	var counts [steps * steps * steps]int32

	bestIdx := -1
	var bestCount int32

	for y := bounds.Min.Y; y < bounds.Max.Y; y += 2 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 2 {
			r, g, b, a := img.At(x, y).RGBA()
			// RGBA() returns 16-bit values (0..65535). Skip
			// transparent pixels — they don't represent the
			// asset's color identity.
			if a>>8 < 128 {
				continue
			}
			ri := int(r>>8) >> shift
			gi := int(g>>8) >> shift
			bi := int(b>>8) >> shift
			idx := ri*steps*steps + gi*steps + bi
			counts[idx]++
			if counts[idx] > bestCount {
				bestCount = counts[idx]
				bestIdx = idx
			}
		}
	}
	if bestIdx < 0 {
		return 0, false
	}

	ri := bestIdx / (steps * steps)
	gi := (bestIdx / steps) % steps
	bi := bestIdx % steps
	// Use the cube CENTER (bucket index * 8 + 4) so values are
	// closer to the real average rather than the bucket's lower
	// bound. Caps at 0xFF; the centers for index 31 land at 252.
	r := uint32(ri<<shift) | 0x04
	g := uint32(gi<<shift) | 0x04
	b := uint32(bi<<shift) | 0x04
	return (r << 16) | (g << 8) | b, true
}

// FormatHex returns a 7-byte CSS-friendly hex string ("#RRGGBB") for a
// packed dominant_color value. Primarily used by the asset card swatch.
// Returns "#000000" for the sentinel zero.
func FormatHex(packed uint32) string {
	return fmt.Sprintf("#%06X", packed&0xFFFFFF)
}
