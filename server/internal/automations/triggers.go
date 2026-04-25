package automations

import (
	"encoding/json"
	"errors"

	"boxland/server/internal/configurable"
)

// ---- Trigger configs --------------------------------------------------

// EntityNearbyConfig fires while at least one entity of `target_type`
// is within `radius_sub` sub-pixels.
type EntityNearbyConfig struct {
	TargetTypeID int64  `json:"target_type_id"`
	RadiusSub    int32  `json:"radius_sub"`
	MinCount     int32  `json:"min_count"`
}

func (c EntityNearbyConfig) Validate() error {
	if c.RadiusSub < 0 {
		return errors.New("entity_nearby: radius_sub must be >= 0")
	}
	if c.MinCount < 0 {
		return errors.New("entity_nearby: min_count must be >= 0")
	}
	return nil
}

// EntityAbsentConfig fires while NO entities of `target_type` are within
// `radius_sub` sub-pixels (the inverse of EntityNearby).
type EntityAbsentConfig struct {
	TargetTypeID int64 `json:"target_type_id"`
	RadiusSub    int32 `json:"radius_sub"`
}

func (c EntityAbsentConfig) Validate() error {
	if c.RadiusSub < 0 {
		return errors.New("entity_absent: radius_sub must be >= 0")
	}
	return nil
}

// ResourceThresholdConfig fires while a resource crosses a threshold.
// Op is "<", "<=", ">", ">=", "==".
type ResourceThresholdConfig struct {
	Resource string `json:"resource"`
	Op       string `json:"op"`
	Value    int32  `json:"value"`
}

func (c ResourceThresholdConfig) Validate() error {
	switch c.Op {
	case "<", "<=", ">", ">=", "==":
	default:
		return errors.New("resource_threshold: op must be one of <, <=, >, >=, ==")
	}
	if c.Resource == "" {
		return errors.New("resource_threshold: resource required")
	}
	return nil
}

// TimerConfig fires every `interval_ms` milliseconds. `jitter_ms` adds
// randomness so identical-config entities don't all fire on the same
// tick boundary.
type TimerConfig struct {
	IntervalMs int32 `json:"interval_ms"`
	JitterMs   int32 `json:"jitter_ms"`
}

func (c TimerConfig) Validate() error {
	if c.IntervalMs < 100 {
		return errors.New("timer: interval_ms must be >= 100")
	}
	if c.JitterMs < 0 {
		return errors.New("timer: jitter_ms must be >= 0")
	}
	return nil
}

// OnSpawnConfig fires once when the entity is created.
type OnSpawnConfig struct{}
func (OnSpawnConfig) Validate() error { return nil }

// OnDeathConfig fires once when the entity's hp reaches 0 (or its
// lifetime expires for non-Health entities).
type OnDeathConfig struct{}
func (OnDeathConfig) Validate() error { return nil }

// OnInteractConfig fires when a player issues an Interact verb against
// the entity.
type OnInteractConfig struct{}
func (OnInteractConfig) Validate() error { return nil }

// OnEnterTileConfig fires when the entity steps onto a tile of the
// configured collision-shape preset (or any tile if `any` is set).
type OnEnterTileConfig struct {
	Any           bool  `json:"any"`
	ShapeFilter   int32 `json:"shape_filter"`   // matches CollisionShape enum
}

func (c OnEnterTileConfig) Validate() error { return nil }

// ---- Helper to wire a Definition from a Configurable struct ---------
//
// All the trigger configs above share the boring decode/validate
// wiring. We build their Definition from a small generic helper.

type cfgFactory func() any

func makeDefinition[T interface{ Validate() error }](kind string, descriptor func() []configurable.FieldDescriptor, defaults T) Definition {
	mk := func() any { return defaults }
	return Definition{
		Kind:       kind,
		Descriptor: descriptor,
		Validate: func(raw json.RawMessage) error {
			if len(raw) == 0 {
				var zero T
				return zero.Validate()
			}
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return v.Validate()
		},
		Default: mk,
		Decode: func(raw json.RawMessage) (any, error) {
			if len(raw) == 0 {
				return mk(), nil
			}
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			return v, nil
		},
	}
}

// ---- DefaultTriggers --------------------------------------------------

// DefaultTriggers returns a Registry with every built-in trigger.
// Boot calls this once and shares the result.
func DefaultTriggers() *Registry {
	r := NewRegistry()

	r.Register(makeDefinition(string(TriggerEntityNearby),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "target_type_id", Label: "Target type", Kind: configurable.KindEntityTypeRef, Required: true},
				{Key: "radius_sub", Label: "Radius (sub-pixels)", Kind: configurable.KindInt, Default: 8 * 32 * 256},
				{Key: "min_count", Label: "Min count", Kind: configurable.KindInt, Default: 1},
			}
		},
		EntityNearbyConfig{}))

	r.Register(makeDefinition(string(TriggerEntityAbsent),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "target_type_id", Label: "Target type", Kind: configurable.KindEntityTypeRef, Required: true},
				{Key: "radius_sub", Label: "Radius (sub-pixels)", Kind: configurable.KindInt, Default: 8 * 32 * 256},
			}
		},
		EntityAbsentConfig{}))

	r.Register(makeDefinition(string(TriggerResourceThreshold),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "resource", Label: "Resource", Kind: configurable.KindString, Required: true},
				{Key: "op", Label: "Operator", Kind: configurable.KindEnum, Options: []configurable.EnumOption{
					{Value: "<", Label: "<"},
					{Value: "<=", Label: "≤"},
					{Value: ">", Label: ">"},
					{Value: ">=", Label: "≥"},
					{Value: "==", Label: "="},
				}, Default: "<="},
				{Key: "value", Label: "Threshold", Kind: configurable.KindInt, Default: 0},
			}
		},
		ResourceThresholdConfig{Op: "<="}))

	r.Register(makeDefinition(string(TriggerTimer),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "interval_ms", Label: "Interval (ms)", Kind: configurable.KindInt, Default: 1000},
				{Key: "jitter_ms", Label: "Jitter (ms)", Kind: configurable.KindInt, Default: 0},
			}
		},
		TimerConfig{IntervalMs: 1000}))

	r.Register(makeDefinition(string(TriggerOnSpawn),
		func() []configurable.FieldDescriptor { return nil },
		OnSpawnConfig{}))

	r.Register(makeDefinition(string(TriggerOnDeath),
		func() []configurable.FieldDescriptor { return nil },
		OnDeathConfig{}))

	r.Register(makeDefinition(string(TriggerOnInteract),
		func() []configurable.FieldDescriptor { return nil },
		OnInteractConfig{}))

	r.Register(makeDefinition(string(TriggerOnEnterTile),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "any", Label: "Any tile", Kind: configurable.KindBool, Default: true},
				{Key: "shape_filter", Label: "Collision-shape filter", Kind: configurable.KindInt, Default: 0,
					Help: "0=Open, 1=Solid, 2=WallN, 3=WallE, 4=WallS, 5=WallW, 6..9=Diagonals, 10..13=Halves"},
			}
		},
		OnEnterTileConfig{Any: true}))

	return r
}
