// Boxland — pixel-similarity WFC.
//
// The companion to socket-driven WFC (generate.go). Where the socket
// engine demands exact edge-socket equality and reseeds on contradictions,
// the pixel engine derives adjacency from how the abutting *pixels* of
// neighbouring tile frames look. That trade is right for flat decorative
// tiles (grass variants, dirt patches, foliage) where small visual
// mismatches are perfectly acceptable but writing 40 socket types is a
// chore the designer shouldn't have to do.
//
// Core differences from generate.go:
//
//   * Tiles carry edge "fingerprints" (per-edge sampled pixel rows)
//     instead of socket ids.
//   * Compatibility is a similarity score (lower = better match) with a
//     KeepBestK cutoff: neighbours of a placed tile are constrained to
//     the K most-similar tiles for that edge.
//   * No backtracking. On contradiction we *fall back* to the single
//     most-compatible tile globally — designers explicitly chose this
//     mode because they prefer "always produces something" over "may
//     fail to converge". A warning still surfaces in the result.
//   * Determinism contract is identical: same (PixelTileSet, options) →
//     same Region.
//
// See PLAN.md §4g (procedural Mapmaker).

package wfc

import (
	"errors"
	"math/rand/v2"
)

// EdgeSamples is the number of RGB samples taken along one edge of a
// tile when computing its fingerprint. 8 is plenty for 32×32 tiles
// (one sample per ~4 source pixels, averaged) while keeping the per-
// tile fingerprint tiny (4 edges × 8 samples × 3 bytes = 96 B).
const EdgeSamples = 8

// EdgeFingerprint captures the look of one edge of a tile as a small
// array of averaged RGB triplets.
type EdgeFingerprint [EdgeSamples][3]uint8

// PixelTile is one tile in the pixel-WFC vocabulary. Carries fingerprints
// for all 4 edges (indexed by Edge: N=0..W=3). Weight biases selection
// just like socket-mode Tile.Weight.
type PixelTile struct {
	EntityType  EntityTypeID
	Fingerprint [4]EdgeFingerprint
	Weight      float64
}

// PixelTileSet is the analogue of TileSet for the pixel engine. Build via
// NewPixelTileSet so the per-edge top-K compatibility lists get
// precomputed (O(T² · 4) once, cheap thereafter).
type PixelTileSet struct {
	tiles []PixelTile

	// compat[from][edge] is the sorted-by-best list of tile indices that
	// can sit on the `edge` side of `from`. Capped at KeepBestK.
	compat [][4][]int

	// fallback[edge] is the single best tile to drop in when a cell ends
	// up with zero options after propagation; chosen as the tile whose
	// edge is the median of all tiles' edges (closest-to-everything).
	fallback [4]int
}

// PixelTileSetOptions controls compatibility list construction.
type PixelTileSetOptions struct {
	// KeepBestK is the per-edge candidate count. 0 = use the package
	// default (8). Smaller K = stricter matching = more visual
	// consistency but more contradictions. Larger K = more variety.
	KeepBestK int
}

// DefaultKeepBestK is the per-edge candidate cap when the caller leaves
// PixelTileSetOptions.KeepBestK at zero. Tuned for typical 16-32 tile
// palettes; produces visibly coherent fields without funneling all
// generations onto the same 2 tiles.
const DefaultKeepBestK = 8

