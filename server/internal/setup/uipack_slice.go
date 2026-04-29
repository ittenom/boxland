package setup

import (
	"strings"

	"boxland/server/internal/entities/components"
)

// MeasureNineSlice picks 9-slice insets for one UI sprite. Filename
// is the lookup key; we recognize the families in the Crusenho
// Gradient pack and return per-family defaults that match the art.
//
// The pack's corner sizes are consistent within a family but vary
// across families:
//
//   * Frames (Standard / Lite / Inward / Outward / Horizontal /
//     Vertical) are 24×24 base tiles with 8 px corners.
//   * Buttons (Small / Medium / Large × Lock / Press / Release × 4
//     animation frames) have 6 px corners on Small/Medium, 8 px on
//     Large.
//   * Sliders, scroll bars, dropdowns, and fill bars use 4 px
//     corners; their thin shape would distort with anything bigger.
//   * Single-cell sprites (icons: Arrow, Checkmark, Cross,
//     Banner, slot indicators) aren't intended to 9-slice but we
//     still need *some* config so the renderer can draw them at
//     their native size — we return 1 px insets to keep the
//     math valid; callers should treat sprites with width <=
//     2*inset as "draw at native size, don't 9-slice."
//
// Width/Height are the source PNG's pixel dimensions; the function
// guards against insets that would degenerate the center cell
// (left+right >= width or top+bottom >= height) by clamping to
// max(1, dim/4). The clamp is the safety net; the per-family
// defaults are the design intent.
//
// Returning a NineSlice (not pointer + error) keeps the call site
// terse — every UI sprite produces *some* config, even if it's the
// fallback. The seeder logs nothing here.
func MeasureNineSlice(filename string, width, height int) components.NineSlice {
	base := defaultsForFamily(filename)
	return clampSlice(base, width, height)
}

// defaultsForFamily returns the per-family inset defaults. Filename
// matching is case-insensitive; substring-based since the pack uses
// underscored stems like `UI_Gradient_Button_Large_Release_01a1`.
func defaultsForFamily(filename string) components.NineSlice {
	lower := strings.ToLower(filename)

	// Frames — 24×24 base tiles, 8 px corners. These are the
	// canonical 9-slice frames in the pack; everything else is
	// derivative.
	if strings.Contains(lower, "frame_") {
		return components.NineSlice{Left: 8, Top: 8, Right: 8, Bottom: 8}
	}

	// Buttons. Large variants are wider/taller, so they tolerate
	// chunkier corners; Small/Medium need thinner ones to avoid the
	// edge gradients colliding.
	if strings.Contains(lower, "button_large") {
		return components.NineSlice{Left: 8, Top: 8, Right: 8, Bottom: 8}
	}
	if strings.Contains(lower, "button_medium") || strings.Contains(lower, "button_small") {
		return components.NineSlice{Left: 6, Top: 6, Right: 6, Bottom: 6}
	}

	// Slider / scroll / dropdown / fill-bar — thin sprites with
	// small corners.
	if strings.Contains(lower, "slider_") ||
		strings.Contains(lower, "scroll_") ||
		strings.Contains(lower, "dropdown_") ||
		strings.Contains(lower, "fill_") {
		return components.NineSlice{Left: 4, Top: 4, Right: 4, Bottom: 4}
	}

	// Slots (inventory cells) — 16×16 with 4 px corners. The art
	// has rounded corners that need to stay intact when stretched.
	if strings.Contains(lower, "slot_") {
		return components.NineSlice{Left: 4, Top: 4, Right: 4, Bottom: 4}
	}

	// Banner — the only true cap/end-cap sprite in the pack;
	// 6 px corners match its aesthetic weight.
	if strings.Contains(lower, "banner") {
		return components.NineSlice{Left: 6, Top: 6, Right: 6, Bottom: 6}
	}

	// Text fields — 6 px corners are the established convention
	// across UI kits; matches the button family visually.
	if strings.Contains(lower, "textfield") {
		return components.NineSlice{Left: 6, Top: 6, Right: 6, Bottom: 6}
	}

	// Single-cell icons (arrow, checkmark, cross, select indicators).
	// These aren't really 9-sliceable; return 1 px so the validator
	// accepts the row and the renderer can fall back to drawing the
	// sprite at native size when width <= 2 * inset.
	return components.NineSlice{Left: 1, Top: 1, Right: 1, Bottom: 1}
}

// clampSlice ensures left+right < width and top+bottom < height.
// The center cell needs at least 1 px in each axis or the
// NineSliceSprite construction will fail. We never inflate the
// inset; only shrink toward the safety floor.
func clampSlice(s components.NineSlice, width, height int) components.NineSlice {
	maxH := width / 4
	if maxH < 1 {
		maxH = 1
	}
	maxV := height / 4
	if maxV < 1 {
		maxV = 1
	}
	if int(s.Left) > maxH {
		s.Left = int32(maxH)
	}
	if int(s.Right) > maxH {
		s.Right = int32(maxH)
	}
	if int(s.Top) > maxV {
		s.Top = int32(maxV)
	}
	if int(s.Bottom) > maxV {
		s.Bottom = int32(maxV)
	}
	if s.Left < 1 {
		s.Left = 1
	}
	if s.Top < 1 {
		s.Top = 1
	}
	if s.Right < 1 {
		s.Right = 1
	}
	if s.Bottom < 1 {
		s.Bottom = 1
	}
	return s
}
