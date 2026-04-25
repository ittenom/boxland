package wfc

import (
	"errors"
	"reflect"
	"testing"
)

// twoTileCheckerTileSet builds a tileset that *forces* a checkerboard
// pattern. Each tile's edge socket points at the OTHER tile's matching
// edge, so A is only compatible with B (and vice versa) — no self-neighbor
// is legal.
//
// Tile A: N=1, E=1, S=2, W=2  (north/east edges expect B's south/west=1)
// Tile B: N=2, E=2, S=1, W=1
// A.east(1) == B.west(1) ✓
// A.south(2) == B.north(2) ✓
// A.east(1) == A.west(2) ✗ (no AA horizontal)
// A.south(2) == A.north(1) ✗ (no AA vertical)
func twoTileCheckerTileSet() *TileSet {
	return NewTileSet([]Tile{
		{EntityType: 10, Sockets: [4]SocketID{1, 1, 2, 2}, Weight: 1},
		{EntityType: 20, Sockets: [4]SocketID{2, 2, 1, 1}, Weight: 1},
	})
}

// uniformGrassTileSet is one tile that fits anywhere; useful for trivially-
// solvable runs and for asserting determinism is independent of randomness.
func uniformGrassTileSet() *TileSet {
	return NewTileSet([]Tile{
		{EntityType: 1, Sockets: [4]SocketID{0, 0, 0, 0}, Weight: 1},
	})
}

// fourTileTerrainTileSet is a richer set: grass (1/1/1/1), path (2/2/2/2),
// and two grass-path edge transition tiles. Designed so seam-anchor tests
// have multiple legal completions.
func fourTileTerrainTileSet() *TileSet {
	return NewTileSet([]Tile{
		{EntityType: 100, Sockets: [4]SocketID{1, 1, 1, 1}, Weight: 1}, // grass
		{EntityType: 200, Sockets: [4]SocketID{2, 2, 2, 2}, Weight: 1}, // path
		{EntityType: 300, Sockets: [4]SocketID{1, 2, 1, 2}, Weight: 1}, // path E-W through grass
		{EntityType: 400, Sockets: [4]SocketID{2, 1, 2, 1}, Weight: 1}, // path N-S through grass
	})
}

func TestGenerate_RejectsEmptyTileSet(t *testing.T) {
	t.Parallel()
	_, err := Generate(NewTileSet(nil), GenerateOptions{Width: 4, Height: 4, Seed: 1})
	if !errors.Is(err, ErrEmptyTileSet) {
		t.Fatalf("expected ErrEmptyTileSet, got %v", err)
	}
	_, err = Generate(nil, GenerateOptions{Width: 4, Height: 4, Seed: 1})
	if !errors.Is(err, ErrEmptyTileSet) {
		t.Fatalf("expected ErrEmptyTileSet for nil tileset, got %v", err)
	}
}

func TestGenerate_RejectsInvalidDimensions(t *testing.T) {
	t.Parallel()
	ts := uniformGrassTileSet()
	cases := []GenerateOptions{
		{Width: 0, Height: 4, Seed: 1},
		{Width: 4, Height: 0, Seed: 1},
		{Width: -1, Height: 4, Seed: 1},
		{Width: 4, Height: -1, Seed: 1},
	}
	for _, c := range cases {
		if _, err := Generate(ts, c); !errors.Is(err, ErrInvalidRegion) {
			t.Fatalf("expected ErrInvalidRegion for %+v, got %v", c, err)
		}
	}
}

func TestGenerate_DeterministicSameSeed(t *testing.T) {
	t.Parallel()
	ts := fourTileTerrainTileSet()
	opts := GenerateOptions{Width: 8, Height: 8, Seed: 12345}

	r1, err := Generate(ts, opts)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	r2, err := Generate(ts, opts)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !reflect.DeepEqual(r1.Cells, r2.Cells) {
		t.Fatalf("identical seed produced different output:\n  r1=%v\n  r2=%v", r1.Cells, r2.Cells)
	}
}

func TestGenerate_DifferentSeedsDiffer(t *testing.T) {
	t.Parallel()
	ts := fourTileTerrainTileSet()
	r1, err := Generate(ts, GenerateOptions{Width: 8, Height: 8, Seed: 1})
	if err != nil {
		t.Fatalf("seed=1: %v", err)
	}
	r2, err := Generate(ts, GenerateOptions{Width: 8, Height: 8, Seed: 2})
	if err != nil {
		t.Fatalf("seed=2: %v", err)
	}
	// 64 cells across 4 tile types — collision probability of producing the
	// same exact grid by chance is negligible.
	if reflect.DeepEqual(r1.Cells, r2.Cells) {
		t.Fatalf("different seeds produced identical output (probably broken RNG seeding)")
	}
}

