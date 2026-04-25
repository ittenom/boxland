package runtime_test

import (
	"testing"

	"boxland/server/internal/sim/runtime"
)

func TestMapInstance_HotSwapQueueAndDrain(t *testing.T) {
	mi := &runtime.MapInstance{}
	if mi.PendingHotSwapCount() != 0 {
		t.Fatal("fresh instance should have 0 pending")
	}
	mi.QueueHotSwap(runtime.HotSwap{EntityTypeID: 1})
	mi.QueueHotSwap(runtime.HotSwap{EntityTypeID: 2,
		RemovedComponentKinds: []string{"old_component"},
	})
	if mi.PendingHotSwapCount() != 2 {
		t.Errorf("pending count: got %d, want 2", mi.PendingHotSwapCount())
	}
	applied := mi.DrainHotSwaps()
	if applied != 2 {
		t.Errorf("drain count: got %d, want 2", applied)
	}
	if mi.PendingHotSwapCount() != 0 {
		t.Errorf("after drain: got %d, want 0", mi.PendingHotSwapCount())
	}
	// Subsequent drain is a no-op.
	if mi.DrainHotSwaps() != 0 {
		t.Error("second drain should return 0")
	}
}
