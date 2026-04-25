package components

import (
	"encoding/json"

	"boxland/server/internal/configurable"
)

// PLAN.md §128: components needed by automations. Health, Inventory,
// AIBehavior, Spawner, Resource, Trigger, AudioEmitter, LightSource.
// Each registers with Descriptor() so the Entity Manager UI updates
// automatically.

// ---- Kind constants ----

const (
	KindHealth       Kind = "health"
	KindInventory    Kind = "inventory"
	KindAIBehavior   Kind = "ai_behavior"
	KindSpawner      Kind = "spawner"
	KindResource     Kind = "resource"
	KindTrigger      Kind = "trigger"
	KindAudioEmitter Kind = "audio_emitter"
	KindLightSource  Kind = "light_source"
)

// ---- Health ----

type Health struct {
	Max     int32 `json:"max"`
	Regen   int32 `json:"regen_per_sec"`
}

func (Health) Validate() error { return nil }

var healthDef = simpleDef(KindHealth, Health{Max: 100},
	[]configurable.FieldDescriptor{
		{Key: "max",           Label: "Max HP",           Kind: configurable.KindInt, Default: 100},
		{Key: "regen_per_sec", Label: "Regen / sec",      Kind: configurable.KindInt, Default: 0},
	})

// ---- Inventory ----

type InventorySlot struct {
	ItemID int64 `json:"item_id"`
	Qty    int32 `json:"qty"`
}

type Inventory struct {
	Slots []InventorySlot `json:"slots"`
	MaxSlots int32 `json:"max_slots"`
}

func (Inventory) Validate() error { return nil }

var inventoryDef = simpleDef(KindInventory, Inventory{MaxSlots: 16},
	[]configurable.FieldDescriptor{
		{Key: "max_slots", Label: "Max slots", Kind: configurable.KindInt, Default: 16},
		{Key: "slots", Label: "Starter slots", Kind: configurable.KindList, Children: []configurable.FieldDescriptor{
			{Key: "item_id", Label: "Item entity-type", Kind: configurable.KindEntityTypeRef, Required: true},
			{Key: "qty", Label: "Qty", Kind: configurable.KindInt, Default: 1},
		}},
	})

// ---- AIBehavior ----

type AIBehavior struct {
	Mode      string `json:"mode"`        // "wander", "guard", "patrol", "chase"
	RangeSub  int32  `json:"range_sub"`   // wander/guard radius
}

func (AIBehavior) Validate() error { return nil }

var aiBehaviorDef = simpleDef(KindAIBehavior, AIBehavior{Mode: "wander", RangeSub: 4 * 32 * 256},
	[]configurable.FieldDescriptor{
		{Key: "mode", Label: "Mode", Kind: configurable.KindEnum, Default: "wander", Options: []configurable.EnumOption{
			{Value: "wander", Label: "Wander"},
			{Value: "guard", Label: "Guard"},
			{Value: "patrol", Label: "Patrol"},
			{Value: "chase", Label: "Chase"},
		}},
		{Key: "range_sub", Label: "Range (sub-pixels)", Kind: configurable.KindInt, Default: 4 * 32 * 256},
	})

// ---- Spawner ----

type Spawner struct {
	SpawnTypeID  int64 `json:"spawn_type_id"`
	IntervalMs   int32 `json:"interval_ms"`
	MaxAlive     int32 `json:"max_alive"`
	RangeSub     int32 `json:"range_sub"`
}

func (Spawner) Validate() error { return nil }

var spawnerDef = simpleDef(KindSpawner, Spawner{IntervalMs: 5000, MaxAlive: 4, RangeSub: 2 * 32 * 256},
	[]configurable.FieldDescriptor{
		{Key: "spawn_type_id", Label: "Spawn type", Kind: configurable.KindEntityTypeRef, Required: true},
		{Key: "interval_ms", Label: "Interval (ms)", Kind: configurable.KindInt, Default: 5000},
		{Key: "max_alive", Label: "Max alive", Kind: configurable.KindInt, Default: 4},
		{Key: "range_sub", Label: "Spawn radius (sub-pixels)", Kind: configurable.KindInt, Default: 2 * 32 * 256},
	})

