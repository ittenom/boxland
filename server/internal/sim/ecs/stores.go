package ecs

import (
	"boxland/server/internal/entities/components"
)

// Stores bundles a typed ComponentStore per built-in kind. Keeping them
// as concrete fields (not a map[Kind]*store) means systems read them
// without an interface lookup or type assertion.
//
// New built-in kinds extend this struct; per-game custom components can
// live in their own struct beside it.
type Stores struct {
	Position *ComponentStore[components.Position]
	Velocity *ComponentStore[components.Velocity]
	Sprite   *ComponentStore[components.Sprite]
	Collider *ComponentStore[components.Collider]
	Tile     *ComponentStore[components.Tile]
	Static   *ComponentStore[components.Static]
}

// NewStores constructs every store sized for `expectedSlots` entities.
// Wire the returned Stores onto a World via `world.SetStores(...)`.
func NewStores(expectedSlots int) *Stores {
	return &Stores{
		Position: NewComponentStore[components.Position](expectedSlots),
		Velocity: NewComponentStore[components.Velocity](expectedSlots),
		Sprite:   NewComponentStore[components.Sprite](expectedSlots),
		Collider: NewComponentStore[components.Collider](expectedSlots),
		Tile:     NewComponentStore[components.Tile](expectedSlots),
		Static:   NewComponentStore[components.Static](expectedSlots),
	}
}

// RemoveAll drops every component the entity owns across every store.
// Called by World.Despawn so callers don't have to remember each kind.
func (s *Stores) RemoveAll(e EntityID) {
	if s == nil {
		return
	}
	s.Position.Remove(e)
	s.Velocity.Remove(e)
	s.Sprite.Remove(e)
	s.Collider.Remove(e)
	s.Tile.Remove(e)
	s.Static.Remove(e)
}
