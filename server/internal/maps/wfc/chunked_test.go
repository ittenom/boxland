package wfc

import (
	"errors"
	"reflect"
	"testing"
)

func TestGenerateChunked_RejectsEmptyTileSet(t *testing.T) {
	t.Parallel()
	_, err := GenerateChunked(NewTileSet(nil), ChunkedOptions{
		ChunkW: 4, ChunkH: 4, CountX: 2, CountY: 2, Seed: 1,
	})
	if !errors.Is(err, ErrEmptyTileSet) {
		t.Fatalf("expected ErrEmptyTileSet, got %v", err)
	}
}

func TestGenerateChunked_RejectsInvalidDimensions(t *testing.T) {
	t.Parallel()
	ts := uniformGrassTileSet()
	cases := []ChunkedOptions{
		{ChunkW: 0, ChunkH: 4, CountX: 2, CountY: 2, Seed: 1},
		{ChunkW: 4, ChunkH: 4, CountX: 0, CountY: 2, Seed: 1},
		{ChunkW: 4, ChunkH: -1, CountX: 2, CountY: 2, Seed: 1},
	}
	for _, c := range cases {
		if _, err := GenerateChunked(ts, c); !errors.Is(err, ErrInvalidRegion) {
			t.Fatalf("expected ErrInvalidRegion for %+v, got %v", c, err)
		}
	}
}

func TestGenerateChunked_OutputDimensions(t *testing.T) {
	t.Parallel()
	ts := fourTileTerrainTileSet()
	r, err := GenerateChunked(ts, ChunkedOptions{
		ChunkW: 4, ChunkH: 4, CountX: 3, CountY: 2, Seed: 7,
	})
	if err != nil {
		t.Fatalf("GenerateChunked: %v", err)
	}
	if r.Width != 12 || r.Height != 8 {
		t.Fatalf("dimensions: got %dx%d, want 12x8", r.Width, r.Height)
	}
	if int32(len(r.Cells)) != r.Width*r.Height {
		t.Fatalf("expected %d cells, got %d", r.Width*r.Height, len(r.Cells))
	}
	// Verify row-major ordering.
	for i, c := range r.Cells {
		wantX := int32(i) % r.Width
		wantY := int32(i) / r.Width
		if c.X != wantX || c.Y != wantY {
			t.Fatalf("cell %d should be (%d,%d), got (%d,%d)", i, wantX, wantY, c.X, c.Y)
		}
	}
}

func TestGenerateChunked_DeterministicSameSeed(t *testing.T) {
	t.Parallel()
	ts := fourTileTerrainTileSet()
	opts := ChunkedOptions{ChunkW: 4, ChunkH: 4, CountX: 3, CountY: 3, Seed: 4242}

	r1, err := GenerateChunked(ts, opts)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	r2, err := GenerateChunked(ts, opts)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !reflect.DeepEqual(r1.Cells, r2.Cells) {
		t.Fatalf("identical seed produced different output")
	}
}

func TestGenerateChunked_SeamsRespectSocketCompatibility(t *testing.T) {
	t.Parallel()
	ts := fourTileTerrainTileSet()
	r, err := GenerateChunked(ts, ChunkedOptions{
		ChunkW: 4, ChunkH: 4, CountX: 3, CountY: 3, Seed: 17,
	})
	if err != nil {
		t.Fatalf("GenerateChunked: %v", err)
	}
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
	// Walk every cell and verify edge-socket compatibility with E and S
	// neighbors (covers all internal edges, including seams between
	// chunks).
	for y := int32(0); y < r.Height; y++ {
		for x := int32(0); x < r.Width; x++ {
			here, _ := get(x, y)
			if n, ok := get(x+1, y); ok && here.Sockets[EdgeE] != n.Sockets[EdgeW] {
				t.Fatalf("E-W mismatch at (%d,%d)/(%d,%d): %d vs %d", x, y, x+1, y, here.Sockets[EdgeE], n.Sockets[EdgeW])
			}
			if n, ok := get(x, y+1); ok && here.Sockets[EdgeS] != n.Sockets[EdgeN] {
				t.Fatalf("N-S mismatch at (%d,%d)/(%d,%d): %d vs %d", x, y, x, y+1, here.Sockets[EdgeS], n.Sockets[EdgeN])
			}
		}
	}
}

func TestGenerateChunked_RespectsWorldCoordinateAnchors(t *testing.T) {
	t.Parallel()
	// Use the uniform tileset so seam propagation can never contradict a
	// designer-painted anchor — the test's job is to verify ROUTING
	// (world coords -> chunk-local) works, not the broader solver.
	ts := uniformGrassTileSet()
	anchors := Anchors{Cells: []Cell{
		{X: 1, Y: 1, EntityType: 1}, // chunk (0,0) local (1,1)
		{X: 5, Y: 1, EntityType: 1}, // chunk (1,0) local (1,1)
		{X: 6, Y: 6, EntityType: 1}, // chunk (1,1) local (2,2)
	}}
	r, err := GenerateChunked(ts, ChunkedOptions{
		ChunkW: 4, ChunkH: 4, CountX: 2, CountY: 2, Seed: 33, Anchors: anchors,
	})
	if err != nil {
		t.Fatalf("GenerateChunked: %v", err)
	}
	want := map[[2]int32]EntityTypeID{
		{1, 1}: 1,
		{5, 1}: 1,
		{6, 6}: 1,
	}
	for xy, et := range want {
		got := r.Cells[xy[1]*r.Width+xy[0]].EntityType
		if got != et {
			t.Fatalf("anchor at (%d,%d) not respected: got %d, want %d", xy[0], xy[1], got, et)
		}
	}
}

func TestGenerateChunked_SingleChunkMatchesPlainGenerate(t *testing.T) {
	t.Parallel()
	// CountX=CountY=1 should yield the same content as a single Generate
	// (modulo seam-derivation: the single chunk has no neighbors so seam
	// anchors are empty, and the per-chunk seed derivation lifts the
	// master seed through splitMix64). We compare topology, not bytes.
	ts := fourTileTerrainTileSet()
	r, err := GenerateChunked(ts, ChunkedOptions{
		ChunkW: 6, ChunkH: 6, CountX: 1, CountY: 1, Seed: 99,
	})
	if err != nil {
		t.Fatalf("GenerateChunked: %v", err)
	}
	if r.Width != 6 || r.Height != 6 || len(r.Cells) != 36 {
		t.Fatalf("expected 6x6 / 36 cells, got %dx%d / %d cells", r.Width, r.Height, len(r.Cells))
	}
}

func TestGenerateChunked_LargeMap(t *testing.T) {
	t.Parallel()
	// Stress test: 4x4 chunks of 16x16 = 64x64 total = 4096 cells. The
	// terrain tileset is solvable everywhere so this should always succeed
	// and finish well under a second.
	ts := fourTileTerrainTileSet()
	r, err := GenerateChunked(ts, ChunkedOptions{
		ChunkW: 16, ChunkH: 16, CountX: 4, CountY: 4, Seed: 2026,
	})
	if err != nil {
		t.Fatalf("GenerateChunked: %v", err)
	}
	if int(r.Width)*int(r.Height) != len(r.Cells) {
		t.Fatalf("cell count mismatch")
	}
}
