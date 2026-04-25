// Package systems holds the canonical built-in systems registered with
// the per-instance Scheduler. Each system is one file so the per-system
// blast radius stays small.
package systems

import (
	"context"

	"boxland/server/internal/entities/components"
	"boxland/server/internal/sim"
	"boxland/server/internal/sim/ecs"
)

// Movement is the per-tick "advance Position by Velocity" system, with
// the canonical "skip entities owning Static" optimization (PLAN.md §4h).
//
// Velocity is applied uncorrected here; collision (a separate stage) is
// what ultimately clips the resolved delta. This keeps the system loops
// composable: callers can toggle collision on/off per surface (e.g.
// designer godmode -> no collision) without rewriting the movement code.
var Movement = sim.SystemEntry{
	Name:  "movement",
	Stage: sim.StageMovement,
	Run: func(_ context.Context, w *ecs.World) error {
		stores := w.Stores()
		stores.Velocity.Each(func(e ecs.EntityID, v *components.Velocity) {
			if stores.Static.Has(e) {
				// Tile entities and other immovables skip movement entirely.
				// They might still own Velocity (e.g. inherited from an
				// archetype) but the system ignores it.
				return
			}
			pos := stores.Position.GetPtr(e)
			if pos == nil {
				return
			}
			vx, vy := v.VX, v.VY
			if v.MaxSpeed > 0 {
				vx = capMagnitude(vx, v.MaxSpeed)
				vy = capMagnitude(vy, v.MaxSpeed)
			}
			pos.X += vx
			pos.Y += vy
		})
		return nil
	},
}

// capMagnitude clips v so |v| <= cap. Component-wise rather than vector
// magnitude so VX and VY get individual caps (cheaper, matches "you can
// move max N px/tick on each axis" mental model).
func capMagnitude(v, cap int32) int32 {
	if v > cap {
		return cap
	}
	if v < -cap {
		return -cap
	}
	return v
}