// ---- Resource ----
//
// Generic counter (currency, ammo, mana, etc.). Multiple resources per
// entity by attaching multiple Resource components -- the Name field
// disambiguates inside the ECS.

type Resource struct {
	Name  string `json:"name"`
	Value int32  `json:"value"`
	Min   int32  `json:"min"`
	Max   int32  `json:"max"`
}

func (Resource) Validate() error { return nil }

var resourceDef = simpleDef(KindResource, Resource{Name: "currency", Min: 0, Max: 9999},
	[]configurable.FieldDescriptor{
		{Key: "name", Label: "Name", Kind: configurable.KindString, Required: true, Default: "currency"},
		{Key: "value", Label: "Initial value", Kind: configurable.KindInt, Default: 0},
		{Key: "min", Label: "Min", Kind: configurable.KindInt, Default: 0},
		{Key: "max", Label: "Max", Kind: configurable.KindInt, Default: 9999},
	})

// ---- Trigger (component) ----
//
// Marks an entity as the source of an OnInteract / OnEnterTile event.
// The actual trigger config lives on entity_automations; this component
// only carries the per-entity opt-in flag + cooldown.

type TriggerComponent struct {
	CooldownMs int32 `json:"cooldown_ms"`
	OneShot    bool  `json:"one_shot"`
}

func (TriggerComponent) Validate() error { return nil }

var triggerDef = simpleDef(KindTrigger, TriggerComponent{CooldownMs: 0},
	[]configurable.FieldDescriptor{
		{Key: "cooldown_ms", Label: "Cooldown (ms)", Kind: configurable.KindInt, Default: 0},
		{Key: "one_shot", Label: "One-shot", Kind: configurable.KindBool, Default: false},
	})

// ---- AudioEmitter ----

type AudioEmitter struct {
	SoundID    int64 `json:"sound_id"`
	IntervalMs int32 `json:"interval_ms"`
	Volume     uint8 `json:"volume"`
}

func (AudioEmitter) Validate() error { return nil }

var audioEmitterDef = simpleDef(KindAudioEmitter, AudioEmitter{Volume: 200, IntervalMs: 0},
	[]configurable.FieldDescriptor{
		{Key: "sound_id", Label: "Audio asset", Kind: configurable.KindAssetRef, RefTags: []string{"audio"}, Required: true},
		{Key: "interval_ms", Label: "Loop interval (ms; 0 = on-trigger only)", Kind: configurable.KindInt, Default: 0},
		{Key: "volume", Label: "Volume (0..255)", Kind: configurable.KindInt, Default: 200},
	})

// ---- LightSource ----

type LightSource struct {
	Color     uint32 `json:"color"`
	Intensity uint8  `json:"intensity"`
	RadiusSub int32  `json:"radius_sub"`
}

func (LightSource) Validate() error { return nil }

var lightSourceDef = simpleDef(KindLightSource, LightSource{Color: 0xfff0c8ff, Intensity: 200, RadiusSub: 4 * 32 * 256},
	[]configurable.FieldDescriptor{
		{Key: "color", Label: "Color (0xRRGGBBAA)", Kind: configurable.KindColor, Default: uint32(0xfff0c8ff)},
		{Key: "intensity", Label: "Intensity (0..255)", Kind: configurable.KindInt, Default: 200},
		{Key: "radius_sub", Label: "Radius (sub-pixels)", Kind: configurable.KindInt, Default: 4 * 32 * 256},
	})

// ---- helper ------------------------------------------------------------

// simpleDef wires a Definition for a Configurable whose JSON shape
// matches the receiver type exactly. Avoids the boring boilerplate
// every component had been duplicating before this batch.
func simpleDef[T interface{ Validate() error }](kind Kind, defaults T, descriptor []configurable.FieldDescriptor) Definition {
	return Definition{
		Kind:    kind,
		Storage: StorageSparseSet,
		Descriptor: func() []configurable.FieldDescriptor { return descriptor },
		Validate: func(raw json.RawMessage) error {
			var zero T
			if len(raw) == 0 {
				return zero.Validate()
			}
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return v.Validate()
		},
		Default: func() any { return defaults },
		Decode: func(raw json.RawMessage) (any, error) {
			if len(raw) == 0 {
				return defaults, nil
			}
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			return v, nil
		},
	}
}
