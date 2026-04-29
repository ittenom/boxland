package components

import (
	"encoding/json"
	"errors"
	"fmt"

	"boxland/server/internal/configurable"
)

// KindNineSlice names the component that carries 9-slice insets for a
// UI sprite. Used by entity_class='ui' rows so the editor's NineSlice
// renderer (and the in-game HUD's NineSliceSprite) can resize the
// frame to any dimensions without distorting the corner art.
//
// The four insets (Left, Top, Right, Bottom) are measured in source
// texture pixels — the SAME units `AnimationFrame.sw/sh` reports. The
// renderer divides the source rect into nine rectangles using these
// insets:
//
//   ┌────┬────────┬────┐
//   │ TL │   T    │ TR │   Top row stretches horizontally only.
//   ├────┼────────┼────┤
//   │ L  │ Center │ R  │   Center stretches both axes.
//   ├────┼────────┼────┤
//   │ BL │   B    │ BR │   Bottom row stretches horizontally only.
//   └────┴────────┴────┘
//
// Insets must satisfy left + right < source_width and top + bottom <
// source_height; otherwise the center cell collapses and the sprite
// degenerates. We don't know the source dimensions at component
// validation time (they live on the asset, not the component), so we
// only sanity-check that the insets are positive — the editor's seeder
// + the asset measurer enforce the dimensional constraint.
const KindNineSlice Kind = "nine_slice"

// NineSlice carries the four 9-slice insets in source texture pixels.
type NineSlice struct {
	Left   int32 `json:"left"`
	Top    int32 `json:"top"`
	Right  int32 `json:"right"`
	Bottom int32 `json:"bottom"`
}

// ErrInvalidNineSlice is returned by Validate when an inset is
// non-positive. Stable for handler error mapping.
var ErrInvalidNineSlice = errors.New("nine_slice: insets must be > 0")

// Validate runs the cheap sanity check (positivity). Dimensional
// validity (insets fit inside the source sprite) is enforced at the
// next layer up — see entities.Service when it stores the row, or
// the seeder when it imports a UI pack PNG.
func (n NineSlice) Validate() error {
	if n.Left <= 0 || n.Top <= 0 || n.Right <= 0 || n.Bottom <= 0 {
		return fmt.Errorf("%w: got {%d,%d,%d,%d}",
			ErrInvalidNineSlice, n.Left, n.Top, n.Right, n.Bottom)
	}
	return nil
}

var nineSliceDef = Definition{
	Kind:    KindNineSlice,
	Storage: StorageSparseSet,
	Descriptor: func() []configurable.FieldDescriptor {
		// Min=1 reflects the Validate() rule; Max=64 is a soft
		// cap that catches obvious typos (the largest sprite in
		// the gradient pack is 64x64 so a 64-px corner would
		// consume the entire sprite).
		one := 1.0
		sixtyFour := 64.0
		return []configurable.FieldDescriptor{
			{Key: "left", Label: "Left inset", Kind: configurable.KindInt, Required: true, Min: &one, Max: &sixtyFour, Help: "Left edge in source pixels."},
			{Key: "top", Label: "Top inset", Kind: configurable.KindInt, Required: true, Min: &one, Max: &sixtyFour, Help: "Top edge in source pixels."},
			{Key: "right", Label: "Right inset", Kind: configurable.KindInt, Required: true, Min: &one, Max: &sixtyFour, Help: "Right edge in source pixels."},
			{Key: "bottom", Label: "Bottom inset", Kind: configurable.KindInt, Required: true, Min: &one, Max: &sixtyFour, Help: "Bottom edge in source pixels."},
		}
	},
	Validate: func(raw json.RawMessage) error {
		if len(raw) == 0 {
			return fmt.Errorf("%w: empty config", ErrInvalidNineSlice)
		}
		var n NineSlice
		if err := json.Unmarshal(raw, &n); err != nil {
			return err
		}
		return n.Validate()
	},
	Default: func() any { return NineSlice{Left: 6, Top: 6, Right: 6, Bottom: 6} },
	Decode: func(raw json.RawMessage) (any, error) {
		var n NineSlice
		if len(raw) == 0 {
			return n, nil
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		return n, nil
	},
}
