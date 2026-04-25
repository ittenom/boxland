package components

import (
	"encoding/json"

	"boxland/server/internal/configurable"
)

// Tile marks an entity as part of the tile grid (PLAN.md §1 "Tiles ARE
// entities"). Carries the layer + grid coordinate so the renderer + WFC
// can find it. The collision shape lives in the entity type's collider
// fields (entity_types.collider_*), not on this component.
//
// Most tile-kind entities never spawn dynamically; the chunked map loader
// (task #103) materializes Tile + Static + Sprite + Collider as a unit.
type Tile struct {
	LayerID uint16 `json:"layer_id"`
	GX      int32  `json:"gx"`
	GY      int32  `json:"gy"`
}

func (Tile) Validate() error { return nil }

var tileDef = Definition{
	Kind:    KindTile,
	Storage: StorageSparseSet,
	Descriptor: func() []configurable.FieldDescriptor {
		return []configurable.FieldDescriptor{
			{Key: "layer_id", Label: "Layer id", Kind: configurable.KindInt,
				Help: "Map layer ordinal. 0 = base."},
			{Key: "gx", Label: "Grid X", Kind: configurable.KindInt},
			{Key: "gy", Label: "Grid Y", Kind: configurable.KindInt},
		}
	},
	Validate: func(raw json.RawMessage) error {
		if len(raw) == 0 {
			return nil
		}
		var t Tile
		if err := json.Unmarshal(raw, &t); err != nil {
			return err
		}
		return t.Validate()
	},
	Default: func() any { return Tile{} },
	Decode: func(raw json.RawMessage) (any, error) {
		var t Tile
		if len(raw) == 0 {
			return t, nil
		}
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, err
		}
		return t, nil
	},
}

// Static marks an entity as immovable. The movement system skips entities
// owning Static so tile entities don't pay per-tick velocity-integration
// cost. Zero-config; the component is a tag.
type Static struct{}

func (Static) Validate() error { return nil }

var staticDef = Definition{
	Kind:    KindStatic,
	Storage: StorageSparseSet,
	Descriptor: func() []configurable.FieldDescriptor {
		return []configurable.FieldDescriptor{} // tag, no fields
	},
	Validate: func(raw json.RawMessage) error { return nil },
	Default:  func() any { return Static{} },
	Decode:   func(raw json.RawMessage) (any, error) { return Static{}, nil },
}
