// Boxland — non-local constraints for WFC.
//
// The base socket / overlapping engines only enforce LOCAL constraints
// (adjacent cells must agree). Many "looks realistic" properties of a
// generated map are GLOBAL — "all border cells should be water", "every
// path tile must be reachable from every other path tile". Encoding
// these via tile sockets blows up the vocabulary; constraint primitives
// keep the engine clean and let designers compose properties.
//
// Inspiration: BorisTheBrave/DeBroglie's `ITileConstraint` (MIT). We
// don't lift their full `Init/Check` controller (that requires exposing
// the propagator's Ban/Select API mid-search) — instead Boxland's
// constraints fire at two points around the existing engine:
//
//   * ApplyInitial(ctrl) is called BEFORE search. It can pre-collapse
//     cells (anchors) or trim the option sets of specific cells. This
//     is enough for hard initialization constraints like "borders must
//     be tile X". The same retry-on-contradiction loop that wraps
//     tryGenerate handles unsolvable initial constraints.
//
//   * Verify(region) is called AFTER each successful collapse. If it
//     returns false the engine treats the result as a contradiction
//     and reseeds. This is how non-local constraints like PathConstraint
//     (require every "ground" cell to be 4-connected) ship without
//     needing mid-search hooks.
//
// This is strictly less powerful than DeBroglie's mid-search constraint
// model (it can spend extra reseeds when Verify fails late). The
// trade-off is that the engine code stays small and the constraint
// authoring surface stays simple. Phase 3 may add a mid-search hook if
// a concrete constraint demands it.

package wfc

// ConstraintInitController is what ApplyInitial gets to mutate. It's a
// narrow surface intentionally — constraints can pin a cell's entity
// type (Pin) or restrict the candidate entity types at a cell (Restrict),
// and that's it. Pin/Restrict are *advisory*: the engine treats them as
// pre-collapse anchors / domain hints and runs propagation from there.
type ConstraintInitController struct {
	width, height int32
	pins          []Cell
	restricts     []constraintRestrict
}

type constraintRestrict struct {
	x, y    int32
	allowed []EntityTypeID
}

// Width / Height are the output dimensions, exposed so constraints can
// iterate over cells.
func (c *ConstraintInitController) Width() int32  { return c.width }
func (c *ConstraintInitController) Height() int32 { return c.height }

// Pin forces the cell at (x, y) to entity type et. Out-of-bounds pins
// are silently ignored (matches anchor semantics elsewhere).
func (c *ConstraintInitController) Pin(x, y int32, et EntityTypeID) {
	if x < 0 || y < 0 || x >= c.width || y >= c.height {
		return
	}
	c.pins = append(c.pins, Cell{X: x, Y: y, EntityType: et})
}

// Restrict caps the cell at (x, y) to only the listed entity types.
// Multiple Restrict calls on the same cell intersect. An empty allowed
// list is treated as "no restriction" (the constraint chose not to
// constrain this cell) so callers don't accidentally make the cell
// unsolvable by passing an empty filter.
func (c *ConstraintInitController) Restrict(x, y int32, allowed []EntityTypeID) {
	if x < 0 || y < 0 || x >= c.width || y >= c.height || len(allowed) == 0 {
		return
	}
	cp := make([]EntityTypeID, len(allowed))
	copy(cp, allowed)
	c.restricts = append(c.restricts, constraintRestrict{x: x, y: y, allowed: cp})
}

// Pins returns the accumulated pin list (read-only; aliases internal
// storage). The engine consumes this after every constraint has run.
func (c *ConstraintInitController) Pins() []Cell { return c.pins }

// Restricts returns the accumulated restrict list (read-only).
func (c *ConstraintInitController) Restricts() []constraintRestrict {
	return c.restricts
}

