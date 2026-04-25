package automations

import (
	"errors"

	"boxland/server/internal/configurable"
)

// ---- Action configs ---------------------------------------------------

// SpawnConfig spawns an entity of `type_id` at the source's position
// (or at offset_sub if non-zero).
type SpawnConfig struct {
	TypeID   int64 `json:"type_id"`
	OffsetX  int32 `json:"offset_x_sub"`
	OffsetY  int32 `json:"offset_y_sub"`
}

func (c SpawnConfig) Validate() error {
	if c.TypeID == 0 {
		return errors.New("spawn: type_id required")
	}
	return nil
}

// DespawnConfig removes the source entity (or `target_self=false`
// removes the trigger's target instead).
type DespawnConfig struct {
	TargetSelf bool `json:"target_self"`
}

func (DespawnConfig) Validate() error { return nil }

// MoveTowardConfig moves the source toward a target at SpeedSub /sec.
// Target is the trigger's matched entity (e.g. EntityNearby's target).
type MoveTowardConfig struct {
	SpeedSub int32 `json:"speed_sub_per_sec"`
}

func (c MoveTowardConfig) Validate() error {
	if c.SpeedSub < 0 {
		return errors.New("move_toward: speed must be >= 0")
	}
	return nil
}

// MoveAwayConfig is the inverse of MoveToward.
type MoveAwayConfig struct {
	SpeedSub int32 `json:"speed_sub_per_sec"`
}

func (c MoveAwayConfig) Validate() error {
	if c.SpeedSub < 0 {
		return errors.New("move_away: speed must be >= 0")
	}
	return nil
}

// SetSpeedConfig sets the entity's max speed.
type SetSpeedConfig struct {
	SpeedSub int32 `json:"speed_sub_per_sec"`
}

func (c SetSpeedConfig) Validate() error {
	if c.SpeedSub < 0 {
		return errors.New("set_speed: speed must be >= 0")
	}
	return nil
}

// SetSpriteConfig changes the source's sprite asset.
type SetSpriteConfig struct {
	AssetID int64 `json:"asset_id"`
}

func (c SetSpriteConfig) Validate() error {
	if c.AssetID == 0 {
		return errors.New("set_sprite: asset_id required")
	}
	return nil
}

// SetAnimationConfig switches the active animation.
type SetAnimationConfig struct {
	AnimID int32 `json:"anim_id"`
}

func (SetAnimationConfig) Validate() error { return nil }

// SetVariantConfig swaps the palette variant (PLAN.md §1 palette swap).
type SetVariantConfig struct {
	VariantID int32 `json:"variant_id"`
}

func (SetVariantConfig) Validate() error { return nil }

// SetTintConfig applies a runtime multiply tint (NOT a palette change).
type SetTintConfig struct {
	Color uint32 `json:"color"` // 0xRRGGBBAA, 0 = clear
	DurationMs int32 `json:"duration_ms"` // 0 = permanent
}

func (c SetTintConfig) Validate() error {
	if c.DurationMs < 0 {
		return errors.New("set_tint: duration_ms must be >= 0")
	}
	return nil
}

// PlaySoundConfig plays an audio asset positionally at the source.
type PlaySoundConfig struct {
	SoundID int64 `json:"sound_id"`
	Volume  uint8 `json:"volume"`
	Pitch   int16 `json:"pitch_cents"`
}

func (c PlaySoundConfig) Validate() error {
	if c.SoundID == 0 {
		return errors.New("play_sound: sound_id required")
	}
	return nil
}

// EmitLightConfig writes a lighting cell at the source's tile, lasting
// DurationMs.
type EmitLightConfig struct {
	Color      uint32 `json:"color"`
	Intensity  uint8  `json:"intensity"`
	DurationMs int32  `json:"duration_ms"`
}

func (c EmitLightConfig) Validate() error {
	if c.DurationMs < 0 {
		return errors.New("emit_light: duration_ms must be >= 0")
	}
	return nil
}

// AdjustResourceConfig adds Delta to the named resource (negative for
// damage / drain).
type AdjustResourceConfig struct {
	Resource string `json:"resource"`
	Delta    int32  `json:"delta"`
}

