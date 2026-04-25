package components

import (
	"encoding/json"

	"boxland/server/internal/configurable"
)

// Velocity is the per-tick movement intent in sub-pixels per tick.
// Movement systems consume this; nothing about Velocity is "physics" --
// the swept-AABB collision (PLAN.md §4h, schemas/collision.md) clips the
// applied delta to whatever the world allows.
type Velocity struct {
	VX int32 `json:"vx"`
	VY int32 `json:"vy"`
	// MaxSpeed caps the magnitude per tick. 0 = no cap (use sparingly).
	MaxSpeed int32 `json:"max_speed"`
}

func (Velocity) Validate() error { return nil }

var velocityDef = Definition{
	Kind:    KindVelocity,
	Storage: StorageSparseSet,
	Descriptor: func() []configurable.FieldDescriptor {
		zero := 0.0
		return []configurable.FieldDescriptor{
			{Key: "vx", Label: "VX", Kind: configurable.KindInt},
			{Key: "vy", Label: "VY", Kind: configurable.KindInt},
			{Key: "max_speed", Label: "Max speed (sub-px / tick)", Kind: configurable.KindInt, Min: &zero,
				Help: "Caps |velocity| each tick. 0 disables the cap."},
		}
	},
	Validate: func(raw json.RawMessage) error {
		var v Velocity
		if len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		return v.Validate()
	},
	Default: func() any { return Velocity{} },
	Decode: func(raw json.RawMessage) (any, error) {
		var v Velocity
		if len(raw) == 0 {
			return v, nil
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return v, nil
	},
}
