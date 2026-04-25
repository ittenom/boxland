package components

import (
	"encoding/json"

	"boxland/server/internal/configurable"
)

// Position is the entity's world location in sub-pixel units (matches the
// canonical 1 px = 256 sub-units convention from schemas/collision.md).
//
// Almost every entity has a Position. The runtime treats absence as
// (0, 0), but the editor lets designers set an initial spawn point on a
// per-entity-type basis (some prefab spawns may snap to a tile centre).
type Position struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
}

func (Position) Validate() error { return nil }

var positionDef = Definition{
	Kind:    KindPosition,
	Storage: StorageSparseSet,
	Descriptor: func() []configurable.FieldDescriptor {
		return []configurable.FieldDescriptor{
			{Key: "x", Label: "X", Kind: configurable.KindInt, Help: "World X in sub-pixels (1 px = 256)."},
			{Key: "y", Label: "Y", Kind: configurable.KindInt, Help: "World Y in sub-pixels."},
		}
	},
	Validate: func(raw json.RawMessage) error {
		var p Position
		if len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		return p.Validate()
	},
	Default: func() any { return Position{} },
	Decode: func(raw json.RawMessage) (any, error) {
		var p Position
		if len(raw) == 0 {
			return p, nil
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return p, nil
	},
}