// Constraint is the interface every constraint implements. Both methods
// are optional — a no-op default is fine. Constraints are stateless
// from the engine's perspective; if they need state they can keep it
// privately.
type Constraint interface {
	// ApplyInitial runs once before search. Use ctrl.Pin / ctrl.Restrict
	// to seed the engine. Called in the order constraints were given to
	// GenerateOptions / OverlappingOptions.
	ApplyInitial(ctrl *ConstraintInitController)

	// Verify runs once after a candidate Region has been fully
	// collapsed. Return true to accept; false triggers a reseed in the
	// outer Generate / GenerateOverlapping loop. Stateless — same
	// (Region) → same result.
	Verify(region *Region) bool
}

// ---- BorderConstraint ----

// BorderEdgeMask selects which sides of the output the constraint
// applies to. Combine with bit-OR: BorderTop | BorderBottom.
type BorderEdgeMask uint8

const (
	BorderTop    BorderEdgeMask = 1 << iota // y == 0
	BorderRight                              // x == Width-1
	BorderBottom                             // y == Height-1
	BorderLeft                               // x == 0
	BorderAll    = BorderTop | BorderRight | BorderBottom | BorderLeft
)

// BorderConstraint pins (or, optionally, restricts) cells along the
// selected edges to the given entity type. The "border = water" /
// "border = wall" idiom for outdoor / dungeon maps respectively.
//
// Mode semantics:
//   * Pin: every selected border cell becomes an anchor with the given
//          entity type. Strict; will trigger a reseed if the engine
//          can't satisfy it.
//   * Restrict: every selected border cell has its options trimmed so
//          the chosen entity type is among the legal options. Softer.
type BorderConstraint struct {
	EntityType EntityTypeID
	Edges      BorderEdgeMask
	// Restrict, when true, uses Restrict instead of Pin for each cell.
	// Useful when the entity type is one of several "border-friendly"
	// tiles and the engine should pick which one goes where.
	Restrict bool
}

// ApplyInitial visits every border cell in the selected edges and
// either pins or restricts it.
func (b *BorderConstraint) ApplyInitial(ctrl *ConstraintInitController) {
	w, h := ctrl.Width(), ctrl.Height()
	apply := func(x, y int32) {
		if b.Restrict {
			ctrl.Restrict(x, y, []EntityTypeID{b.EntityType})
		} else {
			ctrl.Pin(x, y, b.EntityType)
		}
	}
	if b.Edges&BorderTop != 0 {
		for x := int32(0); x < w; x++ {
			apply(x, 0)
		}
	}
	if b.Edges&BorderBottom != 0 {
		for x := int32(0); x < w; x++ {
			apply(x, h-1)
		}
	}
	if b.Edges&BorderLeft != 0 {
		// Skip corners already covered by Top/Bottom to avoid double-
		// pinning the same cell (harmless but tidier).
		yStart := int32(0)
		yEnd := h
		if b.Edges&BorderTop != 0 {
			yStart = 1
		}
		if b.Edges&BorderBottom != 0 {
			yEnd = h - 1
		}
		for y := yStart; y < yEnd; y++ {
			apply(0, y)
		}
	}
	if b.Edges&BorderRight != 0 {
		yStart := int32(0)
		yEnd := h
		if b.Edges&BorderTop != 0 {
			yStart = 1
		}
		if b.Edges&BorderBottom != 0 {
			yEnd = h - 1
		}
		for y := yStart; y < yEnd; y++ {
			apply(w-1, y)
		}
	}
}

// Verify is a no-op for BorderConstraint — Pin/Restrict at init is the
// whole story. (If the engine couldn't satisfy a Pin, the cell would
// have caused a contradiction and been reseeded at that level.)
func (b *BorderConstraint) Verify(region *Region) bool { return true }

// ---- PathConstraint ----