func TestGenerate_FillsAllCells(t *testing.T) {
	t.Parallel()
	ts := fourTileTerrainTileSet()
	r, err := Generate(ts, GenerateOptions{Width: 6, Height: 5, Seed: 42})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if int32(len(r.Cells)) != r.Width*r.Height {
		t.Fatalf("expected %d cells, got %d", r.Width*r.Height, len(r.Cells))
	}
	// Row-major: (x, y) at index y*W+x.
	for y := int32(0); y < r.Height; y++ {
		for x := int32(0); x < r.Width; x++ {
			c := r.Cells[y*r.Width+x]
			if c.X != x || c.Y != y {
				t.Fatalf("cell at index %d should be (%d,%d), got (%d,%d)", y*r.Width+x, x, y, c.X, c.Y)
			}
			if c.EntityType == 0 {
				t.Fatalf("cell (%d,%d) was not collapsed", x, y)
			}
		}
	}
}

func TestGenerate_RespectsSocketCompatibility(t *testing.T) {
	t.Parallel()
	ts := fourTileTerrainTileSet()
	r, err := Generate(ts, GenerateOptions{Width: 6, Height: 6, Seed: 7})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Build entity-type -> tile lookup.
	tileByEntity := make(map[EntityTypeID]Tile)
	for i := 0; i < ts.Len(); i++ {
		tileByEntity[ts.Tile(i).EntityType] = ts.Tile(i)
	}
	get := func(x, y int32) (Tile, bool) {
		if x < 0 || y < 0 || x >= r.Width || y >= r.Height {
			return Tile{}, false
		}
		c := r.Cells[y*r.Width+x]
		t, ok := tileByEntity[c.EntityType]
		return t, ok
	}
	for y := int32(0); y < r.Height; y++ {
		for x := int32(0); x < r.Width; x++ {
			here, _ := get(x, y)
			// East neighbour: here.E must equal neighbour.W.
			if n, ok := get(x+1, y); ok && here.Sockets[EdgeE] != n.Sockets[EdgeW] {
				t.Fatalf("E-W mismatch at (%d,%d)/(%d,%d): %d vs %d", x, y, x+1, y, here.Sockets[EdgeE], n.Sockets[EdgeW])
			}
			// South neighbour: here.S must equal neighbour.N.
			if n, ok := get(x, y+1); ok && here.Sockets[EdgeS] != n.Sockets[EdgeN] {
				t.Fatalf("N-S mismatch at (%d,%d)/(%d,%d): %d vs %d", x, y, x, y+1, here.Sockets[EdgeS], n.Sockets[EdgeN])
			}
		}
	}
}

func TestGenerate_ProducesCheckerboardForCheckerSet(t *testing.T) {
	t.Parallel()
	ts := twoTileCheckerTileSet()
	r, err := Generate(ts, GenerateOptions{Width: 4, Height: 4, Seed: 1})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Every cell must alternate. Pick (0,0) and verify the parity rule.
	first := r.Cells[0].EntityType
	for _, c := range r.Cells {
		parity := (c.X + c.Y) % 2
		if parity == 0 && c.EntityType != first {
			t.Fatalf("expected parity-0 cell (%d,%d) to be %d, got %d", c.X, c.Y, first, c.EntityType)
		}
		if parity == 1 && c.EntityType == first {
			t.Fatalf("expected parity-1 cell (%d,%d) to differ from %d", c.X, c.Y, first)
		}
	}
}

func TestGenerate_RespectsAnchors(t *testing.T) {
	t.Parallel()
	ts := fourTileTerrainTileSet()
	anchors := Anchors{Cells: []Cell{
		{X: 0, Y: 0, EntityType: 100},
		{X: 3, Y: 3, EntityType: 200},
	}}
	r, err := Generate(ts, GenerateOptions{Width: 6, Height: 6, Seed: 99, Anchors: anchors})
	if err != nil {
		t.Fatalf("Generate with anchors: %v", err)
	}
	if got := r.Cells[0].EntityType; got != 100 {
		t.Fatalf("anchor at (0,0) not respected: got %d, want 100", got)
	}
	if got := r.Cells[3*r.Width+3].EntityType; got != 200 {
		t.Fatalf("anchor at (3,3) not respected: got %d, want 200", got)
	}
}

