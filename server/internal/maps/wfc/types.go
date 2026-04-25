// Package wfc implements chunked Wave Function Collapse for the
// procedural Mapmaker (PLAN.md §4g).
//
// One WFC run produces a 64x64 tile region given:
//   * a TileSet (every tile-kind entity-type with N/E/S/W socket ids)
//   * an optional set of pre-collapsed cells (anchor regions, or seam
//     constraints from the neighbor chunk)
//   * a deterministic seed
//
// The Generate() entry point handles backtracking, budget exhaustion,
// and reseed-and-retry for the caller. Chunks are tiled by the caller
// (see ChunkedGenerate); this package is intentionally agnostic about
// whether it's running on chunk 0 or chunk N.
package wfc

// EntityTypeID identifies one tile-kind entity-type. Mirrors the
// entity_types.id surface but uses int64 directly to avoid a package
// dependency on entities.
type EntityTypeID int64

// SocketID identifies one edge-socket type. 0 is reserved as "no
// socket assigned" — a tile with a 0 socket on an edge is treated as
// matching only other 0 sockets (this is the default for designer-
// authored tile types that haven't yet been assigned sockets, so the
// engine doesn't crash when half the project's tiles are unsocketed).
type SocketID int64

// Edge identifies one cardinal direction. Order matters: N=0..W=3 lets
// us index into the [4]SocketID arrays without converting strings.
type Edge uint8

const (
	EdgeN Edge = iota
	EdgeE
	EdgeS
	EdgeW
)

// Opposite returns the edge across from e (N <-> S, E <-> W).
func (e Edge) Opposite() Edge {
	switch e {
	case EdgeN:
		return EdgeS
	case EdgeE:
		return EdgeW
	case EdgeS:
		return EdgeN
	case EdgeW:
		return EdgeE
	}
	return e
}

// Tile is one possible placement: an entity-type with its 4 edge sockets.
// Weight controls how often the WFC picks it during entropy collapse;
// 1.0 = neutral, higher values bias selection toward this tile.
type Tile struct {
	EntityType EntityTypeID
	Sockets    [4]SocketID // indexed by Edge
	Weight     float64
}

// TileSet is the full vocabulary the WFC can pick from. Build via
// NewTileSet so the internal compatibility tables get pre-computed.
type TileSet struct {
	tiles []Tile

	// compat[from][edge] is the set of tile indices that can sit on the
	// `edge` side of `from`. Pre-computed at NewTileSet so the per-cell
	// propagation step is one bitset intersection rather than a nested
	// loop over every tile pair.
	compat [][4][]int
}

// NewTileSet builds the precomputed compatibility tables. O(T^2 * 4)
// in tile count; called once per WFC session, not per cell.
func NewTileSet(tiles []Tile) *TileSet {
	ts := &TileSet{tiles: tiles}
	ts.compat = make([][4][]int, len(tiles))

	for i, t := range tiles {
		for edge := Edge(0); edge < 4; edge++ {
			s := t.Sockets[edge]
			opp := edge.Opposite()
			var matches []int
			for j, other := range tiles {
				if other.Sockets[opp] == s {
					matches = append(matches, j)
				}
			}
			ts.compat[i][edge] = matches
		}
	}
	return ts
}

// Len returns the tile count.
func (ts *TileSet) Len() int { return len(ts.tiles) }

// Tile returns the tile at index i. The caller must ensure i is valid.
func (ts *TileSet) Tile(i int) Tile { return ts.tiles[i] }

// Compat returns the indices of tiles that can sit on the `edge` side of
// the tile at index i. Returned slice aliases internal storage; do not
// mutate.
func (ts *TileSet) Compat(i int, edge Edge) []int {
	return ts.compat[i][edge]
}

// Cell is one (x, y) within a generation region. Used for anchor inputs
// and for the produced output.
type Cell struct {
	X, Y       int32
	EntityType EntityTypeID
}

// Region is a generation result: every cell collapsed to one tile.
type Region struct {
	Width, Height int32
	Cells         []Cell // length = Width * Height; row-major
}

// At returns the entity-type at (x, y), or 0 if out of bounds / not present.
func (r *Region) At(x, y int32) EntityTypeID {
	if x < 0 || y < 0 || x >= r.Width || y >= r.Height {
		return 0
	}
	for _, c := range r.Cells {
		if c.X == x && c.Y == y {
			return c.EntityType
		}
	}
	return 0
}

// Anchors holds pre-collapsed cells the engine starts from. May include
// designer-authored anchor-region cells AND seam constraints from
// neighbor chunks; the engine treats them identically.
type Anchors struct {
	Cells []Cell
}
