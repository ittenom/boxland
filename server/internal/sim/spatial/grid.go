// Package spatial implements the uniform-grid spatial index used by the
// AOI broadcaster, collision sweep, and (later) range-based automation
// triggers. See PLAN.md §1 ("Spatial index") and §4h.
//
// The grid is keyed by (chunkX, chunkY) — each chunk is a 16x16 tile
// region. Entities live in exactly one chunk at a time (whichever chunk
// contains their AABB centre); moving an entity calls Move(old, new).
//
// Concurrency: not safe for concurrent mutation. The map-instance
// goroutine owns the grid alongside the World.
package spatial

import (
	"boxland/server/internal/sim/ecs"
)

// ChunkTiles is the side length of a chunk in tiles. 16 tiles = 16*32 = 512
// pixels per chunk, which the streaming code can serialise into one WAL
// frame comfortably.
const ChunkTiles = 16

// ChunkPxPerTile mirrors the canonical sprite cell. Lives here so callers
// converting world coords -> chunk coords don't have to import the asset
// package.
const ChunkPxPerTile = 32

// chunkSpan is the number of *world pixels* one chunk covers (16 * 32).
const chunkSpan = ChunkTiles * ChunkPxPerTile

// ChunkID identifies one chunk. Pack (cx, cy) so the map can use it
// without composing a slice of two ints.
type ChunkID uint64

// MakeChunkID returns the ChunkID for grid coords (cx, cy). The encoding
// folds signed int32 coords into the high/low halves of a uint64 so the
// id is a stable map key.
func MakeChunkID(cx, cy int32) ChunkID {
	return ChunkID(uint64(uint32(cx))<<32 | uint64(uint32(cy)))
}

// Coords returns the (cx, cy) the ChunkID was built from.
func (c ChunkID) Coords() (cx, cy int32) {
	return int32(uint32(c >> 32)), int32(uint32(c & 0xffffffff))
}

// ChunkOf returns the chunk that contains world pixel (px, py). World
// coords are *pixels*, not sub-pixels — callers in collision/AOI code
// divide their fixed-point sub-pixel coords by SUB_PER_PX (256) first.
func ChunkOf(px, py int32) ChunkID {
	// Floor-divide so negative coords land in the correct chunk.
	cx := floorDiv(px, chunkSpan)
	cy := floorDiv(py, chunkSpan)
	return MakeChunkID(cx, cy)
}

// floorDiv is integer division that rounds towards negative infinity.
// Stdlib `/` rounds towards zero; we need floor for chunk math.
func floorDiv(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// Grid is the spatial index. Tracks per-chunk entity sets; moving an
// entity is one Remove + one Add. Both operations are O(1).
type Grid struct {
	chunks  map[ChunkID]map[ecs.EntityID]struct{}
	homeOf  map[ecs.EntityID]ChunkID // current chunk per entity
	version map[ChunkID]uint64       // bumped on every mutation; AOI uses this
}

// New returns an empty grid.
func New() *Grid {
	return &Grid{
		chunks:  make(map[ChunkID]map[ecs.EntityID]struct{}),
		homeOf:  make(map[ecs.EntityID]ChunkID),
		version: make(map[ChunkID]uint64),
	}
}

// Add registers an entity at world pixel (px, py). Returns the ChunkID it
// landed in. Calling Add for an entity already in the grid moves it
// (equivalent to Move).
func (g *Grid) Add(e ecs.EntityID, px, py int32) ChunkID {
	dest := ChunkOf(px, py)
	if home, ok := g.homeOf[e]; ok {
		if home == dest {
			return dest
		}
		g.removeFrom(home, e)
	}
	g.addTo(dest, e)
	g.homeOf[e] = dest
	return dest
}

// Move updates an entity's chunk if (px, py) crossed a chunk boundary.
// Returns the chunk after the move (which may equal the prior chunk).
// More efficient than Add when the caller already tracks "did I cross a
// boundary?" upstream; functionally identical otherwise.
func (g *Grid) Move(e ecs.EntityID, px, py int32) ChunkID {
	return g.Add(e, px, py)
}

// Remove drops the entity from the grid. No-op if absent.
func (g *Grid) Remove(e ecs.EntityID) {
	home, ok := g.homeOf[e]
	if !ok {
		return
	}
	g.removeFrom(home, e)
	delete(g.homeOf, e)
}

// QueryChunk returns every entity currently in the named chunk. The
// returned slice is owned by the caller; safe to mutate.
func (g *Grid) QueryChunk(c ChunkID) []ecs.EntityID {
	set := g.chunks[c]
	if len(set) == 0 {
		return nil
	}
	out := make([]ecs.EntityID, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	return out
}

// QueryRange returns every entity in any chunk overlapping the world-pixel
// AABB [minX..maxX, minY..maxY] (inclusive). Used by collision sweep,
// AOI radius scans, and proximity automation triggers.
func (g *Grid) QueryRange(minX, minY, maxX, maxY int32) []ecs.EntityID {
	cx0 := floorDiv(minX, chunkSpan)
	cy0 := floorDiv(minY, chunkSpan)
	cx1 := floorDiv(maxX, chunkSpan)
	cy1 := floorDiv(maxY, chunkSpan)

	var out []ecs.EntityID
	for cy := cy0; cy <= cy1; cy++ {
		for cx := cx0; cx <= cx1; cx++ {
			set := g.chunks[MakeChunkID(cx, cy)]
			for e := range set {
				out = append(out, e)
			}
		}
	}
	return out
}

// Version returns the per-chunk monotonic version counter, bumped on
// every Add/Remove that touches the chunk. Used by the AOI subscription
// manager to skip chunks whose state hasn't changed.
func (g *Grid) Version(c ChunkID) uint64 {
	return g.version[c]
}

// HomeOf returns the chunk an entity currently lives in, or false if the
// entity isn't in the grid.
func (g *Grid) HomeOf(e ecs.EntityID) (ChunkID, bool) {
	c, ok := g.homeOf[e]
	return c, ok
}

// Stats returns a small bag of telemetry useful for /healthz-style
// reporting. Cheap to compute (just len() calls).
func (g *Grid) Stats() Stats {
	return Stats{
		Chunks:   len(g.chunks),
		Entities: len(g.homeOf),
	}
}

// Stats reports the live-population health of a Grid.
type Stats struct {
	Chunks   int
	Entities int
}

// ---- internals ----

func (g *Grid) addTo(c ChunkID, e ecs.EntityID) {
	set := g.chunks[c]
	if set == nil {
		set = make(map[ecs.EntityID]struct{})
		g.chunks[c] = set
	}
	set[e] = struct{}{}
	g.version[c]++
}

func (g *Grid) removeFrom(c ChunkID, e ecs.EntityID) {
	set := g.chunks[c]
	if set == nil {
		return
	}
	delete(set, e)
	g.version[c]++
	if len(set) == 0 {
		delete(g.chunks, c)
	}
}