// NewPixelTileSet precomputes per-edge compatibility lists and per-edge
// fallback tiles. Stable for identical input slices (we sort by entity
// type and break ties by index).
func NewPixelTileSet(tiles []PixelTile, opts PixelTileSetOptions) *PixelTileSet {
	keepK := opts.KeepBestK
	if keepK <= 0 {
		keepK = DefaultKeepBestK
	}
	if keepK > len(tiles) {
		keepK = len(tiles)
	}

	ts := &PixelTileSet{
		tiles:  tiles,
		compat: make([][4][]int, len(tiles)),
	}

	scratch := make([]scoredTile, 0, len(tiles))

	for i, t := range tiles {
		for edge := Edge(0); edge < 4; edge++ {
			opp := edge.Opposite()
			scratch = scratch[:0]
			for j, other := range tiles {
				d := fingerprintDistance(t.Fingerprint[edge], other.Fingerprint[opp])
				scratch = append(scratch, scoredTile{j, d})
			}
			// Stable sort by (dist, idx) so ties don't swap places between
			// runs — determinism contract requires it.
			insertionSortScored(scratch)
			n := keepK
			if n > len(scratch) {
				n = len(scratch)
			}
			out := make([]int, n)
			for k := 0; k < n; k++ {
				out[k] = scratch[k].idx
			}
			ts.compat[i][edge] = out
		}
	}

	// Per-edge fallback: the tile whose edge has the lowest total
	// distance to every other tile's opposite edge. That's "the tile
	// most likely to look fine next to anything."
	for edge := Edge(0); edge < 4; edge++ {
		opp := edge.Opposite()
		bestIdx := 0
		var bestSum uint64 = 1<<63 - 1
		for i := range tiles {
			var sum uint64
			for j := range tiles {
				sum += uint64(fingerprintDistance(tiles[i].Fingerprint[edge], tiles[j].Fingerprint[opp]))
			}
			if sum < bestSum {
				bestSum = sum
				bestIdx = i
			}
		}
		ts.fallback[edge] = bestIdx
	}

	return ts
}

// Len returns the tile count.
func (ts *PixelTileSet) Len() int { return len(ts.tiles) }

// Tile returns the tile at index i.
func (ts *PixelTileSet) Tile(i int) PixelTile { return ts.tiles[i] }

// Compat returns the top-K tile indices that look best on the `edge`
// side of tile i. Aliases internal storage; do not mutate.
func (ts *PixelTileSet) Compat(i int, edge Edge) []int { return ts.compat[i][edge] }

// fingerprintDistance is sum-of-absolute-differences across all sample
// channels. Cheap, monotonic, no sqrt — perfect for ranking. Max value
// fits in uint32 even for absurd palettes (8 samples × 3 channels × 255
// difference × 256 tiles ≈ 1.5M).
func fingerprintDistance(a, b EdgeFingerprint) uint32 {
	var d uint32
	for s := 0; s < EdgeSamples; s++ {
		for c := 0; c < 3; c++ {
			av, bv := int32(a[s][c]), int32(b[s][c])
			diff := av - bv
			if diff < 0 {
				diff = -diff
			}
			d += uint32(diff)
		}
	}
	return d
}

type scoredTile struct {
	idx  int
	dist uint32
}

func insertionSortScored(a []scoredTile) {
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i
		for j > 0 && (a[j-1].dist > v.dist || (a[j-1].dist == v.dist && a[j-1].idx > v.idx)) {
			a[j] = a[j-1]
			j--
		}
		a[j] = v
	}
}

// PixelGenerateResult is what GeneratePixel returns. The Region is the
// usual collapsed grid; Fallbacks reports how many cells were filled
// from the no-options fallback path so the UI can warn ("looks weird?
// add more tile variety").
type PixelGenerateResult struct {
	Region    *Region
	Fallbacks int
}

// ErrEmptyPixelTileSet mirrors ErrEmptyTileSet for the pixel engine.
var ErrEmptyPixelTileSet = errors.New("wfc: pixel tileset is empty")