func TestGenerate_SkipsAnchorsOutOfBoundsAndUnknownTypes(t *testing.T) {
	t.Parallel()
	// Even a very picky tileset should still solve when bad anchors are dropped.
	ts := fourTileTerrainTileSet()
	anchors := Anchors{Cells: []Cell{
		{X: 999, Y: 999, EntityType: 100}, // OOB
		{X: -1, Y: 0, EntityType: 100},    // negative
		{X: 1, Y: 1, EntityType: 9999},    // not in tileset
	}}
	r, err := Generate(ts, GenerateOptions{Width: 4, Height: 4, Seed: 11, Anchors: anchors})
	if err != nil {
		t.Fatalf("expected bad anchors to be silently dropped, got %v", err)
	}
	if len(r.Cells) != 16 {
		t.Fatalf("expected 16 cells, got %d", len(r.Cells))
	}
}

func TestGenerate_ContradictionExhaustsReseeds(t *testing.T) {
	t.Parallel()
	// Two tiles that can never neighbor each other (their edge sockets don't
	// match in any direction). The 2x1 region is unsolvable: cell (0,0) and
	// (1,0) need matching sockets across their shared edge but no pair of
	// distinct tiles satisfies that, and a single tile would also need to be
	// compatible with itself horizontally — set it up so neither can.
	ts := NewTileSet([]Tile{
		{EntityType: 1, Sockets: [4]SocketID{1, 2, 1, 3}, Weight: 1}, // E=2, W=3 (self-incompatible)
		{EntityType: 2, Sockets: [4]SocketID{1, 4, 1, 5}, Weight: 1}, // E=4, W=5 (self-incompatible; not compat with #1 either)
	})
	// Anchor both cells to incompatible tiles to force contradiction.
	anchors := Anchors{Cells: []Cell{
		{X: 0, Y: 0, EntityType: 1},
		{X: 1, Y: 0, EntityType: 2},
	}}
	_, err := Generate(ts, GenerateOptions{
		Width: 2, Height: 1, Seed: 1,
		Anchors:    anchors,
		MaxReseeds: 2,
	})
	if !errors.Is(err, ErrTooManyReseeds) {
		t.Fatalf("expected ErrTooManyReseeds, got %v", err)
	}
}

func TestGenerate_WeightBias(t *testing.T) {
	t.Parallel()
	// Three uniformly-compatible tiles (all sockets 0) with skewed weights.
	// The heavier tile should dominate output across many cells.
	ts := NewTileSet([]Tile{
		{EntityType: 1, Sockets: [4]SocketID{0, 0, 0, 0}, Weight: 1},
		{EntityType: 2, Sockets: [4]SocketID{0, 0, 0, 0}, Weight: 1},
		{EntityType: 3, Sockets: [4]SocketID{0, 0, 0, 0}, Weight: 50},
	})
	r, err := Generate(ts, GenerateOptions{Width: 16, Height: 16, Seed: 5})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	counts := map[EntityTypeID]int{}
	for _, c := range r.Cells {
		counts[c.EntityType]++
	}
	// With weight 50:1:1, tile 3 should be roughly 50/52 = ~96% of cells.
	// Use a loose 70% lower bound to stay non-flaky across seeds.
	total := len(r.Cells)
	if counts[3]*100/total < 70 {
		t.Fatalf("expected weighted tile to dominate, got counts=%v (total=%d)", counts, total)
	}
}

func TestEdge_Opposite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want Edge
	}{
		{EdgeN, EdgeS}, {EdgeS, EdgeN}, {EdgeE, EdgeW}, {EdgeW, EdgeE},
	}
	for _, c := range cases {
		if got := c.in.Opposite(); got != c.want {
			t.Fatalf("Opposite(%d)=%d, want %d", c.in, got, c.want)
		}
	}
}

func TestRegion_At(t *testing.T) {
	t.Parallel()
	r := &Region{Width: 2, Height: 2, Cells: []Cell{
		{X: 0, Y: 0, EntityType: 10},
		{X: 1, Y: 0, EntityType: 20},
		{X: 0, Y: 1, EntityType: 30},
		{X: 1, Y: 1, EntityType: 40},
	}}
	if r.At(1, 0) != 20 {
		t.Fatalf("At(1,0)=%d", r.At(1, 0))
	}
	if r.At(0, 1) != 30 {
		t.Fatalf("At(0,1)=%d", r.At(0, 1))
	}
	if r.At(-1, 0) != 0 || r.At(0, 99) != 0 {
		t.Fatalf("OOB should return 0")
	}
}
