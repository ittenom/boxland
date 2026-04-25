package ecs_test

import (
	"errors"
	"testing"

	"boxland/server/internal/entities/components"
	"boxland/server/internal/sim/ecs"
)

func TestWorld_SpawnDespawnAlive(t *testing.T) {
	w := ecs.NewWorld()
	a := w.Spawn()
	if !a.IsValid() {
		t.Error("Spawn returned invalid id")
	}
	if !w.Alive(a) {
		t.Error("freshly spawned entity should be Alive")
	}
	if w.Count() != 1 {
		t.Errorf("Count: got %d, want 1", w.Count())
	}

	if err := w.Despawn(a); err != nil {
		t.Fatalf("Despawn: %v", err)
	}
	if w.Alive(a) {
		t.Error("despawned entity should not be Alive")
	}
	if w.Count() != 0 {
		t.Errorf("Count after Despawn: got %d", w.Count())
	}
}

func TestWorld_DespawnUnknownReturnsError(t *testing.T) {
	w := ecs.NewWorld()
	garbage := ecs.MakeEntityID(99, 0)
	err := w.Despawn(garbage)
	if !errors.Is(err, ecs.ErrUnknownEntity) {
		t.Errorf("got %v, want ErrUnknownEntity", err)
	}
}

func TestWorld_DespawnTwiceReturnsError(t *testing.T) {
	w := ecs.NewWorld()
	a := w.Spawn()
	_ = w.Despawn(a)
	if err := w.Despawn(a); !errors.Is(err, ecs.ErrUnknownEntity) {
		t.Errorf("second Despawn: got %v, want ErrUnknownEntity", err)
	}
}

func TestWorld_SlotIsRecycledWithBumpedGeneration(t *testing.T) {
	w := ecs.NewWorld()
	a := w.Spawn()
	_ = w.Despawn(a)
	b := w.Spawn()

	if a.Index() != b.Index() {
		t.Errorf("expected slot reuse: a.Index=%d b.Index=%d", a.Index(), b.Index())
	}
	if a.Generation() == b.Generation() {
		t.Error("recycled slot should bump generation")
	}
	if w.Alive(a) {
		t.Error("stale reference to recycled slot should be dead")
	}
	if !w.Alive(b) {
		t.Error("new entity at recycled slot should be alive")
	}
}

func TestWorld_ManySpawnsKeepsCountAccurate(t *testing.T) {
	w := ecs.NewWorld()
	const n = 1000
	es := make([]ecs.EntityID, n)
	for i := range es {
		es[i] = w.Spawn()
	}
	if w.Count() != n {
		t.Errorf("Count after spawn: got %d, want %d", w.Count(), n)
	}
	for _, e := range es[:n/2] {
		_ = w.Despawn(e)
	}
	if w.Count() != n/2 {
		t.Errorf("Count after partial despawn: got %d, want %d", w.Count(), n/2)
	}
}

func TestEntityID_Layout(t *testing.T) {
	e := ecs.MakeEntityID(42, 7)
	if e.Index() != 42 || e.Generation() != 7 {
		t.Errorf("round-trip: got (%d, %d)", e.Index(), e.Generation())
	}
}

func TestWorld_DespawnRemovesAllComponents(t *testing.T) {
	w := ecs.NewWorld()
	stores := w.Stores()

	a := w.Spawn()
	stores.Position.Set(a, components.Position{X: 1, Y: 2})
	stores.Velocity.Set(a, components.Velocity{VX: 3})
	stores.Sprite.Set(a, components.Sprite{AssetID: 7})

	if !stores.Position.Has(a) || !stores.Velocity.Has(a) || !stores.Sprite.Has(a) {
		t.Fatal("setup failed: components not attached")
	}
	if err := w.Despawn(a); err != nil {
		t.Fatal(err)
	}
	if stores.Position.Has(a) || stores.Velocity.Has(a) || stores.Sprite.Has(a) {
		t.Errorf("Despawn should have removed every component")
	}
}

func TestEntityID_PanicsOnOversizeIndex(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for oversized index")
		}
	}()
	ecs.MakeEntityID(1<<25, 0)
}
