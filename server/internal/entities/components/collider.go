package components

import (
	"encoding/json"
	"errors"

	"boxland/server/internal/configurable"
)

// Collider overrides the entity type's default AABB. Most entities don't
// need this -- the type's collider_w/h/anchor on entity_types is enough.
// This component exists for runtime-mutable colliders (e.g. a rolling ball
// whose collider changes shape when it transforms).
//
// Mask is the entity's collision-layer bitmask. 0 = a ghost (collides with
// nothing). Default at the entity-type level is 1 ("land") per PLAN.md §1.
type Collider struct {
	W       uint16 `json:"w"`        // pixels (sub-px = w * 256)
	H       uint16 `json:"h"`
	AnchorX uint16 `json:"anchor_x"`
	AnchorY uint16 `json:"anchor_y"`
	Mask    uint32 `json:"mask"`
}

func (c Collider) Validate() error {
	if c.AnchorX > c.W || c.AnchorY > c.H {
		return errors.New("collider: anchor outside W/H bounds")
	}
	return nil
}

var colliderDef = Definition{
	Kind:    KindCollider,
	Storage: StorageSparseSet,
	Descriptor: func() []configurable.FieldDescriptor {
		zero := 0.0
		return []configurable.FieldDescriptor{
			{Key: "w", Label: "Width (px)", Kind: configurable.KindInt, Min: &zero},
			{Key: "h", Label: "Height (px)", Kind: configurable.KindInt, Min: &zero},
			{Key: "anchor_x", Label: "Anchor X (px)", Kind: configurable.KindInt, Min: &zero,
				Help: "Offset from sprite top-left to the collider origin."},
			{Key: "anchor_y", Label: "Anchor Y (px)", Kind: configurable.KindInt, Min: &zero},
			{Key: "mask", Label: "Collision mask", Kind: configurable.KindInt, Min: &zero,
				Help: "uint32 bitmask. 1 = land (default), see project's collision-layer table."},
		}
	},
	Validate: func(raw json.RawMessage) error {
		var c Collider
		if len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		return c.Validate()
	},
	Default: func() any { return Collider{} },
	Decode: func(raw json.RawMessage) (any, error) {
		var c Collider
		if len(raw) == 0 {
			return c, nil
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		return c, nil
	},
}
