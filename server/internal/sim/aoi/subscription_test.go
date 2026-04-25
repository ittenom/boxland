package aoi_test

import (
	"sort"
	"testing"

	"boxland/server/internal/sim/aoi"
	"boxland/server/internal/sim/ecs"
	"boxland/server/internal/sim/spatial"
)

const chunkSpan = spatial.ChunkTiles * spatial.ChunkPxPerTile

func TestVisibleChunks_RadiusZeroIsFocusOnly(t *testing.T) {
	s := aoi.NewSubscription(1, aoi.PolicyPlayer, spatial.MakeChunkID(0, 0), 0)
	got := s.VisibleChunks()
	if len(got) != 1 {
		t.Errorf("radius 0: got %d, want 1", len(got))
	}
	if got[0] != spatial.MakeChunkID(0, 0) {
		t.Errorf("got %v, want focus", got)
	}
}

func TestVisibleChunks_RadiusOneIs3x3(t *testing.T) {
	s := aoi.NewSubscription(1, aoi.PolicyPlayer, spatial.MakeChunkID(5, 5), 1)
	got := s.VisibleChunks()
	if len(got) != 9 {
		t.Errorf("radius 1: got %d, want 9", len(got))
	}
	for _, c := range got {
		cx, cy := c.Coords()
		if cx < 4 || cx > 6 || cy < 4 || cy > 6 {
			t.Errorf("chunk %d, %d outside expected 3x3 around (5,5)", cx, cy)
		}
	}
}

func TestDirtyChunks_FreshSubscriberSeesEveryVisibleChunk(t *testing.T) {
	g := spatial.New()
	w := ecs.NewWorld()
	// Spawn at least one entity in each visible chunk so Version > 0.
	for cy := -1; cy <= 1; cy++ {
		for cx := -1; cx <= 1; cx++ {
			g.Add(w.Spawn(), int32(cx)*chunkSpan+10, int32(cy)*chunkSpan+10)
		}
	}
	s := aoi.NewSubscription(1, aoi.PolicyPlayer, spatial.MakeChunkID(0, 0), 1)
	dirty := s.DirtyChunks(g)
	if len(dirty) != 9 {
		t.Errorf("fresh sub: got %d dirty chunks, want 9", len(dirty))
	}
}

func TestDirtyChunks_AckedChunksDropFromList(t *testing.T) {
	g := spatial.New()
	w := ecs.NewWorld()
	g.Add(w.Spawn(), 10, 10) // (0,0)
	g.Add(w.Spawn(), chunkSpan+10, 10) // (1,0)
	s := aoi.NewSubscription(1, aoi.PolicyPlayer, spatial.MakeChunkID(0, 0), 1)

	s.AckAll(g)
	if dirty := s.DirtyChunks(g); len(dirty) != 0 {
		t.Errorf("after AckAll, expected no dirty; got %v", dirty)
	}

	// Mutate a chunk; it should re-appear as dirty.
	g.Add(w.Spawn(), 50, 50) // bumps version of (0,0)
	dirty := s.DirtyChunks(g)
	if len(dirty) != 1 || dirty[0] != spatial.MakeChunkID(0, 0) {
		t.Errorf("after mutation, expected only (0,0) dirty; got %v", dirty)
	}
}

func TestDirtyChunks_PartialAckLeavesUnackedDirty(t *testing.T) {
	g := spatial.New()
	w := ecs.NewWorld()
	g.Add(w.Spawn(), 10, 10)         // (0,0)
	g.Add(w.Spawn(), chunkSpan+5, 5) // (1,0)
	s := aoi.NewSubscription(1, aoi.PolicyPlayer, spatial.MakeChunkID(0, 0), 1)

	s.Ack(spatial.MakeChunkID(0, 0), g.Version(spatial.MakeChunkID(0, 0)))
	dirty := s.DirtyChunks(g)
	// (0,0) is acked; (1,0) and 7 neighbours that exist should still be dirty.
	for _, c := range dirty {
		if c == spatial.MakeChunkID(0, 0) {
			t.Errorf("acked chunk (0,0) still in dirty list: %v", dirty)
		}
	}
}

func TestSetFocus_ChangesVisibleSet(t *testing.T) {
	s := aoi.NewSubscription(1, aoi.PolicyPlayer, spatial.MakeChunkID(0, 0), 0)
	if c := s.VisibleChunks()[0]; c != spatial.MakeChunkID(0, 0) {
		t.Errorf("got %v", c)
	}
	s.SetFocus(spatial.MakeChunkID(7, 9))
	if c := s.VisibleChunks()[0]; c != spatial.MakeChunkID(7, 9) {
		t.Errorf("got %v after SetFocus", c)
	}
}

func TestReset_ForcesRedeliveryOfEverything(t *testing.T) {
	g := spatial.New()
	w := ecs.NewWorld()
	g.Add(w.Spawn(), 10, 10)
	s := aoi.NewSubscription(1, aoi.PolicyPlayer, spatial.MakeChunkID(0, 0), 1)
	s.AckAll(g)
	if dirty := s.DirtyChunks(g); len(dirty) != 0 {
		t.Fatalf("after AckAll, expected 0 dirty; got %v", dirty)
	}
	s.Reset()
	dirty := s.DirtyChunks(g)
	if len(dirty) == 0 {
		t.Errorf("after Reset, expected dirty chunks; got 0")
	}
}

func TestForgetChunk_OnlyAffectsThatChunk(t *testing.T) {
	g := spatial.New()
	w := ecs.NewWorld()
	g.Add(w.Spawn(), 10, 10)         // (0,0)
	g.Add(w.Spawn(), chunkSpan+5, 5) // (1,0)
	s := aoi.NewSubscription(1, aoi.PolicyPlayer, spatial.MakeChunkID(0, 0), 1)
	s.AckAll(g)

	target := spatial.MakeChunkID(1, 0)
	s.ForgetChunk(target)
	dirty := s.DirtyChunks(g)
	sort.Slice(dirty, func(i, j int) bool { return dirty[i] < dirty[j] })
	if len(dirty) != 1 || dirty[0] != target {
		t.Errorf("after ForgetChunk(%v), expected only that chunk dirty; got %v", target, dirty)
	}
}