// PathConstraint guarantees that every cell whose entity type is in
// PathTypes is 4-connected to every other path cell. The "no
// fragmented islands of grass" property — the perceptual fix for the
// noisy outputs in the original screenshots.
//
// Implementation is a single flood-fill over the collapsed region;
// runs in O(W * H). When Verify returns false the outer Generate loop
// reseeds.
type PathConstraint struct {
	// PathTypes is the set of entity types that count as path cells.
	// Empty means "every non-zero cell" (the constraint becomes "the
	// entire map is connected" — useful for purely-walkable layouts).
	PathTypes []EntityTypeID
}

// ApplyInitial is a no-op for PathConstraint — connectivity is a global
// property that can only be checked once we have a full Region.
func (p *PathConstraint) ApplyInitial(ctrl *ConstraintInitController) {}

// Verify runs a flood-fill from the first path cell and returns true
// iff every other path cell was reached.
func (p *PathConstraint) Verify(region *Region) bool {
	if region == nil || len(region.Cells) == 0 {
		return true
	}
	w, h := int(region.Width), int(region.Height)

	// Index region.Cells by (x, y) for O(1) lookup. Region.Cells is
	// row-major (Generate / GenerateOverlapping guarantee this) so the
	// straight index works without a map.
	isPath := make([]bool, w*h)
	totalPath := 0
	allowed := pathTypeSet(p.PathTypes)
	for _, c := range region.Cells {
		idx := int(c.Y)*w + int(c.X)
		if idx < 0 || idx >= len(isPath) {
			continue
		}
		if pathTypeMatches(allowed, c.EntityType) {
			isPath[idx] = true
			totalPath++
		}
	}
	if totalPath <= 1 {
		// 0 or 1 path cells are trivially connected.
		return true
	}

	// Flood-fill from the first path cell.
	startIdx := -1
	for i, ok := range isPath {
		if ok {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		return true
	}
	visited := make([]bool, w*h)
	stack := make([]int, 0, totalPath)
	stack = append(stack, startIdx)
	visited[startIdx] = true
	reached := 1
	for len(stack) > 0 {
		curr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		x := curr % w
		y := curr / w
		for _, d := range [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}} {
			nx, ny := x+d[0], y+d[1]
			if nx < 0 || ny < 0 || nx >= w || ny >= h {
				continue
			}
			ni := ny*w + nx
			if visited[ni] || !isPath[ni] {
				continue
			}
			visited[ni] = true
			reached++
			stack = append(stack, ni)
		}
	}
	return reached == totalPath
}

// pathTypeSet returns nil for the "any non-zero counts" case (empty
// input) and a small lookup map otherwise. A linear scan of the slice
// would be cheaper for ≤4 entries but the map keeps the call site
// branch-free.
func pathTypeSet(types []EntityTypeID) map[EntityTypeID]struct{} {
	if len(types) == 0 {
		return nil
	}
	m := make(map[EntityTypeID]struct{}, len(types))
	for _, et := range types {
		m[et] = struct{}{}
	}
	return m
}

func pathTypeMatches(allowed map[EntityTypeID]struct{}, et EntityTypeID) bool {
	if et == 0 {
		return false
	}
	if allowed == nil {
		return true
	}
	_, ok := allowed[et]
	return ok
}

// ---- engine helpers ----

// runConstraintsInit folds a slice of constraints into a populated
// ConstraintInitController. The engine then drains pins/restricts into
// the wfcCell array before search starts.
func runConstraintsInit(width, height int32, constraints []Constraint) *ConstraintInitController {
	ctrl := &ConstraintInitController{width: width, height: height}
	for _, c := range constraints {
		if c == nil {
			continue
		}
		c.ApplyInitial(ctrl)
	}
	return ctrl
}

// verifyAll runs every constraint's Verify and returns the first one
// that failed (by index, for diagnostics) or -1 on success.
func verifyAll(region *Region, constraints []Constraint) int {
	for i, c := range constraints {
		if c == nil {
			continue
		}
		if !c.Verify(region) {
			return i
		}
	}
	return -1
}
