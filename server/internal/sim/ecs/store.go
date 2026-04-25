package ecs

// ComponentStore[T] is the typed storage for one component kind. The world
// holds one *ComponentStore[T] per registered kind; systems read/write
// directly through their store handle (no map lookup, no interface boxing).
//
// Internal layout:
//
//   dense   []T           -- packed component data
//   owners  []EntityID    -- dense[i] belongs to owners[i]
//   sparse  []int32       -- entity slot index -> dense index, -1 if absent
//
// Sparse is a flat slice (not map[EntityID]int) because contiguous memory +
// branchless lookups beat hash maps for this access pattern at every size
// we care about (10 to 100k components per kind).
type ComponentStore[T any] struct {
	dense  []T
	owners []EntityID
	sparse []int32
}

// NewComponentStore returns an empty store sized for `expectedSlots`
// entities. The store grows on demand; the hint just spares the first few
// reallocations.
func NewComponentStore[T any](expectedSlots int) *ComponentStore[T] {
	if expectedSlots < 0 {
		expectedSlots = 0
	}
	return &ComponentStore[T]{
		dense:  make([]T, 0, expectedSlots),
		owners: make([]EntityID, 0, expectedSlots),
		sparse: make([]int32, expectedSlots),
	}
}

// Has reports whether the entity owns a value of this component.
func (s *ComponentStore[T]) Has(e EntityID) bool {
	idx := e.Index()
	if int(idx) >= len(s.sparse) {
		return false
	}
	di := s.sparse[idx]
	if di < 0 || int(di) >= len(s.owners) {
		return false
	}
	return s.owners[di] == e
}

// Get returns a copy of the entity's component value. Use GetPtr for
// in-place mutation in hot loops.
func (s *ComponentStore[T]) Get(e EntityID) (T, bool) {
	idx := e.Index()
	if int(idx) >= len(s.sparse) {
		var zero T
		return zero, false
	}
	di := s.sparse[idx]
	if di < 0 || int(di) >= len(s.owners) || s.owners[di] != e {
		var zero T
		return zero, false
	}
	return s.dense[di], true
}

// GetPtr returns a pointer to the entity's component value, or nil if
// absent. Mutating the pointer is O(1) and avoids a copy.
func (s *ComponentStore[T]) GetPtr(e EntityID) *T {
	idx := e.Index()
	if int(idx) >= len(s.sparse) {
		return nil
	}
	di := s.sparse[idx]
	if di < 0 || int(di) >= len(s.owners) || s.owners[di] != e {
		return nil
	}
	return &s.dense[di]
}

// Set writes (or overwrites) the entity's component value. O(1) amortized.
func (s *ComponentStore[T]) Set(e EntityID, v T) {
	idx := e.Index()
	s.ensureSparse(idx)
	di := s.sparse[idx]
	if di >= 0 && int(di) < len(s.owners) && s.owners[di] == e {
		s.dense[di] = v
		return
	}
	s.sparse[idx] = int32(len(s.dense))
	s.dense = append(s.dense, v)
	s.owners = append(s.owners, e)
}

// Remove releases the entity's component, if any. O(1) via swap-with-last.
// Safe to call when the entity has no component (no-op).
func (s *ComponentStore[T]) Remove(e EntityID) {
	idx := e.Index()
	if int(idx) >= len(s.sparse) {
		return
	}
	di := s.sparse[idx]
	if di < 0 || int(di) >= len(s.owners) || s.owners[di] != e {
		return
	}
	last := int32(len(s.dense) - 1)
	if di != last {
		// Swap last into the freed slot.
		s.dense[di] = s.dense[last]
		s.owners[di] = s.owners[last]
		s.sparse[s.owners[di].Index()] = di
	}
	s.dense = s.dense[:last]
	s.owners = s.owners[:last]
	s.sparse[idx] = -1
}

// Len returns the number of attached components.
func (s *ComponentStore[T]) Len() int { return len(s.dense) }

// Each iterates every (entity, component) pair in dense order. The fn may
// Set/Remove the iterated kind on the *current* entity (Remove swaps the
// last entry into the freed slot; the loop handles the index shift by
// not advancing when that happens).
//
// Set/Remove on OTHER entities of the same kind during iteration is
// undefined behavior — mutations swap-with-last and would alias indices.
// Use a deferred mutation queue if you need that.
func (s *ComponentStore[T]) Each(fn func(e EntityID, v *T)) {
	i := 0
	for i < len(s.dense) {
		e := s.owners[i]
		fn(e, &s.dense[i])
		if i < len(s.dense) && s.owners[i] == e {
			i++ // entity unchanged at slot i; advance
		}
		// else: fn removed e and the swap put a different entity at slot i;
		// re-process slot i with the new occupant.
	}
}

// Owners returns the dense owner slice for callers that need direct access
// (collision sweep, AOI broadcast). The returned slice aliases internal
// storage; do not retain it across mutating calls.
func (s *ComponentStore[T]) Owners() []EntityID { return s.owners }

// Dense returns the packed component slice. Same aliasing rules as Owners.
// Index i in Dense corresponds to Owners[i].
func (s *ComponentStore[T]) Dense() []T { return s.dense }

// ensureSparse grows the sparse slice so sparse[idx] is addressable.
// New entries default to -1 (absent).
func (s *ComponentStore[T]) ensureSparse(idx uint32) {
	need := int(idx) + 1
	if cap(s.sparse) < need {
		grown := make([]int32, need, growCap(cap(s.sparse), need))
		copy(grown, s.sparse)
		for i := len(s.sparse); i < need; i++ {
			grown[i] = -1
		}
		s.sparse = grown
		return
	}
	if len(s.sparse) < need {
		old := len(s.sparse)
		s.sparse = s.sparse[:need]
		for i := old; i < need; i++ {
			s.sparse[i] = -1
		}
	}
}

// growCap is the standard "double until 1024, then +25%" pattern Go's
// runtime uses. Inlined here so growth is deterministic at every size.
func growCap(have, need int) int {
	if have == 0 {
		return need
	}
	for have < need {
		if have < 1024 {
			have *= 2
		} else {
			have += have / 4
		}
	}
	return have
}
