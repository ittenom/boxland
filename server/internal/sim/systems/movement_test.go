package systems_test

import (
	"context"
	"testing"

	"boxland/server/internal/entities/components"
	"boxland/server/internal/sim"
	"boxland/server/internal/sim/ecs"
	"boxland/server/internal/sim/systems"
)

func TestMovement_SkipsStaticEntities(t *testing.T) {
	w := ecs.NewWorld()
	stores := w.Stores()
	sch := sim.NewScheduler(w)
	sch.Register(systems.Movement)

	mover := w.Spawn()
	stores.Position.Set(mover, components.Position{X: 0, Y: 0})
	stores.Velocity.Set(mover, components.Velocity{VX: 100, VY: 50})

	tile := w.Spawn()
	stores.Position.Set(tile, components.Position{X: 0, Y: 0})
	// Tile entities sometimes also own Velocity (inherited from archetype);
	// the Static tag keeps them pinned regardless.
	stores.Velocity.Set(tile, components.Velocity{VX: 999, VY: 999})
	stores.Static.Set(tile, components.Static{})

	if err := sch.Step(context.Background()); err != nil {
		t.Fatal(err)
	}

	mp, _ := stores.Position.Get(mover)
	if mp.X != 100 || mp.Y != 50 {
		t.Errorf("mover should have advanced by velocity; got %+v", mp)
	}
	tp, _ := stores.Position.Get(tile)
	if tp.X != 0 || tp.Y != 0 {
		t.Errorf("static tile should NOT have moved; got %+v", tp)
	}
}

func TestMovement_RespectsMaxSpeed(t *testing.T) {
	w := ecs.NewWorld()
	stores := w.Stores()
	sch := sim.NewScheduler(w)
	sch.Register(systems.Movement)

	e := w.Spawn()
	stores.Position.Set(e, components.Position{})
	stores.Velocity.Set(e, components.Velocity{VX: 5000, VY: -5000, MaxSpeed: 100})

	_ = sch.Step(context.Background())
	got, _ := stores.Position.Get(e)
	if got.X != 100 || got.Y != -100 {
		t.Errorf("MaxSpeed clamp wrong: got %+v", got)
	}
}

func TestMovement_ZeroVelocityIsNoOp(t *testing.T) {
	w := ecs.NewWorld()
	stores := w.Stores()
	sch := sim.NewScheduler(w)
	sch.Register(systems.Movement)

	e := w.Spawn()
	stores.Position.Set(e, components.Position{X: 50, Y: 50})
	stores.Velocity.Set(e, components.Velocity{})

	_ = sch.Step(context.Background())
	got, _ := stores.Position.Get(e)
	if got.X != 50 || got.Y != 50 {
		t.Errorf("zero velocity should not move; got %+v", got)
	}
}

func TestMovement_HandlesMissingPositionGracefully(t *testing.T) {
	// An entity with Velocity but no Position should be silently ignored,
	// not crash.
	w := ecs.NewWorld()
	stores := w.Stores()
	sch := sim.NewScheduler(w)
	sch.Register(systems.Movement)

	e := w.Spawn()
	stores.Velocity.Set(e, components.Velocity{VX: 99})

	if err := sch.Step(context.Background()); err != nil {
		t.Errorf("Step should not error on Velocity-without-Position; got %v", err)
	}
}
