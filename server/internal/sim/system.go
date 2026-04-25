// Package sim is the game-loop runtime: tick scheduler, the canonical
// system order, and (later) the AOI broadcaster + WAL flush. The actual
// component storage lives in internal/sim/ecs.
//
// Per PLAN.md §4h, systems run in this order each tick:
//
//   input  -> AI  -> movement  -> collision  -> triggers  -> audio  -> AOI
//
// Each system is a pure func(world, ctx) — no goroutines, no I/O. The
// scheduler enforces order; system code never refers to another system
// directly.
package sim

import (
	"context"
	"fmt"

	"boxland/server/internal/sim/ecs"
)

// Stage is the position in the canonical pipeline. Lower numbers run first.
type Stage uint8

const (
	StageInput     Stage = 10
	StageAI        Stage = 20
	StageMovement  Stage = 30
	StageCollision Stage = 40
	StageTrigger   Stage = 50
	StageAudio     Stage = 60
	StageBroadcast Stage = 70
)

// System is the per-tick callable. ctx is cancellable so a long-running
// system can be aborted on shutdown; world is the entity store.
type System func(ctx context.Context, w *ecs.World) error

// SystemEntry is one registered system + the metadata the scheduler needs
// to order it.
type SystemEntry struct {
	Name  string
	Stage Stage
	Run   System
}

// Scheduler runs registered systems in canonical order. Construct one per
// World; Step advances the world by one tick.
type Scheduler struct {
	world   *ecs.World
	entries []SystemEntry
	tick    uint64
	frozen  bool
}

// NewScheduler returns a Scheduler bound to a world.
func NewScheduler(w *ecs.World) *Scheduler {
	return &Scheduler{world: w}
}

// Register attaches a system. Order within a stage is registration order;
// stage order is fixed by the Stage constant. Panics on duplicate names.
func (s *Scheduler) Register(entry SystemEntry) {
	if entry.Name == "" {
		panic("sim.Scheduler: SystemEntry.Name required")
	}
	if entry.Run == nil {
		panic(fmt.Sprintf("sim.Scheduler: SystemEntry.Run required (system %q)", entry.Name))
	}
	for _, e := range s.entries {
		if e.Name == entry.Name {
			panic(fmt.Sprintf("sim.Scheduler: duplicate system name %q", entry.Name))
		}
	}
	s.entries = append(s.entries, entry)
	s.sortEntries()
}

// Tick reports the number of completed ticks. Useful for AOI subscription
// version vectors and the WAL.
func (s *Scheduler) Tick() uint64 { return s.tick }

// Systems returns the canonical-order system list. Mostly for debug logs.
func (s *Scheduler) Systems() []SystemEntry {
	out := make([]SystemEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Step runs every registered system once, in canonical order. Returns the
// first error any system reports; remaining systems are skipped so a
// failure surfaces immediately rather than compounding.
//
// When the scheduler is frozen (Sandbox FreezeTick designer-opcode),
// Step is a no-op AND the tick counter does NOT advance. This keeps
// the WAL + AOI version vectors stable while a designer pokes around;
// StepTick advances exactly one tick regardless of freeze state for
// frame-by-frame inspection.
func (s *Scheduler) Step(ctx context.Context) error {
	if s.frozen {
		return nil
	}
	return s.runOneTick(ctx)
}

// StepOnce runs exactly one tick even when frozen. Used by the
// StepTick designer opcode for frame-by-frame debugging.
func (s *Scheduler) StepOnce(ctx context.Context) error {
	return s.runOneTick(ctx)
}

func (s *Scheduler) runOneTick(ctx context.Context) error {
	for _, e := range s.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.Run(ctx, s.world); err != nil {
			return fmt.Errorf("system %q: %w", e.Name, err)
		}
	}
	s.tick++
	return nil
}

// Freeze pauses the scheduler. Idempotent.
func (s *Scheduler) Freeze() { s.frozen = true }

// Unfreeze resumes the scheduler. Idempotent.
func (s *Scheduler) Unfreeze() { s.frozen = false }

// IsFrozen reports the current freeze state.
func (s *Scheduler) IsFrozen() bool { return s.frozen }

// sortEntries is an insertion sort so the order within a stage matches
// registration order (stable sort property). With single-digit system
// counts the algorithm choice is irrelevant; correctness matters more.
func (s *Scheduler) sortEntries() {
	for i := 1; i < len(s.entries); i++ {
		j := i
		for j > 0 && s.entries[j-1].Stage > s.entries[j].Stage {
			s.entries[j-1], s.entries[j] = s.entries[j], s.entries[j-1]
			j--
		}
	}
}
