package ecs_test

import (
	"sort"
	"testing"

	"boxland/server/internal/sim/ecs"
)

type Pos struct{ X, Y int32 }

func TestStore_SetGetHasRemove(t *testing.T) {
	s := ecs.NewComponentStore[Pos](16)
	w := ecs.NewWorld()
	a, b := w.Spawn(), w.Spawn()

	s.Set(a, Pos{X: 10, Y: 20})
	s.Set(b, Pos{X: 30, Y: 40})

	if !s.Has(a) || !s.Has(b) {
		t.Errorf("Has should be true after Set")
	}
	if v, ok := s.Get(a); !ok || v != (Pos{10, 20}) {
		t.Errorf("Get(a): got %v, %v", v, ok)
	}
	if s.Len() != 2 {
		t.Errorf("Len: got %d, want 2", s.Len())
	}

	s.Remove(a)
	if s.Has(a) {
		t.Errorf("Has(a) after Remove should be false")
	}
	if !s.Has(b) {
		t.Errorf("b should still be present after a's removal")
	}
	if s.Len() != 1 {
		t.Errorf("Len: got %d, want 1", s.Len())
	}
}

func TestStore_OverwriteIsInPlace(t *testing.T) {
	s := ecs.NewComponentStore[Pos](4)
	w := ecs.NewWorld()
	a := w.Spawn()

	s.Set(a, Pos{X: 1})
	s.Set(a, Pos{X: 2})
	if s.Len() != 1 {
		t.Errorf("overwrite should not grow dense; got %d", s.Len())
	}
	v, _ := s.Get(a)
	if v.X != 2 {
		t.Errorf("overwrite: got %v", v)
	}
}

func TestStore_GetPtrMutates(t *testing.T) {
	s := ecs.NewComponentStore[Pos](4)
	w := ecs.NewWorld()
	a := w.Spawn()
	s.Set(a, Pos{X: 5})

	p := s.GetPtr(a)
	if p == nil {
		t.Fatal("GetPtr returned nil for present entity")
	}
	p.Y = 99
	v, _ := s.Get(a)
	if v.Y != 99 {
		t.Errorf("GetPtr mutation didn't stick; got %v", v)
	}
}

func TestStore_RemoveSwapsInLast(t *testing.T) {
	s := ecs.NewComponentStore[Pos](4)
	w := ecs.NewWorld()

	es := make([]ecs.EntityID, 4)
	for i := range es {
		es[i] = w.Spawn()
		s.Set(es[i], Pos{X: int32(i)})
	}
	// Remove the middle one; the last entity should now occupy its slot.
	s.Remove(es[1])

	if s.Has(es[1]) {
		t.Error("removed entity should not be present")
	}
	for _, e := range []ecs.EntityID{es[0], es[2], es[3]} {
		if !s.Has(e) {
			t.Errorf("untouched entity %v lost", e)
		}
	}
	if s.Len() != 3 {
		t.Errorf("Len: got %d, want 3", s.Len())
	}
}

func TestStore_EachVisitsAll(t *testing.T) {
	s := ecs.NewComponentStore[Pos](16)
	w := ecs.NewWorld()
	want := make(map[ecs.EntityID]int32)
	for i := 0; i < 10; i++ {
		e := w.Spawn()
		s.Set(e, Pos{X: int32(i * 7)})
		want[e] = int32(i * 7)
	}

	got := make(map[ecs.EntityID]int32)
	s.Each(func(e ecs.EntityID, v *Pos) {
		got[e] = v.X
	})
	if len(got) != len(want) {
		t.Errorf("Each visited %d, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("missing or wrong value for %v: got %d want %d", k, got[k], v)
		}
	}
}

func TestStore_EachAllowsCurrentEntityRemoval(t *testing.T) {
	s := ecs.NewComponentStore[Pos](16)
	w := ecs.NewWorld()
	es := make([]ecs.EntityID, 6)
	for i := range es {
		es[i] = w.Spawn()
		s.Set(es[i], Pos{X: int32(i)})
	}

	// Remove the entity whose X is even, mid-iteration.
	visited := make(map[ecs.EntityID]bool)
	s.Each(func(e ecs.EntityID, v *Pos) {
		visited[e] = true
		if v.X%2 == 0 {
			s.Remove(e)
		}
	})

	// Each surviving entity has odd X.
	for i := 0; i < 6; i++ {
		if i%2 == 0 && s.Has(es[i]) {
			t.Errorf("e[%d] should have been removed", i)
		}
		if i%2 == 1 && !s.Has(es[i]) {
			t.Errorf("e[%d] should have survived", i)
		}
	}
	// Every entity was visited at least once.
	for i, e := range es {
		if !visited[e] {
			t.Errorf("e[%d] never visited", i)
		}
	}
}

func TestStore_ReturnsZeroForUnknown(t *testing.T) {
	s := ecs.NewComponentStore[Pos](4)
	w := ecs.NewWorld()
	a := w.Spawn()

	if _, ok := s.Get(a); ok {
		t.Error("Get on never-set entity should return ok=false")
	}
	if s.Has(a) {
		t.Error("Has on never-set entity should be false")
	}
	if s.GetPtr(a) != nil {
		t.Error("GetPtr should be nil")
	}
}

func TestStore_DenseAndOwnersAlign(t *testing.T) {
	s := ecs.NewComponentStore[Pos](8)
	w := ecs.NewWorld()
	for i := 0; i < 5; i++ {
		s.Set(w.Spawn(), Pos{X: int32(i)})
	}
	owners := s.Owners()
	dense := s.Dense()
	if len(owners) != len(dense) {
		t.Fatalf("Owners len %d != Dense len %d", len(owners), len(dense))
	}
	// Owners are insertion-order; Dense values match.
	xs := make([]int, len(dense))
	for i, p := range dense {
		xs[i] = int(p.X)
	}
	sort.Ints(xs)
	for i, x := range xs {
		if x != i {
			t.Errorf("dense order broken: %v", xs)
		}
	}
}