// GeneratePixel runs the pixel-similarity engine. Same options shape as
// Generate; Anchors / Width / Height / Seed all behave identically.
// BacktrackBudget and MaxReseeds are ignored (this engine never reseeds).
func GeneratePixel(ts *PixelTileSet, opts GenerateOptions) (*PixelGenerateResult, error) {
	if ts == nil || ts.Len() == 0 {
		return nil, ErrEmptyPixelTileSet
	}
	if opts.Width <= 0 || opts.Height <= 0 {
		return nil, ErrInvalidRegion
	}

	rng := rand.New(rand.NewPCG(opts.Seed, opts.Seed^0xa5a5a5a5))
	w, h := int(opts.Width), int(opts.Height)
	cells := make([]wfcCell, w*h)
	allOpts := make([]int, ts.Len())
	for i := range allOpts {
		allOpts[i] = i
	}
	for i := range cells {
		opt := make([]int, len(allOpts))
		copy(opt, allOpts)
		cells[i].options = opt
		recomputePixelWeights(&cells[i], ts)
	}

	// Apply anchors. Same semantics as the socket engine: drop OOB or
	// unknown-entity ones, mark the rest collapsed, then propagate.
	entityIndex := make(map[EntityTypeID]int, ts.Len())
	for i := 0; i < ts.Len(); i++ {
		entityIndex[ts.tiles[i].EntityType] = i
	}
	for _, a := range opts.Anchors.Cells {
		if a.X < 0 || a.Y < 0 || int(a.X) >= w || int(a.Y) >= h {
			continue
		}
		idx, ok := entityIndex[a.EntityType]
		if !ok {
			continue
		}
		ci := int(a.Y)*w + int(a.X)
		cells[ci].collapsed = true
		cells[ci].chosen = idx
		cells[ci].options = []int{idx}
		recomputePixelWeights(&cells[ci], ts)
	}
	// Anchor-pair check is a no-op here: we never fail. Just propagate.
	for _, a := range opts.Anchors.Cells {
		if a.X < 0 || a.Y < 0 || int(a.X) >= w || int(a.Y) >= h {
			continue
		}
		if _, ok := entityIndex[a.EntityType]; !ok {
			continue
		}
		propagatePixelFrom(cells, ts, w, h, int(a.X), int(a.Y))
	}

	fallbacks := 0
	for {
		ci, found := pickLowestEntropy(cells, rng)
		if !found {
			// Either all collapsed, or we hit a contradiction. The
			// pickLowestEntropy(`zero options`) signal is the same; in
			// pixel mode we treat it as "fill the empties from
			// fallback" rather than aborting.
			break
		}
		chosen := weightedPixelPick(&cells[ci], ts, rng)
		cells[ci].collapsed = true
		cells[ci].chosen = chosen
		cells[ci].options = []int{chosen}
		recomputePixelWeights(&cells[ci], ts)
		propagatePixelFrom(cells, ts, w, h, ci%w, ci/w)
	}

	// Sweep any cell that ended up with zero options (or never got
	// touched) and drop a fallback tile in. This is the "never fails"
	// guarantee the designer asked for; we count occurrences so we can
	// surface them in the UI.
	for i := range cells {
		if cells[i].collapsed {
			continue
		}
		if len(cells[i].options) == 0 {
			cells[i].chosen = bestFallback(cells, ts, w, i)
			fallbacks++
		} else {
			cells[i].chosen = weightedPixelPick(&cells[i], ts, rng)
		}
		cells[i].collapsed = true
	}

	out := &Region{Width: opts.Width, Height: opts.Height, Cells: make([]Cell, 0, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			out.Cells = append(out.Cells, Cell{
				X: int32(x), Y: int32(y),
				EntityType: ts.tiles[c.chosen].EntityType,
			})
		}
	}
	return &PixelGenerateResult{Region: out, Fallbacks: fallbacks}, nil
}

