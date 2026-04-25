package sim_test

import (
	"context"
	"errors"
	"testing"

	"boxland/server/internal/sim"
	"boxland/server/internal/sim/ecs"
)

func TestScheduler_FreezeStepsAreNoOps(t *testing.T) {
	w := ecs.NewWorld()
	sch := sim.NewScheduler(w)
	runs := 0
	sch.Register(sim.SystemEntry{Name: "x", Stage: sim.StageAI,
		Run: func(_ context.Context, _ *ecs.World) error { runs++; return nil },
	})
	if err := sch.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("first step: got %d runs, want 1", runs)
	}
	if sch.Tick() != 1 {
		t.Errorf("tick: got %d, want 1", sch.Tick())
	}

	sch.Freeze()
	if !sch.IsFrozen() {
		t.Fatal("IsFrozen should report true after Freeze")
	}
	for i := 0; i < 3; i++ {
		_ = sch.Step(context.Background())
	}
	if runs != 1 {
		t.Errorf("frozen Step ran the system: runs=%d", runs)
	}
	if sch.Tick() != 1 {
		t.Errorf("frozen Step advanced tick: %d", sch.Tick())
	}

	// StepOnce overrides freeze for frame-by-frame inspection.
	if err := sch.StepOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runs != 2 || sch.Tick() != 2 {
		t.Errorf("StepOnce: got runs=%d tick=%d, want 2/2", runs, sch.Tick())
	}

	sch.Unfreeze()
	if sch.IsFrozen() {
		t.Error("Unfreeze should clear frozen flag")
	}
	_ = sch.Step(context.Background())
	if runs != 3 || sch.Tick() != 3 {
		t.Errorf("post-unfreeze: got runs=%d tick=%d, want 3/3", runs, sch.Tick())
	}
}

func TestScheduler_StepRunsSystemsInStageOrder(t *testing.T) {
	w := ecs.NewWorld()
	sch := sim.NewScheduler(w)

	var order []string
	mk := func(name string, stage sim.Stage) sim.SystemEntry {
		return sim.SystemEntry{Name: name, Stage: stage,
			Run: func(_ context.Context, _ *ecs.World) error {
				order = append(order, name)
				return nil
			},
		}
	}
	sch.Register(mk("audio", sim.StageAudio))
	sch.Register(mk("input", sim.StageInput))
	sch.Register(mk("collision", sim.StageCollision))
	sch.Register(mk("ai", sim.StageAI))
	sch.Register(mk("movement", sim.StageMovement))
	sch.Register(mk("trigger", sim.StageTrigger))
	sch.Register(mk("broadcast", sim.StageBroadcast))

	if err := sch.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"input", "ai", "movement", "collision", "trigger", "audio", "broadcast"}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("step order: got %v, want %v", order, want)
			break
		}
	}
}

func TestScheduler_RegistrationOrderWithinStage(t *testing.T) {
	w := ecs.NewWorld()
	sch := sim.NewScheduler(w)
	var order []string
	for _, name := range []string{"a", "b", "c"} {
		name := name
		sch.Register(sim.SystemEntry{Name: name, Stage: sim.StageMovement,
			Run: func(_ context.Context, _ *ecs.World) error {
				order = append(order, name)
				return nil
			}})
	}
	_ = sch.Step(context.Background())
	for i, exp := range []string{"a", "b", "c"} {
		if order[i] != exp {
			t.Errorf("expected stable order; got %v", order)
			break
		}
	}
}

func TestScheduler_TickCounterIncrementsOnSuccess(t *testing.T) {
	w := ecs.NewWorld()
	sch := sim.NewScheduler(w)
	sch.Register(sim.SystemEntry{Name: "noop", Stage: sim.StageMovement,
		Run: func(_ context.Context, _ *ecs.World) error { return nil }})

	for i := 0; i < 3; i++ {
		_ = sch.Step(context.Background())
	}
	if sch.Tick() != 3 {
		t.Errorf("Tick: got %d, want 3", sch.Tick())
	}
}

func TestScheduler_TickDoesNotIncrementOnError(t *testing.T) {
	w := ecs.NewWorld()
	sch := sim.NewScheduler(w)
	wantErr := errors.New("boom")
	sch.Register(sim.SystemEntry{Name: "fail", Stage: sim.StageMovement,
		Run: func(_ context.Context, _ *ecs.World) error { return wantErr }})

	if err := sch.Step(context.Background()); !errors.Is(err, wantErr) {
		t.Errorf("expected error to propagate; got %v", err)
	}
	if sch.Tick() != 0 {
		t.Errorf("Tick: got %d, want 0 (failed step shouldn't advance)", sch.Tick())
	}
}

func TestScheduler_DuplicateNamePanics(t *testing.T) {
	w := ecs.NewWorld()
	sch := sim.NewScheduler(w)
	sch.Register(sim.SystemEntry{Name: "x", Stage: sim.StageInput,
		Run: func(_ context.Context, _ *ecs.World) error { return nil }})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate name")
		}
	}()
	sch.Register(sim.SystemEntry{Name: "x", Stage: sim.StageMovement,
		Run: func(_ context.Context, _ *ecs.World) error { return nil }})
}

func TestScheduler_StopsOnContextCancel(t *testing.T) {
	w := ecs.NewWorld()
	sch := sim.NewScheduler(w)
	sch.Register(sim.SystemEntry{Name: "noop", Stage: sim.StageMovement,
		Run: func(_ context.Context, _ *ecs.World) error { return nil }})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sch.Step(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}