func (c AdjustResourceConfig) Validate() error {
	if c.Resource == "" {
		return errors.New("adjust_resource: resource required")
	}
	return nil
}

// ---- DefaultActions ---------------------------------------------------

// DefaultActions returns a Registry with every built-in action.
func DefaultActions() *Registry {
	r := NewRegistry()

	r.Register(makeDefinition(string(ActionSpawn),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "type_id", Label: "Entity type", Kind: configurable.KindEntityTypeRef, Required: true},
				{Key: "offset_x_sub", Label: "Offset X (sub-px)", Kind: configurable.KindInt},
				{Key: "offset_y_sub", Label: "Offset Y (sub-px)", Kind: configurable.KindInt},
			}
		},
		SpawnConfig{}))

	r.Register(makeDefinition(string(ActionDespawn),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "target_self", Label: "Target self", Kind: configurable.KindBool, Default: true},
			}
		},
		DespawnConfig{TargetSelf: true}))

	r.Register(makeDefinition(string(ActionMoveToward),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "speed_sub_per_sec", Label: "Speed (sub-px/sec)", Kind: configurable.KindInt, Default: 60 * 256},
			}
		},
		MoveTowardConfig{SpeedSub: 60 * 256}))

	r.Register(makeDefinition(string(ActionMoveAway),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "speed_sub_per_sec", Label: "Speed (sub-px/sec)", Kind: configurable.KindInt, Default: 60 * 256},
			}
		},
		MoveAwayConfig{SpeedSub: 60 * 256}))

	r.Register(makeDefinition(string(ActionSetSpeed),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "speed_sub_per_sec", Label: "Speed (sub-px/sec)", Kind: configurable.KindInt, Default: 60 * 256},
			}
		},
		SetSpeedConfig{SpeedSub: 60 * 256}))

	r.Register(makeDefinition(string(ActionSetSprite),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "asset_id", Label: "Sprite asset", Kind: configurable.KindAssetRef, Required: true, RefTags: []string{"sprite"}},
			}
		},
		SetSpriteConfig{}))

	r.Register(makeDefinition(string(ActionSetAnimation),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "anim_id", Label: "Animation id", Kind: configurable.KindInt, Default: 0},
			}
		},
		SetAnimationConfig{}))

	r.Register(makeDefinition(string(ActionSetVariant),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "variant_id", Label: "Variant id (0 = base)", Kind: configurable.KindInt, Default: 0},
			}
		},
		SetVariantConfig{}))

	r.Register(makeDefinition(string(ActionSetTint),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "color", Label: "Tint color (0xRRGGBBAA)", Kind: configurable.KindColor},
				{Key: "duration_ms", Label: "Duration (ms; 0 = permanent)", Kind: configurable.KindInt, Default: 0},
			}
		},
		SetTintConfig{}))

	r.Register(makeDefinition(string(ActionPlaySound),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "sound_id", Label: "Audio asset", Kind: configurable.KindAssetRef, Required: true, RefTags: []string{"audio"}},
				{Key: "volume", Label: "Volume (0..255)", Kind: configurable.KindInt, Default: 200},
				{Key: "pitch_cents", Label: "Pitch (cents)", Kind: configurable.KindInt, Default: 0},
			}
		},
		PlaySoundConfig{Volume: 200}))

	r.Register(makeDefinition(string(ActionEmitLight),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "color", Label: "Light color (0xRRGGBBAA)", Kind: configurable.KindColor},
				{Key: "intensity", Label: "Intensity (0..255)", Kind: configurable.KindInt, Default: 200},
				{Key: "duration_ms", Label: "Duration (ms)", Kind: configurable.KindInt, Default: 1000},
			}
		},
		EmitLightConfig{Intensity: 200, DurationMs: 1000}))

	r.Register(makeDefinition(string(ActionAdjustResource),
		func() []configurable.FieldDescriptor {
			return []configurable.FieldDescriptor{
				{Key: "resource", Label: "Resource", Kind: configurable.KindString, Required: true},
				{Key: "delta", Label: "Delta", Kind: configurable.KindInt, Default: 1},
			}
		},
		AdjustResourceConfig{}))

	return r
}
