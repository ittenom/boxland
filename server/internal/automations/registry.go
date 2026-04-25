// Package automations defines the trigger + action catalogs and the
// no-code AST used to express per-entity-type automations.
//
// PLAN.md §1 (Automations): "Per-type (with type copy-paste); no-code
// AST that compiles to ECS systems". The editor (task #127) consumes
// each Definition's Descriptor() so adding a trigger or action gives
// the UI for free.
//
// Two parallel registries — Triggers and Actions — sharing the same
// Definition shape. The AST glues them together (see ast.go).
package automations

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"boxland/server/internal/configurable"
)

// TriggerKind / ActionKind are stable string identifiers stored in the
// AST JSON. Convention: lower_snake.
type TriggerKind string
type ActionKind string

// Built-in trigger kinds (PLAN.md §122).
const (
	TriggerEntityNearby      TriggerKind = "entity_nearby"
	TriggerEntityAbsent      TriggerKind = "entity_absent"
	TriggerResourceThreshold TriggerKind = "resource_threshold"
	TriggerTimer             TriggerKind = "timer"
	TriggerOnSpawn           TriggerKind = "on_spawn"
	TriggerOnDeath           TriggerKind = "on_death"
	TriggerOnInteract        TriggerKind = "on_interact"
	TriggerOnEnterTile       TriggerKind = "on_enter_tile"
)

// Built-in action kinds (PLAN.md §123).
const (
	ActionSpawn          ActionKind = "spawn"
	ActionDespawn        ActionKind = "despawn"
	ActionMoveToward     ActionKind = "move_toward"
	ActionMoveAway       ActionKind = "move_away"
	ActionSetSpeed       ActionKind = "set_speed"
	ActionSetSprite      ActionKind = "set_sprite"
	ActionSetAnimation   ActionKind = "set_animation"
	ActionSetVariant     ActionKind = "set_variant"
	ActionSetTint        ActionKind = "set_tint"
	ActionPlaySound      ActionKind = "play_sound"
	ActionEmitLight      ActionKind = "emit_light"
	ActionAdjustResource ActionKind = "adjust_resource"
)

// Definition describes one trigger or action end-to-end. Mirrors
// components.Definition so the form renderer can drive both the same way.
type Definition struct {
	Kind       string                                  // string view of TriggerKind / ActionKind
	Descriptor func() []configurable.FieldDescriptor
	Validate   func(json.RawMessage) error
	Default    func() any
	Decode     func(json.RawMessage) (any, error)
}

// Registry holds Definitions of one variety (triggers OR actions).
type Registry struct {
	mu   sync.RWMutex
	defs map[string]Definition
}

// NewRegistry returns an empty registry. Tests use this; production
// uses DefaultTriggers() / DefaultActions().
func NewRegistry() *Registry {
	return &Registry{defs: make(map[string]Definition)}
}

// Register adds a definition. Panics on duplicate Kind so misconfigured
// boots fail loudly.
func (r *Registry) Register(def Definition) {
	if def.Kind == "" {
		panic("automations: Definition.Kind required")
	}
	if def.Descriptor == nil || def.Validate == nil || def.Default == nil || def.Decode == nil {
		panic(fmt.Sprintf("automations.Register(%q): all hook functions are required", def.Kind))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.defs[def.Kind]; ok {
		panic(fmt.Sprintf("automations: duplicate kind %q", def.Kind))
	}
	r.defs[def.Kind] = def
}

// Get returns the definition for a kind, or false.
func (r *Registry) Get(k string) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.defs[k]
	return d, ok
}

// Kinds returns every registered kind, sorted (stable for UI lists).
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.defs))
	for k := range r.defs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Has reports whether a kind is registered.
func (r *Registry) Has(k string) bool {
	_, ok := r.Get(k)
	return ok
}

// ErrUnknownKind is returned by lookups against unregistered kinds.
var ErrUnknownKind = errors.New("automations: unknown kind")
