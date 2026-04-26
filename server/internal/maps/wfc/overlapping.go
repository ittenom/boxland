// Boxland — overlapping-model WFC.
//
// The Overlapping Model is the second of two WFC variants from Maxim
// Gumin's reference implementation (https://github.com/mxgmn/WaveFunctionCollapse,
// MIT). Where the socket engine in generate.go takes a fixed vocabulary
// of tiles + per-edge socket ids, the overlapping engine takes a small
// "sample patch" — a designer-painted rectangle of entity-type ids —
// extracts every NxN window from it, and only emits NxN windows that
// appeared in the sample. That switches the unit of constraint from
// "edges line up" to "this exact local context occurred in something a
// human drew", which is the difference between the static-noise output
// in PLAN.md §4g screenshots and a coherent-looking field.
//
// Implementation notes:
//
//   * Patterns are hashed by their N*N entity-type sequence. The hash is
//     stable across runs (FNV-1a over the int64 sequence), so the same
//     sample produces the same pattern indices, which keeps the engine
//     deterministic in (sample, seed) — required for our reseed-on-
//     reload contract (procedural.go).
//   * Pattern "weight" is the count of occurrences in the sample. Tiles
//     the designer painted often appear often in the output.
//   * Compatibility table: pattern A is compatible with pattern B at
//     direction d iff A and B agree on the (N-1)*N pixel overlap when
//     B is shifted by d. Built once per Generate (O(P^2 * N^2) where P
//     is pattern count, typically <100 for an 8x8 sample at N=2).
//   * Cells store *which patterns are still possible*. After collapse
//     we read the top-left pixel of each cell's chosen pattern and emit
//     it as the EntityType for that (x,y). That mirrors mxgmn's
//     reference choice and avoids the "which pixel of the pattern goes
//     where" ambiguity overlap models otherwise have.
//
// Reference: mxgmn/WaveFunctionCollapse/OverlappingModel.cs (MIT).
// Propagator structure inspired by shawnridgeway/wfc (MIT Go port).

package wfc

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
)

// SamplePatch is the designer-painted source the overlapping model
// learns from. Cells are row-major, length = Width*Height. Each
// EntityType must refer to a tile-kind entity type (the same vocabulary
// the socket engine uses). Cells with EntityType == 0 are "wildcards"
// and never become the top-left of an emitted pattern, but may appear
// in the body of one (the model treats them as "anything goes here").
type SamplePatch struct {
	Width, Height int32
	// Tiles is row-major: Tiles[y*Width+x].
	Tiles []EntityTypeID
}

// At returns the entity type at (x, y), or 0 if out of bounds.
func (s *SamplePatch) At(x, y int32) EntityTypeID {
	if x < 0 || y < 0 || x >= s.Width || y >= s.Height {
		return 0
	}
	return s.Tiles[y*s.Width+x]
}

// OverlappingOptions controls one GenerateOverlapping call.
type OverlappingOptions struct {
	// Sample is the designer-painted patch. Required; an empty sample
	// returns ErrEmptySample.
	Sample SamplePatch

	// PatternSize is N — the side length of each extracted pattern.
	// Larger N = more local context = more coherent output, but more
	// patterns and slower convergence. 2 is the practical sweet spot
	// for tile-based generation; 3 is also viable for big samples.
	// 0 = use OverlappingDefaultPatternSize.
	PatternSize int

	// Periodic, if true, treats the sample as a torus (wraps around at
	// the edges). Useful when the designer's sample patch is itself a
	// repeating texture. Default false.
	Periodic bool

	// Width, Height: output dimensions in cells. Required.
	Width, Height int32

	// Seed: deterministic seed.
	Seed uint64

	// Anchors: pre-collapsed cells. Same semantics as the socket
	// engine — coordinate (X, Y) in output space, EntityType is what
	// must appear there. Anchors whose entity type never appeared in
	// the sample are dropped (we can't satisfy them).
	Anchors Anchors

	// MaxReseeds caps retry attempts on contradiction. 0 = use the
	// package default (same as the socket engine).
	MaxReseeds int

	// BacktrackBudget caps total cell-collapse attempts per pass.
	// 0 = use the package default.
	BacktrackBudget int

	// Constraints are non-local properties the engine must satisfy.
	// Same semantics as GenerateOptions.Constraints (see generate.go).
	Constraints []Constraint
}

