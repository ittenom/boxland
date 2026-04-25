package wfc

import (
	"errors"
	"fmt"
	"math/rand/v2"
)

// GenerateOptions controls one Generate call.
type GenerateOptions struct {
	Width, Height int32
	Seed          uint64

	// Anchors are pre-collapsed cells (designer regions + seam
	// constraints from neighbor chunks). Treated as already-collapsed
	// at the start of search.
	Anchors Anchors

	// BacktrackBudget bounds total cell-pop attempts. 0 = use the
	// package default. Practical maps converge in a few hundred ops;
	// the budget exists to fail-fast on impossible configurations.
	BacktrackBudget int

	// MaxReseeds is how many times Generate retries with a derived seed
	// before giving up. 0 = use the package default.
	MaxReseeds int
}

// Errors returned by Generate.
var (
	ErrTooManyReseeds = errors.New("wfc: exhausted reseed attempts; configuration likely unsolvable")
	ErrEmptyTileSet   = errors.New("wfc: tileset is empty")
	ErrInvalidRegion  = errors.New("wfc: width and height must be > 0")
)

// Defaults documented in PLAN.md task #110: bounded budget per chunk,
// reseed-and-retry on exhaustion, structured error after N reseeds.
const (
	defaultBacktrackBudget = 4096
	defaultMaxReseeds      = 4
)

// Generate runs WFC on a region. Returns a fully collapsed Region or an
// error after MaxReseeds + 1 attempts.
//
// The seed is deterministic: same (TileSet, options) -> same Region.
// This is essential for procedural-map persistence — store the seed,
// regenerate the same world on reload.
func Generate(ts *TileSet, opts GenerateOptions) (*Region, error) {
	if ts == nil || ts.Len() == 0 {
		return nil, ErrEmptyTileSet
	}
	if opts.Width <= 0 || opts.Height <= 0 {
		return nil, ErrInvalidRegion
	}
	if opts.BacktrackBudget == 0 {
		opts.BacktrackBudget = defaultBacktrackBudget
	}
	if opts.MaxReseeds == 0 {
		opts.MaxReseeds = defaultMaxReseeds
	}

	currentSeed := opts.Seed
	for attempt := 0; attempt <= opts.MaxReseeds; attempt++ {
		region, err := tryGenerate(ts, opts, currentSeed)
		if err == nil {
			return region, nil
		}
		// Reseed deterministically: derive next from current via splitmix64
		// so the retry sequence is itself reproducible.
		currentSeed = splitMix64(currentSeed + 0x9E3779B97F4A7C15)
	}
	return nil, fmt.Errorf("%w: %d attempts on seed=%d (%d cells, %d tiles)",
		ErrTooManyReseeds, opts.MaxReseeds+1, opts.Seed,
		opts.Width*opts.Height, ts.Len())
}

// tryGenerate is one WFC pass. Returns ErrBudgetExhausted on contradiction
// or budget; the caller (Generate) handles reseed.
type wfcCell struct {
	collapsed   bool
	chosen      int
	options     []int   // remaining tile indices; sorted ascending for stable iteration
	cumWeights  []float64
	totalWeight float64
}

var errBudgetExhausted = errors.New("wfc: budget exhausted")

func tryGenerate(ts *TileSet, opts GenerateOptions, seed uint64) (*Region, error) {
	rng := rand.New(rand.NewPCG(seed, seed^0xa5a5a5a5))

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
		recomputeWeights(&cells[i], ts)
	}

	// Apply anchors first. If an anchor cell references an entity type
	// not in the tileset, we drop it (the designer can't paint with what
	// they don't have).
	entityIndex := make(map[EntityTypeID]int, ts.Len())
	for i := 0; i < ts.Len(); i++ {
		entityIndex[ts.Tile(i).EntityType] = i
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
		recomputeWeights(&cells[ci], ts)
	}
	// Validate adjacent collapsed-anchor pairs: propagation skips
	// already-collapsed neighbours so it can't detect "anchor A and
	// anchor B are next to each other but their sockets disagree." We
	// check those directly so an unsolvable anchor configuration triggers
	// a reseed instead of silently producing a contradicting layout.
	for _, a := range opts.Anchors.Cells {
		if a.X < 0 || a.Y < 0 || int(a.X) >= w || int(a.Y) >= h {
			continue
		}
		ai, ok := entityIndex[a.EntityType]
		if !ok {
			continue
		}
		for _, d := range []struct {
			dx, dy int
			edge   Edge
		}{
			{0, -1, EdgeN}, {1, 0, EdgeE}, {0, 1, EdgeS}, {-1, 0, EdgeW},
		} {
			nx, ny := int(a.X)+d.dx, int(a.Y)+d.dy
			if nx < 0 || ny < 0 || nx >= w || ny >= h {
				continue
			}
			n := cells[ny*w+nx]
			if !n.collapsed {
				continue
			}
			// Both sides collapsed: edge sockets must match.
			here := ts.Tile(ai).Sockets[d.edge]
			there := ts.Tile(n.chosen).Sockets[d.edge.Opposite()]
			if here != there {
				return nil, errBudgetExhausted
			}
		}
	}
	// Propagate constraints from anchors before search starts.
	for _, a := range opts.Anchors.Cells {
		if a.X < 0 || a.Y < 0 || int(a.X) >= w || int(a.Y) >= h {
			continue
		}
		if _, ok := entityIndex[a.EntityType]; !ok {
			continue
		}
		if !propagateFrom(cells, ts, w, h, int(a.X), int(a.Y)) {
			return nil, errBudgetExhausted
		}
	}

	budget := opts.BacktrackBudget
	for {
		// Find lowest-entropy uncollapsed cell.
		ci, found := pickLowestEntropy(cells, rng)
		if !found {
			break // every cell collapsed
		}
		budget--
		if budget < 0 {
			return nil, errBudgetExhausted
		}

		// Collapse it: weighted pick from current options.
		chosen := weightedPick(&cells[ci], rng)
		cells[ci].collapsed = true
		cells[ci].chosen = chosen
		cells[ci].options = []int{chosen}
		recomputeWeights(&cells[ci], ts)

		// Propagate.
		if !propagateFrom(cells, ts, w, h, ci%w, ci/w) {
			return nil, errBudgetExhausted
		}
	}

	// Build output region.
	out := &Region{Width: opts.Width, Height: opts.Height, Cells: make([]Cell, 0, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if !c.collapsed {
				return nil, errBudgetExhausted
			}
			out.Cells = append(out.Cells, Cell{
				X: int32(x), Y: int32(y),
				EntityType: ts.Tile(c.chosen).EntityType,
			})
		}
	}
	return out, nil
}

