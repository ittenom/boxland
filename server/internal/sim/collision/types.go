// Package collision is the server-side swept-AABB implementation. THIS
// MUST PRODUCE byte-identical resolved deltas to web/src/collision/move.ts
// for every vector in /shared/test-vectors/collision.json. The canonical
// algorithm is documented in schemas/collision.md; both implementations
// are literal ports.
//
// Coordinate convention (fixed-point sub-pixels): 1 px = 256 sub-units.
// All math in this package is int32 arithmetic — no floats anywhere on
// the hot path.
package collision

// Sub-pixel constants. Mirror web/src/collision/types.ts.
const (
	SubPerPx     int32 = 256
	TilePx       int32 = 32
	TileSizeSub  int32 = TilePx * SubPerPx // 8192
)

// Edge-bit constants. Same encoding as world.fbs Tile.edge_collisions.
const (
	EdgeN uint8 = 1
	EdgeE uint8 = 2
	EdgeS uint8 = 4
	EdgeW uint8 = 8
)

// AABB is an axis-aligned bounding box in world sub-pixel coordinates.
type AABB struct {
	Left, Top, Right, Bottom int32
}

// Tile is a tile cell as the collision algorithm sees it (post-shape
// resolution). Mirror world.fbs Tile, minus rendering fields.
type Tile struct {
	GX                 int32
	GY                 int32
	EdgeCollisions     uint8
	CollisionLayerMask uint32
}

// Entity is the moving object's collision properties.
type Entity struct {
	AABB AABB
	Mask uint32
}

// MoveResult reports the actual delta applied after collision clipping.
type MoveResult struct {
	ResolvedDX int32
	ResolvedDY int32
}

// World is the (sparse) tile lookup the algorithm queries. Implementations
// live elsewhere (the live game uses internal/sim/spatial; tests build
// in-memory worlds via BuildWorld).
type World interface {
	TileAt(gx, gy int32) (Tile, bool)
}
