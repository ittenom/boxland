package components

import (
	"encoding/json"
	"errors"

	"boxland/server/internal/configurable"
)

// Sprite is the renderable stitched onto an entity. AssetID + AnimID
// reference the asset pipeline; the renderer (web/src/render/scene) pulls
// the actual texture by these ids.
//
// Most entity types pin Sprite via the entity_types.sprite_asset_id
// column already, so this component is for entities whose sprite differs
// from the type's default (e.g. a unique boss outfit).
type Sprite struct {
	AssetID   uint32 `json:"asset_id"`
	// Frame is the atlas-cell index inside AssetID — row-major, 32x32
	// cells, top-left origin. 0 = first cell (the only cell on a plain
	// 32x32 sprite). Populated from entity_types.atlas_index at load
	// time so the renderer can draw a sub-rect of the source sheet.
	Frame     uint16 `json:"frame"`
	AnimID    uint32 `json:"anim_id"`
	VariantID uint16 `json:"variant_id"`
	Tint      uint32 `json:"tint"`        // 0xRRGGBBAA, 0 = none
	Layer     int16  `json:"layer"`       // render layer; higher draws on top
	// Facing matches the EntityState.facing wire encoding (0=N, 1=E,
	// 2=S, 3=W). Set by the animation system from the entity's
	// velocity each tick; stays sticky on a zero-velocity tick so a
	// stationary entity keeps its last-walked direction. Encoded into
	// the broadcast Diff so the renderer (and any spectator) can pick
	// the correct directional walk clip on its own.
	Facing    uint8  `json:"facing"`
}

func (s Sprite) Validate() error {
	if s.AssetID == 0 && s.VariantID != 0 {
		return errors.New("sprite: variant_id requires asset_id")
	}
	return nil
}

var spriteDef = Definition{
	Kind:    KindSprite,
	Storage: StorageSparseSet,
	Descriptor: func() []configurable.FieldDescriptor {
		return []configurable.FieldDescriptor{
			{Key: "asset_id", Label: "Asset", Kind: configurable.KindAssetRef, Help: "Sheet id."},
			{Key: "anim_id", Label: "Animation id", Kind: configurable.KindInt},
			{Key: "variant_id", Label: "Palette variant id", Kind: configurable.KindInt,
				Help: "0 = base art. Pre-baked variants are picked by id."},
			{Key: "tint", Label: "Runtime tint (0xRRGGBBAA)", Kind: configurable.KindColor,
				Help: "Secondary multiply for damage flash, freeze, etc. Not for palette swap."},
			{Key: "layer", Label: "Render layer", Kind: configurable.KindInt},
		}
	},
	Validate: func(raw json.RawMessage) error {
		var s Sprite
		if len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		return s.Validate()
	},
	Default: func() any { return Sprite{} },
	Decode: func(raw json.RawMessage) (any, error) {
		var s Sprite
		if len(raw) == 0 {
			return s, nil
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return s, nil
	},
}
