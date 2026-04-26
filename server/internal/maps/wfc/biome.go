// Boxland — biome pre-pass for chunked WFC.
//
// Background: chunked WFC produces locally consistent output but lacks
// any large-scale structure. A 256×256 map looks like 16 independent
// noise samples — every chunk has roughly the same average colour
// regardless of position. The fix used by hierarchical WFC research
// (fileho/Hierarchical-Wave-Function-Collapse, Laurent et al. 2025) is
// to run a coarse first pass that assigns each chunk to one of N
// "biomes", then filter the WFC vocabulary per chunk so a "forest"
// biome only emits forest-tagged tiles.
//
// This file provides the building blocks (BiomeMap + value-noise
// generator) and the per-chunk filter wiring (ChunkedOptions.BiomeCount
// + ChunkedOptions.BiomePalette). The actual entity-type-to-biome
// tagging is a follow-up DB change; until that lands callers leave
// BiomeCount at zero and the chunked engine behaves identically to
// before.

package wfc

import (
	"math/rand/v2"
)

// BiomeMap is the result of the pre-pass: a chunk-resolution grid where
// each cell is a biome label in [0, BiomeCount). Stored row-major.
type BiomeMap struct {
	CountX, CountY int32
	BiomeCount     int
	// Labels[cy*CountX + cx] is the biome at chunk (cx, cy).
	Labels []int
}

// At returns the biome label at chunk (cx, cy), or -1 if out of bounds.
func (b *BiomeMap) At(cx, cy int32) int {
	if cx < 0 || cy < 0 || cx >= b.CountX || cy >= b.CountY {
		return -1
	}
	return b.Labels[cy*b.CountX+cx]
}

// BiomeMapOptions controls GenerateBiomeMap.
type BiomeMapOptions struct {
	// CountX, CountY are the chunk-grid dimensions (NOT cell dimensions).
	CountX, CountY int32

	// BiomeCount is how many distinct biome labels to assign. >= 2.
	BiomeCount int

	// Seed is the deterministic seed.
	Seed uint64

	// Frequency controls the smoothness of biome regions. Smaller =
	// larger contiguous regions. Default 0.25 produces 4-6 cell wide
	// blobs which look right at typical map sizes (16x16 chunks).
	// Range: 0.05 to 1.0.
	Frequency float64
}

// DefaultBiomeFrequency is used when BiomeMapOptions.Frequency is 0.
const DefaultBiomeFrequency = 0.25

// GenerateBiomeMap builds a coarse biome assignment using value noise.
//
// Algorithm: for each chunk, sample a smooth scalar field at (cx, cy)
// in noise-space (cx*Frequency, cy*Frequency), then quantise the [0, 1)
// result into [0, BiomeCount) bins. Value noise is the simplest
// procedural noise that produces visibly contiguous regions; we don't
// need Perlin's gradient-vector dance for this use case.
//
// Determinism: same (Seed, dims, BiomeCount, Frequency) → same map.
func GenerateBiomeMap(opts BiomeMapOptions) *BiomeMap {
	if opts.CountX <= 0 || opts.CountY <= 0 || opts.BiomeCount < 1 {
		return &BiomeMap{
			CountX:     opts.CountX,
			CountY:     opts.CountY,
			BiomeCount: opts.BiomeCount,
			Labels:     []int{},
		}
	}
	freq := opts.Frequency
	if freq <= 0 {
		freq = DefaultBiomeFrequency
	}

	// Pre-generate a hashed lattice. Value noise samples between hashed
	// integer-coordinate values; the lattice spans the noise-space
	// covered by the chunk grid (rounded up).
	lw := int(float64(opts.CountX)*freq) + 2
	lh := int(float64(opts.CountY)*freq) + 2
	if lw < 2 {
		lw = 2
	}
	if lh < 2 {
		lh = 2
	}

	rng := rand.New(rand.NewPCG(opts.Seed, opts.Seed^0x6a09e667bb67ae85))
	lattice := make([]float64, lw*lh)
	for i := range lattice {
		lattice[i] = rng.Float64()
	}

	labels := make([]int, int(opts.CountX)*int(opts.CountY))
	for cy := int32(0); cy < opts.CountY; cy++ {
		for cx := int32(0); cx < opts.CountX; cx++ {
			nx := float64(cx) * freq
			ny := float64(cy) * freq
			v := sampleValueNoise(lattice, lw, lh, nx, ny)
			label := int(v * float64(opts.BiomeCount))
			if label >= opts.BiomeCount {
				label = opts.BiomeCount - 1
			}
			if label < 0 {
				label = 0
			}
			labels[cy*opts.CountX+cx] = label
		}
	}
	return &BiomeMap{
		CountX:     opts.CountX,
		CountY:     opts.CountY,
		BiomeCount: opts.BiomeCount,
		Labels:     labels,
	}
}

// sampleValueNoise samples the bilinearly-smoothed lattice at (x, y).
// Returns a value in [0, 1). Uses smoothstep for the interpolation
// (cheap, branch-free, looks better than linear).
func sampleValueNoise(lattice []float64, lw, lh int, x, y float64) float64 {
	xi := int(x)
	yi := int(y)
	if xi < 0 {
		xi = 0
	}
	if yi < 0 {
		yi = 0
	}
	if xi >= lw-1 {
		xi = lw - 2
	}
	if yi >= lh-1 {
		yi = lh - 2
	}
	tx := smoothstep(x - float64(xi))
	ty := smoothstep(y - float64(yi))

	v00 := lattice[yi*lw+xi]
	v10 := lattice[yi*lw+xi+1]
	v01 := lattice[(yi+1)*lw+xi]
	v11 := lattice[(yi+1)*lw+xi+1]

	a := v00 + (v10-v00)*tx
	b := v01 + (v11-v01)*tx
	out := a + (b-a)*ty
	if out < 0 {
		out = 0
	}
	if out >= 1 {
		out = 0.9999999
	}
	return out
}

// smoothstep is the standard 3t² − 2t³ Hermite curve, clamped.
func smoothstep(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// FilterTilesByBiome returns a TileSet containing only tiles whose
// entity types appear in `allowed`. If `allowed` is empty, returns the
// full tileset unchanged (graceful degradation when biome metadata
// hasn't been authored yet for some biome).
func FilterTilesByBiome(ts *TileSet, allowed []EntityTypeID) *TileSet {
	if ts == nil || len(allowed) == 0 {
		return ts
	}
	allow := make(map[EntityTypeID]struct{}, len(allowed))
	for _, et := range allowed {
		allow[et] = struct{}{}
	}
	filtered := make([]Tile, 0, len(ts.tiles))
	for _, t := range ts.tiles {
		if _, ok := allow[t.EntityType]; ok {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) == 0 {
		// Filter would empty the tileset — degrade rather than crash.
		return ts
	}
	return NewTileSet(filtered)
}