// pickLowestEntropy returns the index of the uncollapsed cell with the
// fewest options. Ties are broken randomly so the same seed produces a
// stable sequence even when many cells share a low entropy.
func pickLowestEntropy(cells []wfcCell, rng *rand.Rand) (int, bool) {
	bestIdx := -1
	bestN := 1 << 30
	tieCount := 0
	for i := range cells {
		if cells[i].collapsed {
			continue
		}
		n := len(cells[i].options)
		if n == 0 {
			// Contradiction: a propagation pruned everything from this cell.
			return -1, false
		}
		if n < bestN {
			bestN = n
			bestIdx = i
			tieCount = 1
			continue
		}
		if n == bestN {
			tieCount++
			// Reservoir-sample the tie so each tied cell has equal chance.
			if rng.IntN(tieCount) == 0 {
				bestIdx = i
			}
		}
	}
	if bestIdx == -1 {
		return -1, false // every cell collapsed
	}
	return bestIdx, true
}

// weightedPick returns a tile index sampled by Weight from the cell's
// remaining options.
func weightedPick(cell *wfcCell, rng *rand.Rand) int {
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

// recomputeWeights rebuilds the cumulative-weight slice for the cell.
// Called after the option set shrinks (or when the cell is initialized).
func recomputeWeights(cell *wfcCell, ts *TileSet) {
	cell.cumWeights = cell.cumWeights[:0]
	if cap(cell.cumWeights) < len(cell.options) {
		cell.cumWeights = make([]float64, 0, len(cell.options))
	}
	cum := 0.0
	for _, idx := range cell.options {
		w := ts.Tile(idx).Weight
		if w <= 0 {
			w = 1
		}
		cum += w
		cell.cumWeights = append(cell.cumWeights, cum)
	}
	cell.totalWeight = cum
}

// propagateFrom updates neighbour option sets using the constraints
// from the cell at (sx, sy). Returns false if any cell ends up with
// zero options (contradiction).
func propagateFrom(cells []wfcCell, ts *TileSet, w, h, sx, sy int) bool {
	type frontierEntry struct{ x, y int }
	frontier := []frontierEntry{{sx, sy}}

	for len(frontier) > 0 {
		curr := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]

		ci := curr.y*w + curr.x
		currOpts := cells[ci].options

		// For each neighbour direction, intersect that neighbour's option
		// set with the union of "tiles that can sit on the matching edge
		// of any current option."
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
			allowed := allowedFromEdge(currOpts, ts, d.edge)
			before := len(cells[nci].options)
			cells[nci].options = intersectSorted(cells[nci].options, allowed)
			if len(cells[nci].options) == 0 {
				return false
			}
			if len(cells[nci].options) != before {
				recomputeWeights(&cells[nci], ts)
				frontier = append(frontier, frontierEntry{nx, ny})
			}
		}
	}
	return true
}

// allowedFromEdge returns the sorted union of tiles that can sit on
// `edge` of any tile in `currOpts`.
func allowedFromEdge(currOpts []int, ts *TileSet, edge Edge) []int {
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
	// Sort ascending so intersectSorted's preconditions hold.
	insertionSortInts(out)
	return out
}

// intersectSorted returns the sorted intersection of two sorted slices.
// Allocates a new slice; both inputs are owned by callers.
func intersectSorted(a, b []int) []int {
	out := make([]int, 0, len(a))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// insertionSortInts is a small in-place sort; faster than sort.Ints for
// the typical case of <50 elements per cell.
func insertionSortInts(a []int) {
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i
		for j > 0 && a[j-1] > v {
			a[j] = a[j-1]
			j--
		}
		a[j] = v
	}
}

// splitMix64 derives a fresh seed from the current one. Standard
// constant from Vigna; the bit mixing is good enough for "regenerate
// from a different starting point" without collisions in practice.
func splitMix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
