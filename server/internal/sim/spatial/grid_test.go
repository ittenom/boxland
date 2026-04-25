package spatial_test

import (
	"sort"
	"testing"

	"boxland/server/internal/sim/ecs"
	"boxland/server/internal/sim/spatial"
)

// chunkSpan is the world-pixel side length of one chunk
// (matches spatial.ChunkTiles * spatial.ChunkPxPerTile).
const chunkSpan = spatial.ChunkTiles * spatial.ChunkPxPerTile

func TestChunkOf_OriginAndSimpleCases(t *testing.T) {
	cases := []struct {
		px, py int32
		cx, cy int32
	}{
		{0, 0, 0, 0},
		{chunkSpan - 1, chunkSpan - 1, 0, 0},
		{chunkSpan, 0, 1, 0},
		{0, chunkSpan, 0, 1},
		{-1, -1, -1, -1},                   // floor div pulls to -1
		{-chunkSpan, -chunkSpan, -1, -1},
		{-chunkSpan - 1, 0, -2, 0},
	}
	for _, c := range cases {
		got := spatial.ChunkOf(c.px, c.py)
		gx, gy := got.Coords()
		if gx != c.cx || gy != c.cy {
			t.Errorf("ChunkOf(%d, %d): got (%d, %d), want (%d, %d)",
				c.px, c.py, gx, gy, c.cx, c.cy)
		}
	}
}

func TestMakeChunkID_RoundTrip(t *testing.T) {
	cases := [][2]int32{{0, 0}, {-1, -1}, {99999, -99999}, {-2147483648, 2147483647}}
	for _, c := range cases {
		id := spatial.MakeChunkID(c[0], c[1])
		gx, gy := id.Coords()
		if gx != c[0] || gy != c[1] {
			t.Errorf("round-trip (%d, %d) -> (%d, %d)", c[0], c[1], gx, gy)
		}
	}
}

func TestGrid_AddQueryRemove(t *testing.T) {
	g := spatial.New()
	w := ecs.NewWorld()
	a, b := w.Spawn(), w.Spawn()

	g.Add(a, 100, 100)  // chunk (0, 0)
	g.Add(b, 600, 600)  // chunk (1, 1) given chunkSpan = 512

	c0 := spatial.MakeChunkID(0, 0)
	c1 := spatial.MakeChunkID(1, 1)
	if got := g.QueryChunk(c0); len(got) != 1 || got[0] != a {
		t.Errorf("chunk (0,0): got %v, want [a]", got)
	}
	if got := g.QueryChunk(c1); len(got) != 1 || got[0] != b {
		t.Errorf("chunk (1,1): got %v, want [b]", got)
	}

	g.Remove(a)
	if got := g.QueryChunk(c0); len(got) != 0 {
		t.Errorf("chunk (0,0) after remove: got %v", got)
	}
}

func TestGrid_MoveAcrossChunkBoundary(t *testing.T) {
	g := spatial.New()
	w := ecs.NewWorld()
	a := w.Spawn()

	g.Add(a, 10, 10)
	first, _ := g.HomeOf(a)
	g.Move(a, chunkSpan+5, chunkSpan+5)
	second, _ := g.HomeOf(a)

	if first == second {
		t.Errorf("move across boundary should change chunk; both = %v", first)
	}
	if got := g.QueryChunk(first); len(got) != 0 {
		t.Errorf("origin chunk should be empty after move")
	}
	if got := g.QueryChunk(second); len(got) != 1 || got[0] != a {
		t.Errorf("destination chunk: got %v", got)
	}
}

func TestGrid_QueryRange(t *testing.T) {
	g := spatial.New()
	w := ecs.NewWorld()

	// Spawn 4 entities in known chunks.
	a := w.Spawn(); g.Add(a, 0, 0)         // (0,0)
	b := w.Spawn(); g.Add(b, chunkSpan, 0) // (1,0)
	c := w.Spawn(); g.Add(c, 0, chunkSpan) // (0,1)
	d := w.Spawn(); g.Add(d, chunkSpan*3, chunkSpan*3) // (3,3) — outside our query range

	// Query the 2x2 chunk square covering (0,0)..(1,1).
	got := g.QueryRange(0, 0, 2*chunkSpan-1, 2*chunkSpan-1)
	want := []ecs.EntityID{a, b, c}
	sort.Slice(got,  func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(got) != len(want) {
		t.Fatalf("QueryRange: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("QueryRange[%d]: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestGrid_VersionBumpsOnMutation(t *testing.T) {
	g := spatial.New()
	w := ecs.NewWorld()
	c0 := spatial.MakeChunkID(0, 0)

	v0 := g.Version(c0)
	a := w.Spawn()
	g.Add(a, 10, 10)
	v1 := g.Version(c0)
	if v1 <= v0 {
		t.Errorf("Add should bump version: %d -> %d", v0, v1)
	}

	g.Move(a, 20, 20) // same chunk: no version bump
	v2 := g.Version(c0)
	if v2 != v1 {
		t.Errorf("Move within chunk should not bump: %d -> %d", v1, v2)
	}

	g.Remove(a)
	v3 := g.Version(c0)
	if v3 <= v2 {
		t.Errorf("Remove should bump version: %d -> %d", v2, v3)
	}
}

func TestGrid_Stats(t *testing.T) {
	g := spatial.New()
	w := ecs.NewWorld()
	for i := 0; i < 100; i++ {
		g.Add(w.Spawn(), int32(i*10), 0)
	}
	s := g.Stats()
	if s.Entities != 100 {
		t.Errorf("Stats.Entities: got %d, want 100", s.Entities)
	}
	if s.Chunks < 1 {
		t.Errorf("Stats.Chunks: got %d, expected at least 1", s.Chunks)
	}
}
