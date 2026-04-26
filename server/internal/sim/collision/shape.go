// Boxland — collision shape → edge-bits expansion.
//
// One source of truth for shape semantics, used by:
//   * the authoring path (Mapmaker tile-place hands the shape, gets edges)
//   * the runtime loader (reading map_tiles into ECS Tiles)
//   * tests + the canonical schemas/collision.md doc.
//
// Mirror of web/src/collision/shape.ts; both must stay in lockstep.
//
// Edge convention (matches world.fbs Tile.edge_collisions):
//   EdgeN = 1   the tile's NORTH edge blocks an entity moving DOWN through it.
//   EdgeE = 2   ... EAST edge blocks an entity moving WEST through it.
//   EdgeS = 4   ... SOUTH edge blocks an entity moving NORTH through it.
//   EdgeW = 8   ... WEST edge blocks an entity moving EAST through it.
//
// (i.e. the edge name is the side of the TILE that does the blocking,
// not the side of the entity. A `WallNorth` tile has only its north edge
// solid, so an entity walking south into the tile from above is stopped.)
package collision

// CollisionShape mirrors the FlatBuffers enum world.fbs CollisionShape.
// Stable; do not renumber.
type CollisionShape uint8

const (
	ShapeOpen      CollisionShape = 0
	ShapeSolid     CollisionShape = 1
	ShapeWallNorth CollisionShape = 2
	ShapeWallEast  CollisionShape = 3
	ShapeWallSouth CollisionShape = 4
	ShapeWallWest  CollisionShape = 5
	ShapeDiagNE    CollisionShape = 6
	ShapeDiagNW    CollisionShape = 7
	ShapeDiagSE    CollisionShape = 8
	ShapeDiagSW    CollisionShape = 9
	ShapeHalfNorth CollisionShape = 10
	ShapeHalfEast  CollisionShape = 11
	ShapeHalfSouth CollisionShape = 12
	ShapeHalfWest  CollisionShape = 13
	// ShapeOneWayN is the "passable from above" platform tile. Only the
	// north edge is solid — entities falling through the top are stopped,
	// jumping up through it from below is unimpeded, and stepping out
	// the sides is unimpeded. The runtime move resolver MUST also apply
	// the foot-position rule (entity foot must already be above the tile
	// top to be blocked) so a player crossing into a one-way from the
	// side mid-step doesn't pop up onto it. See IsOneWay.
	ShapeOneWayN CollisionShape = 14
)

// EdgesForShape returns the edge-bit mask for a shape. Unknown shapes
// default to ShapeOpen (no collision).
func EdgesForShape(s CollisionShape) uint8 {
	switch s {
	case ShapeOpen:
		return 0
	case ShapeSolid:
		return EdgeN | EdgeE | EdgeS | EdgeW
	case ShapeWallNorth:
		return EdgeN
	case ShapeWallEast:
		return EdgeE
	case ShapeWallSouth:
		return EdgeS
	case ShapeWallWest:
		return EdgeW
	case ShapeDiagNE:
		// Triangle whose hypotenuse runs from NE corner to SW corner.
		// The two solid edges are the two sides of the entity that
		// would touch the *tile interior*: north + east.
		return EdgeN | EdgeE
	case ShapeDiagNW:
		return EdgeN | EdgeW
	case ShapeDiagSE:
		return EdgeS | EdgeE
	case ShapeDiagSW:
		return EdgeS | EdgeW
	case ShapeHalfNorth:
		// Half-tile occupying the north half: blocks downward motion
		// from above + side motion through the upper half. v1 keeps it
		// simple: same edges as a Solid tile, then the runtime trims
		// based on entity geometry. Future revisions may emit a
		// half-cell record.
		return EdgeN | EdgeE | EdgeW
	case ShapeHalfEast:
		return EdgeN | EdgeE | EdgeS
	case ShapeHalfSouth:
		return EdgeE | EdgeS | EdgeW
	case ShapeHalfWest:
		return EdgeN | EdgeS | EdgeW
	case ShapeOneWayN:
		// Only the north edge is solid; the rest pass through.
		return EdgeN
	}
	return 0
}

// IsOneWay reports whether a shape needs the runtime foot-position rule
// applied at move-resolution time. The move resolver should:
//
//   1. expand shape → edges via EdgesForShape (already done at load).
//   2. if IsOneWay(shape) AND axis==Y AND step>0 AND entity foot is NOT
//      already above the tile top, skip the block.
//
// "Already above the tile top" means entity.AABB.Bottom <= tile.Top
// at the START of the sweep (sub-pixel precision). That's the
// standard Cave-Story / Mario semantics: jumping up through the
// platform is allowed, landing on it is blocked.
func IsOneWay(s CollisionShape) bool {
	return s == ShapeOneWayN
}
