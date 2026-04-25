package ecs_test

import (
	"testing"
	"time"

	"boxland/server/internal/entities/components"
	"boxland/server/internal/sim/ecs"
)

// nowNS returns the current monotonic-ish time in nanoseconds. Wrapping
// time.Now().UnixNano() rather than direct use keeps the benchmark code
// readable and central if we ever switch to a higher-resolution source.
func nowNS() int64 { return time.Now().UnixNano() }

// makeWorldNEntities spawns N entities with Position + Velocity components
// and returns the world. Used by the benchmark and the gating test.
func makeWorldNEntities(n int) *ecs.World {
	w := ecs.NewWorld()
	w.SetStores(ecs.NewStores(n))
	stores := w.Stores()
	for i := 0; i < n; i++ {
		e := w.Spawn()
		stores.Position.Set(e, components.Position{X: int32(i), Y: int32(i)})
		stores.Velocity.Set(e, components.Velocity{VX: 16, VY: -8})
	}
	return w
}

// systemMove is the canonical "advance every Position by its Velocity"
// system. Tightest possible loop: walks the dense Velocity slice and
// applies into the Position dense slice via the parallel sparse mapping.
func systemMove(w *ecs.World) {
	stores := w.Stores()
	stores.Velocity.Each(func(e ecs.EntityID, v *components.Velocity) {
		p := stores.Position.GetPtr(e)
		if p == nil {
			return
		}
		p.X += v.VX
		p.Y += v.VY
	})
}

// BenchmarkTick_10kEntities reports the wall-clock time for one tick over
// 10 000 entities. Run with `just bench`; the gating test below converts
// the benchmark into a hard ceiling that fails CI on regression.
func BenchmarkTick_10kEntities(b *testing.B) {
	w := makeWorldNEntities(10_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		systemMove(w)
	}
}

// TestPerf_10kEntitiesTickUnder1ms is the regression gate. We can't
// measure a single tick reliably on Windows (clock resolution ~100 ns to
// 1 µs), so we batch 100 ticks and divide. The 1 ms-per-tick budget
// becomes a 100 ms-per-batch ceiling.
//
// Skipped when -short to keep contributors' fast feedback loops clean.
func TestPerf_10kEntitiesTickUnder1ms(t *testing.T) {
	if testing.Short() {
		t.Skip("perf gate skipped under -short")
	}
	const (
		entities      = 10_000
		ticksPerBatch = 100
		limitNS       = 1_000_000 // 1 ms per tick
	)
	w := makeWorldNEntities(entities)

	// Warm up.
	for i := 0; i < 3; i++ {
		systemMove(w)
	}

	// Median over 5 batches.
	const runs = 5
	samples := make([]int64, runs)
	for r := 0; r < runs; r++ {
		t0 := nowNS()
		for i := 0; i < ticksPerBatch; i++ {
			systemMove(w)
		}
		samples[r] = (nowNS() - t0) / ticksPerBatch
	}
	for i := 0; i < len(samples); i++ {
		for j := i + 1; j < len(samples); j++ {
			if samples[j] < samples[i] {
				samples[i], samples[j] = samples[j], samples[i]
			}
		}
	}
	median := samples[runs/2]
	t.Logf("10k-entity Position+Velocity tick median: %d ns (limit %d ns); samples=%v", median, limitNS, samples)
	if median > limitNS {
		t.Errorf("perf regression: median %d ns exceeds %d ns limit", median, limitNS)
	}
}