// OverlappingDefaultPatternSize is N when callers leave PatternSize at 0.
// Tuned for 8-32 cell sample patches; larger N over-constrains a small
// sample.
const OverlappingDefaultPatternSize = 2

// OverlappingResult is what GenerateOverlapping returns.
type OverlappingResult struct {
	Region *Region

	// PatternCount is how many distinct NxN patterns were extracted from
	// the sample. Surfaced so the UI can warn "your sample only produced
	// 1 pattern — try painting more variety."
	PatternCount int
}

// Errors specific to the overlapping engine.
var (
	ErrEmptySample        = errors.New("wfc: overlapping sample patch is empty")
	ErrSamplePatternsZero = errors.New("wfc: overlapping sample produced zero patterns (try a larger sample or smaller N)")
)

// pattern is one extracted NxN window from the sample. Cells are row-
// major: cells[r*N + c]. ID is the FNV-1a hash, which we use as a stable
// cross-run identifier so (sample, seed) remains deterministic.
type pattern struct {
	id     uint64
	cells  []EntityTypeID
	weight float64 // occurrence count in the sample
}

// patternSet holds every distinct pattern extracted from the sample plus
// the precomputed compatibility table.
type patternSet struct {
	n        int
	patterns []pattern

	// compat[i][edge] is the indices of patterns that fit on the `edge`
	// side of pattern i. "Fit" means the (N-1)*N overlap region matches
	// cell-for-cell when neighbour is shifted by one cell in `edge`'s
	// direction. Wildcards (EntityType 0) match anything.
	compat [][4][]int
}

// extractPatterns walks the sample with an NxN window and builds the
// pattern table + compatibility list. Returns ErrSamplePatternsZero if
// the sample is too small for the chosen N.
func extractPatterns(sample SamplePatch, n int, periodic bool) (*patternSet, error) {
	if n < 2 {
		n = 2
	}
	w, h := int(sample.Width), int(sample.Height)
	if w < n || h < n {
		if !periodic {
			return nil, ErrSamplePatternsZero
		}
	}

	maxX, maxY := w, h
	if !periodic {
		maxX = w - n + 1
		maxY = h - n + 1
	}
	if maxX <= 0 || maxY <= 0 {
		return nil, ErrSamplePatternsZero
	}

	byID := make(map[uint64]int) // id -> index in patterns
	var patterns []pattern

	for y := 0; y < maxY; y++ {
		for x := 0; x < maxX; x++ {
			cells := make([]EntityTypeID, n*n)
			for r := 0; r < n; r++ {
				for c := 0; c < n; c++ {
					cells[r*n+c] = sample.At(int32((x+c)%w), int32((y+r)%h))
				}
			}
			id := hashPattern(cells)
			if idx, ok := byID[id]; ok {
				patterns[idx].weight++
				continue
			}
			byID[id] = len(patterns)
			patterns = append(patterns, pattern{id: id, cells: cells, weight: 1})
		}
	}

	if len(patterns) == 0 {
		return nil, ErrSamplePatternsZero
	}

	ps := &patternSet{n: n, patterns: patterns}
	ps.compat = buildOverlapCompat(patterns, n)
	return ps, nil
}

// hashPattern is FNV-1a over the int64 sequence; deterministic across
// runs and process restarts. We accept the (vanishingly small) collision
// probability; a real collision just means two patterns merge and the
// designer sees one fewer variation.
func hashPattern(cells []EntityTypeID) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for _, et := range cells {
		v := uint64(et)
		buf[0] = byte(v)
		buf[1] = byte(v >> 8)
		buf[2] = byte(v >> 16)
		buf[3] = byte(v >> 24)
		buf[4] = byte(v >> 32)
		buf[5] = byte(v >> 40)
		buf[6] = byte(v >> 48)
		buf[7] = byte(v >> 56)
		h.Write(buf[:])
	}
	return h.Sum64()
}

