// Package components defines the catalog of ECS components and the
// registry that hosts them. Each entry knows its own:
//
//   * Kind (the stable string used in entity_components.component_kind)
//   * Descriptor (drives the generic form renderer in views/_form.templ)
//   * Validate / Default (typed config helpers)
//   * Decode (turn the persisted config_json into a typed value the sim
//     hands to systems)
//
// Sparse-set storage lives in the ECS package (internal/sim) — that's
// where the per-kind ComponentStore[T] is constructed. This package only
// owns metadata and config validation; it has no runtime / no game state.
//
// The catalog is open-ended: add a new component by writing a new file in
// this package + Register()-ing into Default() and the world will pick it
// up. No migration needed.
package components

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"boxland/server/internal/configurable"
)

// Kind is the stable identifier for a component. Convention: lower_snake.
type Kind string

// Built-in component kinds. Add new constants alongside their definitions
// in the per-kind files (position.go, sprite.go, ...).
const (
	KindPosition Kind = "position"
	KindVelocity Kind = "velocity"
	KindSprite   Kind = "sprite"
	KindCollider Kind = "collider"
)

// Storage signals how the ECS should physically store this component.
// Sparse-set is the universal default per PLAN.md §1; flagging here keeps
// the door open to alt storage strategies (per-archetype columns,
// hierarchical octrees) without changing every component definition.
type Storage uint8

const (
	StorageSparseSet Storage = iota
)

// Definition describes one component kind end-to-end.
type Definition struct {
	Kind       Kind
	Storage    Storage
	Descriptor func() []configurable.FieldDescriptor // drives the editor UI
	Validate   func(json.RawMessage) error            // run before persist
	Default    func() any                             // typed default value
	Decode     func(json.RawMessage) (any, error)     // persisted -> typed
}

// Registry holds the active set of component definitions.
type Registry struct {
	mu    sync.RWMutex
	defs  map[Kind]Definition
}

// NewRegistry returns an empty registry. Tests construct fresh ones; the
// production path uses Default().
func NewRegistry() *Registry {
	return &Registry{defs: make(map[Kind]Definition)}
}

// Register adds a definition. Panics on duplicate Kind so the bug surfaces
// at boot time.
func (r *Registry) Register(def Definition) {
	if def.Kind == "" {
		panic("components: Definition.Kind required")
	}
	if def.Descriptor == nil || def.Validate == nil || def.Default == nil || def.Decode == nil {
		panic(fmt.Sprintf("components.Register(%q): all hook functions are required", def.Kind))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.defs[def.Kind]; ok {
		panic(fmt.Sprintf("components: duplicate kind %q", def.Kind))
	}
	r.defs[def.Kind] = def
}

// Get returns the definition for a kind, or false.
func (r *Registry) Get(k Kind) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.defs[k]
	return d, ok
}

// Kinds returns every registered kind, sorted (stable for UI ordering).
func (r *Registry) Kinds() []Kind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Kind, 0, len(r.defs))
	for k := range r.defs {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Has reports whether a kind is registered.
func (r *Registry) Has(k Kind) bool {
	_, ok := r.Get(k)
	return ok
}

// Default returns the production registry with every built-in component
// registered. Server boot calls this once and shares the result.
func Default() *Registry {
	r := NewRegistry()
	r.Register(positionDef)
	r.Register(velocityDef)
	r.Register(spriteDef)
	r.Register(colliderDef)
	return r
}

// ErrUnknownKind is returned by lookups against unregistered kinds.
var ErrUnknownKind = errors.New("components: unknown kind")

// ValidateAll runs Validate against a map of (kind -> config_json).
// Returns the first error or nil. Used by the EntityType artifact handler.
func (r *Registry) ValidateAll(configs map[Kind]json.RawMessage) error {
	for k, raw := range configs {
		def, ok := r.Get(k)
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownKind, k)
		}
		if err := def.Validate(raw); err != nil {
			return fmt.Errorf("component %s: %w", k, err)
		}
	}
	return nil
}
