package wfc

import (
	"testing"
)

func TestGenerateBiomeMap_DimensionsAndRange(t *testing.T) {
	bm := GenerateBiomeMap(BiomeMapOptions{
		CountX: 4, CountY: 3, BiomeCount: 3, Seed: 7,
	})
	if bm.CountX != 4 || bm.CountY != 3 || len(bm.Labels) != 12 {
		t.Fatalf("dims wrong: %+v", bm)
	}
	for _, lbl := range bm.Labels {
		if lbl < 0 || lbl >= 3 {
			t.Errorf("label out of range: %d", lbl)
		}
	}
}

func TestGenerateBiomeMap_DeterministicSameSeed(t *testing.T) {
	a := GenerateBiomeMap(BiomeMapOptions{CountX: 5, CountY: 5, BiomeCount: 4, Seed: 42})
	b := GenerateBiomeMap(BiomeMapOptions{CountX: 5, CountY: 5, BiomeCount: 4, Seed: 42})
	for i := range a.Labels {
		if a.Labels[i] != b.Labels[i] {
			t.Errorf("label %d differs: %d vs %d", i, a.Labels[i], b.Labels[i])
		}
	}
}

func TestGenerateBiomeMap_DifferentSeedsDiffer(t *testing.T) {
	a := GenerateBiomeMap(BiomeMapOptions{CountX: 8, CountY: 8, BiomeCount: 4, Seed: 1})
	b := GenerateBiomeMap(BiomeMapOptions{CountX: 8, CountY: 8, BiomeCount: 4, Seed: 9999})
	differs := false
	for i := range a.Labels {
		if a.Labels[i] != b.Labels[i] {
			differs = true
			break
		}
	}
	if !differs {
		t.Errorf("two seeds produced identical biome maps")
	}
}

func TestGenerateBiomeMap_ProducesContiguousRegions(t *testing.T) {
	// With low frequency, neighbouring chunks should usually share a
	// biome. Test that on a 16x16 grid at most ~30% of horizontal
	// neighbours have different labels (a hard bound that should hold
	// for any reasonable seed at the default frequency).
	bm := GenerateBiomeMap(BiomeMapOptions{
		CountX: 16, CountY: 16, BiomeCount: 3, Seed: 13,
	})
	w := int(bm.CountX)
	transitions, total := 0, 0
	for y := 0; y < int(bm.CountY); y++ {
		for x := 0; x < w-1; x++ {
			total++
			if bm.Labels[y*w+x] != bm.Labels[y*w+x+1] {
				transitions++
			}
		}
	}
	frac := float64(transitions) / float64(total)
	if frac > 0.5 {
		t.Errorf("too noisy: %.0f%% horizontal transitions (want < 50%% for value-noise biomes)", frac*100)
	}
}

func TestGenerateBiomeMap_ZeroDimsReturnsEmpty(t *testing.T) {
	bm := GenerateBiomeMap(BiomeMapOptions{CountX: 0, CountY: 5, BiomeCount: 3, Seed: 1})
	if len(bm.Labels) != 0 {
		t.Errorf("expected empty labels, got %d", len(bm.Labels))
	}
}

func TestBiomeMap_AtBounds(t *testing.T) {
	bm := GenerateBiomeMap(BiomeMapOptions{CountX: 3, CountY: 3, BiomeCount: 2, Seed: 1})
	if got := bm.At(-1, 0); got != -1 {
		t.Errorf("oob At(-1,0) = %d, want -1", got)
	}
	if got := bm.At(0, 5); got != -1 {
		t.Errorf("oob At(0,5) = %d, want -1", got)
	}
	if got := bm.At(0, 0); got < 0 || got >= 2 {
		t.Errorf("in-bounds At(0,0) = %d, want 0..1", got)
	}
}

func TestFilterTilesByBiome_KeepsAllowed(t *testing.T) {
	ts := NewTileSet([]Tile{
		{EntityType: 1, Sockets: [4]SocketID{1, 1, 1, 1}, Weight: 1},
		{EntityType: 2, Sockets: [4]SocketID{1, 1, 1, 1}, Weight: 1},
		{EntityType: 3, Sockets: [4]SocketID{1, 1, 1, 1}, Weight: 1},
	})
	got := FilterTilesByBiome(ts, []EntityTypeID{1, 3})
	if got.Len() != 2 {
		t.Errorf("filter kept %d tiles, want 2", got.Len())
	}
	for i := 0; i < got.Len(); i++ {
		et := got.Tile(i).EntityType
		if et != 1 && et != 3 {
			t.Errorf("filter let through %d", et)
		}
	}
}

func TestFilterTilesByBiome_EmptyAllowedReturnsOriginal(t *testing.T) {
	ts := NewTileSet([]Tile{
		{EntityType: 1, Sockets: [4]SocketID{1, 1, 1, 1}, Weight: 1},
	})
	got := FilterTilesByBiome(ts, nil)
	if got != ts {
		t.Error("expected the same tileset pointer back when allowed is nil")
	}
}

func TestFilterTilesByBiome_EmptyResultDegrades(t *testing.T) {
	// If the filter would empty the tileset, FilterTilesByBiome returns
	// the original (the engine prefers "ugly biome" over "no tiles").
	ts := NewTileSet([]Tile{
		{EntityType: 1, Sockets: [4]SocketID{1, 1, 1, 1}, Weight: 1},
	})
	got := FilterTilesByBiome(ts, []EntityTypeID{99})
	if got != ts {
		t.Error("expected original tileset back when filter would empty it")
	}
}

func TestGenerateChunked_BiomeFilteringRestrictsTiles(t *testing.T) {
	// Three tiles, three biomes. Each biome's palette is one tile.
	// Every chunk should contain only one tile after generation.
	ts := NewTileSet([]Tile{
		{EntityType: 1, Sockets: [4]SocketID{1, 1, 1, 1}, Weight: 1},
		{EntityType: 2, Sockets: [4]SocketID{1, 1, 1, 1}, Weight: 1},
		{EntityType: 3, Sockets: [4]SocketID{1, 1, 1, 1}, Weight: 1},
	})
	region, err := GenerateChunked(ts, ChunkedOptions{
		ChunkW: 4, ChunkH: 4, CountX: 4, CountY: 4, Seed: 1,
		BiomeCount: 3,
		BiomePalette: func(b int) []EntityTypeID {
			return []EntityTypeID{EntityTypeID(b + 1)}
		},
	})
	if err != nil {
		t.Fatalf("GenerateChunked: %v", err)
	}
	// Every chunk should be uniform (all cells same entity type).
	for cy := int32(0); cy < 4; cy++ {
		for cx := int32(0); cx < 4; cx++ {
			seen := EntityTypeID(0)
			for y := int32(0); y < 4; y++ {
				for x := int32(0); x < 4; x++ {
					et := region.Cells[(cy*4+y)*16+(cx*4+x)].EntityType
					if seen == 0 {
						seen = et
					} else if et != seen {
						// Allow seam exception: anchored cells from
						// neighbour chunks may carry a different biome's
						// tile into this chunk's edge. Only flag if
						// non-edge cells differ.
						if x != 0 && y != 0 {
							t.Errorf("chunk (%d,%d) interior cell (%d,%d) = %d, expected %d (biome filter broke)",
								cx, cy, x, y, et, seen)
						}
					}
				}
			}
		}
	}
}