// bestFallback picks the fallback tile that best matches whichever
// neighbours are already collapsed around (i = y*w+x). Falls back to the
// global per-edge fallback when no neighbours are collapsed.
func bestFallback(cells []wfcCell, ts *PixelTileSet, w, i int) int {
	x, y := i%w, i/w
	type neigh struct {
		edge   Edge
		chosen int
		ok     bool
	}
	deltas := []struct {
		dx, dy int
		edge   Edge
	}{
		{0, -1, EdgeN}, {1, 0, EdgeE}, {0, 1, EdgeS}, {-1, 0, EdgeW},
	}
	neighbours := make([]neigh, 0, 4)
	h := len(cells) / w
	for _, d := range deltas {
		nx, ny := x+d.dx, y+d.dy
		if nx < 0 || ny < 0 || nx >= w || ny >= h {
			continue
		}
		c := cells[ny*w+nx]
		if !c.collapsed {
			continue
		}
		neighbours = append(neighbours, neigh{d.edge, c.chosen, true})
	}
	if len(neighbours) == 0 {
		// No collapsed neighbours: just pick the global N-edge fallback.
		return ts.fallback[EdgeN]
	}
	bestIdx := 0
	var bestSum uint64 = 1<<63 - 1
	for j := range ts.tiles {
		var sum uint64
		for _, n := range neighbours {
			sum += uint64(fingerprintDistance(
				ts.tiles[j].Fingerprint[n.edge],
				ts.tiles[n.chosen].Fingerprint[n.edge.Opposite()],
			))
		}
		if sum < bestSum {
			bestSum = sum
			bestIdx = j
		}
	}
	return bestIdx
}

func recomputePixelWeights(cell *wfcCell, ts *PixelTileSet) {
	cell.cumWeights = cell.cumWeights[:0]
	if cap(cell.cumWeights) < len(cell.options) {
		cell.cumWeights = make([]float64, 0, len(cell.options))
	}
	cum := 0.0
	for _, idx := range cell.options {
		w := ts.tiles[idx].Weight
		if w <= 0 {
			w = 1
		}
		cum += w
		cell.cumWeights = append(cell.cumWeights, cum)
	}
	cell.totalWeight = cum
}

func weightedPixelPick(cell *wfcCell, ts *PixelTileSet, rng *rand.Rand) int {
	if len(cell.options) == 0 {
		return ts.fallback[EdgeN]
	}
	if len(cell.options) == 1 {
		return cell.options[0]
	}
	r := rng.Float64() * cell.totalWeight
	for i, w := range cell.cumWeights {
		if r <= w {
			return cell.options[i]
		}
	}
	return cell.options[len(cell.options)-1]
}

// propagatePixelFrom shrinks neighbour option sets to the union of "tiles
// that look good on the matching edge of any current option." Unlike the
// socket engine this never returns false on contradiction — we let
// options drain to empty and let bestFallback handle it later.
func propagatePixelFrom(cells []wfcCell, ts *PixelTileSet, w, h, sx, sy int) {
	type frontierEntry struct{ x, y int }
	frontier := []frontierEntry{{sx, sy}}
	for len(frontier) > 0 {
		curr := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		ci := curr.y*w + curr.x
		currOpts := cells[ci].options
		for _, d := range []struct {
			dx, dy int
			edge   Edge
		}{
			{0, -1, EdgeN}, {1, 0, EdgeE}, {0, 1, EdgeS}, {-1, 0, EdgeW},
		} {
			nx, ny := curr.x+d.dx, curr.y+d.dy
			if nx < 0 || ny < 0 || nx >= w || ny >= h {
				continue
			}
			nci := ny*w + nx
			if cells[nci].collapsed {
				continue
			}
			allowed := pixelAllowedFromEdge(currOpts, ts, d.edge)
			before := len(cells[nci].options)
			cells[nci].options = intersectSorted(cells[nci].options, allowed)
			if len(cells[nci].options) != before {
				recomputePixelWeights(&cells[nci], ts)
				if len(cells[nci].options) > 0 {
					frontier = append(frontier, frontierEntry{nx, ny})
				}
				// On empty: don't propagate further (any neighbour of an
				// empty cell would be over-constrained); the fallback
				// sweep at the end of GeneratePixel handles it.
			}
		}
	}
}

func pixelAllowedFromEdge(currOpts []int, ts *PixelTileSet, edge Edge) []int {
	seen := make(map[int]struct{}, 16)
	for _, idx := range currOpts {
		for _, ok := range ts.Compat(idx, edge) {
			seen[ok] = struct{}{}
		}
	}
	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	insertionSortInts(out)
	return out
}
