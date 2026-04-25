// Package ecs is the runtime entity-component-system for the live game.
//
// Storage strategy: sparse-set (per PLAN.md §1 "ECS storage"). Each component
// kind owns a ComponentStore[T] holding:
//
//   * dense  []T              -- packed component data, GC-friendly
//   * owners []EntityID       -- parallel array, dense[i] belongs to owners[i]
//   * sparse []int32          -- flat index, sparse[entity.Index()] -> dense index
//
// O(1) add/remove via swap-with-last; dense iteration is cache-friendly;
// queries against multiple components walk the smaller set and probe the
// other(s).
//
// EntityID packs a generation counter so stale references can't accidentally
// point at a re-used slot. uint32 layout:
//
//   bits 0..23   index (16 M live entities per world)
//   bits 24..31  generation (256 reuses per slot before wraparound)
package ecs

import "fmt"

// EntityID identifies one entity. Zero is the reserved invalid id.
type EntityID uint32

// invalidEntity is returned by lookups against unknown entities.
const invalidEntity EntityID = 0

// indexBits, indexMask, and genShift split the EntityID bit layout.
const (
	indexBits = 24
	indexMask = (1 << indexBits) - 1
	genShift  = indexBits
)

// MakeEntityID composes an index + generation into an EntityID.
func MakeEntityID(index uint32, gen uint8) EntityID {
	if index > indexMask {
		panic(fmt.Sprintf("ecs: entity index %d exceeds %d-bit limit", index, indexBits))
	}
	return EntityID(uint32(gen)<<genShift | (index & indexMask))
}

// Index returns the per-world entity slot index.
func (e EntityID) Index() uint32 { return uint32(e) & indexMask }

// Generation returns the slot generation. Zero is a valid generation.
func (e EntityID) Generation() uint8 { return uint8(uint32(e) >> genShift) }

// IsValid reports whether the EntityID is non-zero (zero is the sentinel).
func (e EntityID) IsValid() bool { return e != invalidEntity }

// String renders an EntityID like "e#42:gen3" for log + test diagnostics.
func (e EntityID) String() string {
	if !e.IsValid() {
		return "e#invalid"
	}
	return fmt.Sprintf("e#%d:gen%d", e.Index(), e.Generation())
}