// buildOverlapCompat fills the compatibility table. Pattern a fits next
// to pattern b on edge `edge` iff a and b agree on every cell of the
// (N-1)*N overlap region (shifted by edge's unit vector). Wildcards
// match anything.
func buildOverlapCompat(patterns []pattern, n int) [][4][]int {
	out := make([][4][]int, len(patterns))
	for i := range patterns {
		for edge := Edge(0); edge < 4; edge++ {
			dx, dy := edgeDelta(edge)
			var matches []int
			for j := range patterns {
				if overlapsAgree(patterns[i].cells, patterns[j].cells, n, dx, dy) {
					matches = append(matches, j)
				}
			}
			out[i][edge] = matches
		}
	}
	return out
}

// edgeDelta is the unit vector for `edge`. N=(0,-1), E=(1,0), S=(0,1),
// W=(-1,0). Used by the overlap test and by propagation.
func edgeDelta(edge Edge) (int, int) {
	switch edge {
	case EdgeN:
		return 0, -1
	case EdgeE:
		return 1, 0
	case EdgeS:
		return 0, 1
	case EdgeW:
		return -1, 0
	}
	return 0, 0
}

// overlapsAgree reports whether pattern a and pattern b (each NxN, row-
// major) are consistent when b is placed (dx, dy) cells away from a.
// The overlap region is the cells of a that fall under cells of b after
// the shift. EntityType 0 is a wildcard and always agrees.
func overlapsAgree(a, b []EntityTypeID, n, dx, dy int) bool {
	xMin := max2(0, dx)
	xMax := min2(n, n+dx)
	yMin := max2(0, dy)
	yMax := min2(n, n+dy)
	for r := yMin; r < yMax; r++ {
		for c := xMin; c < xMax; c++ {
			av := a[r*n+c]
			bv := b[(r-dy)*n+(c-dx)]
			if av == 0 || bv == 0 {
				continue
			}
			if av != bv {
				return false
			}
		}
	}
	return true
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GenerateOverlapping runs the overlapping model. Returns a fully
// collapsed Region or an error after MaxReseeds + 1 attempts.
//
// Determinism contract: same (Sample, options) → same Region.
func GenerateOverlapping(opts OverlappingOptions) (*OverlappingResult, error) {
	if opts.Sample.Width <= 0 || opts.Sample.Height <= 0 || len(opts.Sample.Tiles) == 0 {
		return nil, ErrEmptySample
	}
	if opts.Width <= 0 || opts.Height <= 0 {
		return nil, ErrInvalidRegion
	}
	if opts.PatternSize == 0 {
		opts.PatternSize = OverlappingDefaultPatternSize
	}
	if opts.MaxReseeds == 0 {
		opts.MaxReseeds = defaultMaxReseeds
	}
	if opts.BacktrackBudget == 0 {
		opts.BacktrackBudget = defaultBacktrackBudget
	}

	ps, err := extractPatterns(opts.Sample, opts.PatternSize, opts.Periodic)
	if err != nil {
		return nil, err
	}

	// Pre-merge constraint pins/restricts (same shape as generate.go).
	ctrl := runConstraintsInit(opts.Width, opts.Height, opts.Constraints)
	mergedOpts := opts
	if pins := ctrl.Pins(); len(pins) > 0 {
		seen := make(map[[2]int32]struct{}, len(opts.Anchors.Cells))
		merged := make([]Cell, 0, len(opts.Anchors.Cells)+len(pins))
		for _, a := range opts.Anchors.Cells {
			seen[[2]int32{a.X, a.Y}] = struct{}{}
			merged = append(merged, a)
		}
		for _, p := range pins {
			if _, dup := seen[[2]int32{p.X, p.Y}]; dup {
				continue
			}
			merged = append(merged, p)
		}
		mergedOpts.Anchors = Anchors{Cells: merged}
	}
	restricts := ctrl.Restricts()

	currentSeed := opts.Seed
	for attempt := 0; attempt <= opts.MaxReseeds; attempt++ {
		region, err := tryOverlapping(ps, mergedOpts, currentSeed, restricts)
		if err == nil {
			if failed := verifyAll(region, opts.Constraints); failed < 0 {
				return &OverlappingResult{Region: region, PatternCount: len(ps.patterns)}, nil
			}
			// Constraint failure → reseed.
		}
		currentSeed = splitMix64(currentSeed + 0x9E3779B97F4A7C15)
	}
	return nil, fmt.Errorf("%w: %d attempts on seed=%d (%d cells, %d patterns, N=%d)",
		ErrTooManyReseeds, opts.MaxReseeds+1, opts.Seed,
		opts.Width*opts.Height, len(ps.patterns), opts.PatternSize)
}

// tryOverlapping is one pass. Returns errBudgetExhausted on contradiction
// or budget; the caller (GenerateOverlapping) handles reseed.
//
// `restricts` is the constraint-derived per-cell domain filter — for
// the overlapping engine "allowed entity types" means "allowed patterns
// whose top-left cell is one of those entity types".
func tryOverlapping(ps *patternSet, opts OverlappingOptions, seed uint64, restricts []constraintRestrict) (*Region, error) {
	rng := rand.New(rand.NewPCG(seed, seed^0xa5a5a5a5))

	w, h := int(opts.Width), int(opts.Height)
	cells := make([]wfcCell, w*h)
	allOpts := make([]int, len(ps.patterns))
	for i := range allOpts {
		allOpts[i] = i
	}
	for i := range cells {
		opt := make([]int, len(allOpts))
		copy(opt, allOpts)
		cells[i].options = opt
		recomputeOverlapWeights(&cells[i], ps)
	}

	// Apply constraint-derived restricts. Same flow as the socket
	// engine: trim the cell's option set, propagate.
	if len(restricts) > 0 {
		touched := make([]int, 0, len(restricts))
		for _, r := range restricts {
			if r.x < 0 || r.y < 0 || int(r.x) >= w || int(r.y) >= h {
				continue
			}
			ci := int(r.y)*w + int(r.x)
			if cells[ci].collapsed {
				continue
			}
			allowed := make(map[EntityTypeID]struct{}, len(r.allowed))
			for _, et := range r.allowed {
				allowed[et] = struct{}{}
			}
			filtered := cells[ci].options[:0]
			for _, idx := range cells[ci].options {
				et := ps.patterns[idx].cells[0]
				if _, ok := allowed[et]; ok {
					filtered = append(filtered, idx)
				}
			}
			cells[ci].options = filtered
			if len(cells[ci].options) == 0 {
				return nil, errBudgetExhausted
			}
			recomputeOverlapWeights(&cells[ci], ps)
			touched = append(touched, ci)
		}
		for _, ci := range touched {
			if !propagateOverlap(cells, ps, w, h, ci%w, ci/w) {
				return nil, errBudgetExhausted
			}
		}
	}

	// Anchors: keep only patterns whose top-left cell matches the
	// anchor's entity type. If an anchor's entity type never appears as
	// the top-left of any pattern in the sample, drop the anchor (the
	// designer painted something we can't satisfy).
	for _, a := range opts.Anchors.Cells {
		if a.X < 0 || a.Y < 0 || int(a.X) >= w || int(a.Y) >= h {
			continue
		}
		matching := patternsWithTopLeft(ps, a.EntityType)
		if len(matching) == 0 {
			continue
		}
		ci := int(a.Y)*w + int(a.X)
		cells[ci].options = matching
		// Don't mark collapsed yet — there may be several patterns whose
		// top-left is this entity type. Propagation prunes them further;
		// the lowest-entropy picker collapses to a single one later.
		recomputeOverlapWeights(&cells[ci], ps)
	}
	// Propagate from anchors before search starts.
	for _, a := range opts.Anchors.Cells {
		if a.X < 0 || a.Y < 0 || int(a.X) >= w || int(a.Y) >= h {
			continue
		}
		if !propagateOverlap(cells, ps, w, h, int(a.X), int(a.Y)) {
			return nil, errBudgetExhausted
		}
	}

	budget := opts.BacktrackBudget
	for {
		ci, found := pickLowestEntropy(cells, rng)
		if !found {
			break
		}
		budget--
		if budget < 0 {
			return nil, errBudgetExhausted
		}
		chosen := weightedPick(&cells[ci], rng)
		cells[ci].collapsed = true
		cells[ci].chosen = chosen
		cells[ci].options = []int{chosen}
		recomputeOverlapWeights(&cells[ci], ps)
		if !propagateOverlap(cells, ps, w, h, ci%w, ci/w) {
			return nil, errBudgetExhausted
		}
	}

	// Build output: each cell's emitted entity type is the top-left
	// cell of its chosen pattern.
	out := &Region{Width: opts.Width, Height: opts.Height, Cells: make([]Cell, 0, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if !c.collapsed {
				return nil, errBudgetExhausted
			}
			et := ps.patterns[c.chosen].cells[0]
			out.Cells = append(out.Cells, Cell{
				X: int32(x), Y: int32(y), EntityType: et,
			})
		}
	}
	return out, nil
}

// patternsWithTopLeft returns the indices of patterns whose top-left
// cell is `et`. Used for anchor-constraint setup. Result is sorted
// ascending so intersectSorted preconditions hold.
func patternsWithTopLeft(ps *patternSet, et EntityTypeID) []int {
	var out []int
	for i, p := range ps.patterns {
		if p.cells[0] == et {
			out = append(out, i)
		}
	}
	insertionSortInts(out)
	return out
}

// recomputeOverlapWeights mirrors recomputeWeights for the overlapping
// engine. Pattern weight is its sample-occurrence count.
func recomputeOverlapWeights(cell *wfcCell, ps *patternSet) {
	cell.cumWeights = cell.cumWeights[:0]
	if cap(cell.cumWeights) < len(cell.options) {
		cell.cumWeights = make([]float64, 0, len(cell.options))
	}
	cum := 0.0
	for _, idx := range cell.options {
		w := ps.patterns[idx].weight
		if w <= 0 {
			w = 1
		}
		cum += w
		cell.cumWeights = append(cell.cumWeights, cum)
	}
	cell.totalWeight = cum
}

// propagateOverlap is the AC-style propagation step. Cells store pattern
// option sets (not tile sets); compatibility is the precomputed overlap
// table.
func propagateOverlap(cells []wfcCell, ps *patternSet, w, h, sx, sy int) bool {
	type frontierEntry struct{ x, y int }
	frontier := []frontierEntry{{sx, sy}}

	for len(frontier) > 0 {
		curr := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		ci := curr.y*w + curr.x
		currOpts := cells[ci].options

		for edge := Edge(0); edge < 4; edge++ {
			dx, dy := edgeDelta(edge)
			nx, ny := curr.x+dx, curr.y+dy
			if nx < 0 || ny < 0 || nx >= w || ny >= h {
				continue
			}
			nci := ny*w + nx
			if cells[nci].collapsed {
				continue
			}
			allowed := overlapAllowedFromEdge(currOpts, ps, edge)
			before := len(cells[nci].options)
			cells[nci].options = intersectSorted(cells[nci].options, allowed)
			if len(cells[nci].options) == 0 {
				return false
			}
			if len(cells[nci].options) != before {
				recomputeOverlapWeights(&cells[nci], ps)
				frontier = append(frontier, frontierEntry{nx, ny})
			}
		}
	}
	return true
}

// overlapAllowedFromEdge returns the sorted union of patterns that can
// sit on `edge` of any pattern in `currOpts`.
func overlapAllowedFromEdge(currOpts []int, ps *patternSet, edge Edge) []int {
	seen := make(map[int]struct{}, 16)
	for _, idx := range currOpts {
		for _, ok := range ps.compat[idx][edge] {
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
