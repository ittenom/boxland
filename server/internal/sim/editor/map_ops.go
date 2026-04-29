package editor

import (
	"context"
	"fmt"

	mapsservice "boxland/server/internal/maps"
)

// map_ops.go — concrete `Op` implementations for the mapmaker
// surface. Each op persists through `maps.Service` and computes
// the inverse needed for undo (capturing the pre-image at Apply
// time so the inverse can restore exactly what was there).
//
// Multi-cell strokes (a brush drag covering 30 cells, a rect tool
// flood, a fill) compose via `compositeOp`: one outer Op holding
// many inner ops, applied + reversed atomically.
//
// Diff fan-out: a stroke that touches N cells emits N Diffs (one
// per cell) so siblings can apply them incrementally + so the
// renderer's per-cell sprite cache can react cheaply. The session
// broadcasts each diff to every subscriber.

// ---- PlaceTilesOp ---------------------------------------------------

// PlaceTilesOp upserts a batch of tiles. Its Apply captures the
// pre-image of every cell so Inverse() can restore the exact
// previous state (whether each cell was empty or had a tile).
type PlaceTilesOp struct {
	MapID int64
	Tiles []mapsservice.Tile

	prevTiles map[tileKey]*mapsservice.Tile // nil entry = was empty
}

func (o *PlaceTilesOp) Apply(ctx context.Context, deps Deps) (Diff, error) {
	if deps.Maps == nil {
		return Diff{}, fmt.Errorf("PlaceTilesOp: Maps service required")
	}
	if len(o.Tiles) == 0 {
		return Diff{Kind: DiffNone}, nil
	}
	// Pre-image: read existing tiles in the bounding box, then
	// build a per-cell map so Inverse can restore exactly what
	// was there. ChunkTiles is a single SQL query so this is a
	// fixed-cost lookup regardless of stroke length.
	if err := o.capturePreImage(ctx, deps); err != nil {
		return Diff{}, err
	}
	if err := deps.Maps.PlaceTiles(ctx, o.Tiles); err != nil {
		return Diff{}, fmt.Errorf("place tiles: %w", err)
	}
	// Headline diff is the first tile; the rest fan out via the
	// MultiDiffOp interface so subscribers see one diff per cell.
	headline := o.Tiles[0]
	return Diff{
		Kind: DiffTilePlaced,
		Body: &headline,
	}, nil
}

// ExtraDiffs implements MultiDiffOp: emit a diff per remaining
// tile so siblings can apply the full stroke incrementally.
func (o *PlaceTilesOp) ExtraDiffs() []Diff {
	if len(o.Tiles) <= 1 {
		return nil
	}
	out := make([]Diff, 0, len(o.Tiles)-1)
	for i := 1; i < len(o.Tiles); i++ {
		t := o.Tiles[i]
		out = append(out, Diff{Kind: DiffTilePlaced, Body: &t})
	}
	return out
}

func (o *PlaceTilesOp) Inverse(_ context.Context, _ Deps) (Op, error) {
	if o.prevTiles == nil {
		return nil, fmt.Errorf("PlaceTilesOp.Inverse: Apply not run")
	}
	// Group cells by what the inverse looks like:
	//   * cells that were empty before -> EraseTilesOp
	//   * cells that had something before -> PlaceTilesOp with the
	//     pre-image rows.
	var (
		erasePoints = map[int64][][2]int32{} // layerID -> points
		restoreTiles []mapsservice.Tile
	)
	for k, prev := range o.prevTiles {
		if prev == nil {
			erasePoints[k.layerID] = append(erasePoints[k.layerID], [2]int32{k.x, k.y})
		} else {
			restoreTiles = append(restoreTiles, *prev)
		}
	}
	children := make([]Op, 0, len(erasePoints)+1)
	for layerID, points := range erasePoints {
		children = append(children, &EraseTilesOp{
			MapID: o.MapID, LayerID: layerID, Points: points,
		})
	}
	if len(restoreTiles) > 0 {
		children = append(children, &PlaceTilesOp{
			MapID: o.MapID, Tiles: restoreTiles,
		})
	}
	if len(children) == 0 {
		return &noopOp{}, nil
	}
	if len(children) == 1 {
		return children[0], nil
	}
	return &compositeOp{children: children, label: fmt.Sprintf("inverse of place×%d", len(o.Tiles))}, nil
}

func (o *PlaceTilesOp) Describe() string {
	return fmt.Sprintf("place tiles ×%d on map=%d", len(o.Tiles), o.MapID)
}

// capturePreImage reads the current state of every cell the op
// will touch + stores it in o.prevTiles so Inverse can restore.
func (o *PlaceTilesOp) capturePreImage(ctx context.Context, deps Deps) error {
	o.prevTiles = make(map[tileKey]*mapsservice.Tile, len(o.Tiles))
	for _, t := range o.Tiles {
		o.prevTiles[tileKey{layerID: t.LayerID, x: t.X, y: t.Y}] = nil
	}
	x0, y0, x1, y1 := bboxOf(o.Tiles, func(t mapsservice.Tile) (int32, int32) { return t.X, t.Y })
	existing, err := deps.Maps.ChunkTiles(ctx, o.MapID, x0, y0, x1, y1)
	if err != nil {
		return fmt.Errorf("place tiles: pre-image: %w", err)
	}
	for i := range existing {
		k := tileKey{layerID: existing[i].LayerID, x: existing[i].X, y: existing[i].Y}
		if _, want := o.prevTiles[k]; want {
			snap := existing[i]
			o.prevTiles[k] = &snap
		}
	}
	return nil
}

// ---- EraseTilesOp --------------------------------------------------

// EraseTilesOp deletes a batch of tiles on a single layer. Pre-image
// captured at Apply for Inverse to restore.
type EraseTilesOp struct {
	MapID   int64
	LayerID int64
	Points  [][2]int32 // [x, y]

	prevTiles []mapsservice.Tile // captured rows that actually existed
}

func (o *EraseTilesOp) Apply(ctx context.Context, deps Deps) (Diff, error) {
	if deps.Maps == nil {
		return Diff{}, fmt.Errorf("EraseTilesOp: Maps service required")
	}
	if len(o.Points) == 0 {
		return Diff{Kind: DiffNone}, nil
	}
	x0, y0, x1, y1 := bboxOfPoints(o.Points)
	existing, err := deps.Maps.ChunkTiles(ctx, o.MapID, x0, y0, x1, y1)
	if err != nil {
		return Diff{}, fmt.Errorf("erase tiles: pre-image: %w", err)
	}
	want := make(map[[3]int32]struct{}, len(o.Points))
	for _, p := range o.Points {
		want[[3]int32{int32(o.LayerID), p[0], p[1]}] = struct{}{}
	}
	o.prevTiles = nil
	for i := range existing {
		key := [3]int32{int32(existing[i].LayerID), existing[i].X, existing[i].Y}
		if _, ok := want[key]; ok {
			o.prevTiles = append(o.prevTiles, existing[i])
		}
	}
	if err := deps.Maps.EraseTiles(ctx, o.MapID, o.LayerID, o.Points); err != nil {
		return Diff{}, fmt.Errorf("erase tiles: %w", err)
	}
	return Diff{
		Kind: DiffTileErased,
		Body: TileErasedBody{LayerID: int32(o.LayerID), X: o.Points[0][0], Y: o.Points[0][1]},
	}, nil
}

func (o *EraseTilesOp) ExtraDiffs() []Diff {
	if len(o.Points) <= 1 {
		return nil
	}
	out := make([]Diff, 0, len(o.Points)-1)
	for i := 1; i < len(o.Points); i++ {
		out = append(out, Diff{
			Kind: DiffTileErased,
			Body: TileErasedBody{LayerID: int32(o.LayerID), X: o.Points[i][0], Y: o.Points[i][1]},
		})
	}
	return out
}

func (o *EraseTilesOp) Inverse(_ context.Context, _ Deps) (Op, error) {
	if len(o.prevTiles) == 0 {
		// Nothing was actually erased -> noop inverse.
		return &noopOp{}, nil
	}
	return &PlaceTilesOp{MapID: o.MapID, Tiles: append([]mapsservice.Tile(nil), o.prevTiles...)}, nil
}

func (o *EraseTilesOp) Describe() string {
	return fmt.Sprintf("erase tiles ×%d on map=%d layer=%d", len(o.Points), o.MapID, o.LayerID)
}

// ---- LockTilesOp / UnlockTilesOp ------------------------------------

// LockTilesOp upserts a batch of locked cells. Locks survive
// procedural regeneration; the mapmaker's Lock brush writes them.
type LockTilesOp struct {
	MapID int64
	Cells []mapsservice.LockedCell

	prevCells map[lockKey]*mapsservice.LockedCell
}

func (o *LockTilesOp) Apply(ctx context.Context, deps Deps) (Diff, error) {
	if deps.Maps == nil {
		return Diff{}, fmt.Errorf("LockTilesOp: Maps service required")
	}
	if len(o.Cells) == 0 {
		return Diff{Kind: DiffNone}, nil
	}
	if err := o.capturePreImage(ctx, deps); err != nil {
		return Diff{}, err
	}
	if err := deps.Maps.LockCells(ctx, o.Cells); err != nil {
		return Diff{}, fmt.Errorf("lock cells: %w", err)
	}
	headline := o.Cells[0]
	return Diff{
		Kind: DiffLockAdded,
		Body: &headline,
	}, nil
}

func (o *LockTilesOp) ExtraDiffs() []Diff {
	if len(o.Cells) <= 1 {
		return nil
	}
	out := make([]Diff, 0, len(o.Cells)-1)
	for i := 1; i < len(o.Cells); i++ {
		c := o.Cells[i]
		out = append(out, Diff{Kind: DiffLockAdded, Body: &c})
	}
	return out
}

func (o *LockTilesOp) Inverse(_ context.Context, _ Deps) (Op, error) {
	if o.prevCells == nil {
		return nil, fmt.Errorf("LockTilesOp.Inverse: Apply not run")
	}
	var (
		unlockPoints = map[int64][][2]int32{}
		restoreCells []mapsservice.LockedCell
	)
	for k, prev := range o.prevCells {
		if prev == nil {
			unlockPoints[k.layerID] = append(unlockPoints[k.layerID], [2]int32{k.x, k.y})
		} else {
			restoreCells = append(restoreCells, *prev)
		}
	}
	children := make([]Op, 0, len(unlockPoints)+1)
	for layerID, points := range unlockPoints {
		children = append(children, &UnlockTilesOp{
			MapID: o.MapID, LayerID: layerID, Points: points,
		})
	}
	if len(restoreCells) > 0 {
		children = append(children, &LockTilesOp{
			MapID: o.MapID, Cells: restoreCells,
		})
	}
	if len(children) == 0 {
		return &noopOp{}, nil
	}
	if len(children) == 1 {
		return children[0], nil
	}
	return &compositeOp{children: children, label: fmt.Sprintf("inverse of lock×%d", len(o.Cells))}, nil
}

func (o *LockTilesOp) Describe() string {
	return fmt.Sprintf("lock cells ×%d on map=%d", len(o.Cells), o.MapID)
}

func (o *LockTilesOp) capturePreImage(ctx context.Context, deps Deps) error {
	o.prevCells = make(map[lockKey]*mapsservice.LockedCell, len(o.Cells))
	want := make(map[int64]struct{}, 4) // distinct layers we'll need to query
	for _, c := range o.Cells {
		o.prevCells[lockKey{layerID: c.LayerID, x: c.X, y: c.Y}] = nil
		want[c.LayerID] = struct{}{}
	}
	for layerID := range want {
		existing, err := deps.Maps.LockedCellsForLayer(ctx, o.MapID, layerID)
		if err != nil {
			return fmt.Errorf("lock cells: pre-image: %w", err)
		}
		for i := range existing {
			k := lockKey{layerID: existing[i].LayerID, x: existing[i].X, y: existing[i].Y}
			if _, w := o.prevCells[k]; w {
				snap := existing[i]
				o.prevCells[k] = &snap
			}
		}
	}
	return nil
}

// UnlockTilesOp removes a batch of locked cells on a single layer.
type UnlockTilesOp struct {
	MapID   int64
	LayerID int64
	Points  [][2]int32

	prevCells []mapsservice.LockedCell
}

func (o *UnlockTilesOp) Apply(ctx context.Context, deps Deps) (Diff, error) {
	if deps.Maps == nil {
		return Diff{}, fmt.Errorf("UnlockTilesOp: Maps service required")
	}
	if len(o.Points) == 0 {
		return Diff{Kind: DiffNone}, nil
	}
	existing, err := deps.Maps.LockedCellsForLayer(ctx, o.MapID, o.LayerID)
	if err != nil {
		return Diff{}, fmt.Errorf("unlock cells: pre-image: %w", err)
	}
	want := make(map[[2]int32]struct{}, len(o.Points))
	for _, p := range o.Points {
		want[[2]int32{p[0], p[1]}] = struct{}{}
	}
	o.prevCells = nil
	for i := range existing {
		key := [2]int32{existing[i].X, existing[i].Y}
		if _, ok := want[key]; ok {
			o.prevCells = append(o.prevCells, existing[i])
		}
	}
	if err := deps.Maps.UnlockCells(ctx, o.MapID, o.LayerID, o.Points); err != nil {
		return Diff{}, fmt.Errorf("unlock cells: %w", err)
	}
	return Diff{
		Kind: DiffLockRemoved,
		Body: TileErasedBody{LayerID: int32(o.LayerID), X: o.Points[0][0], Y: o.Points[0][1]},
	}, nil
}

func (o *UnlockTilesOp) ExtraDiffs() []Diff {
	if len(o.Points) <= 1 {
		return nil
	}
	out := make([]Diff, 0, len(o.Points)-1)
	for i := 1; i < len(o.Points); i++ {
		out = append(out, Diff{
			Kind: DiffLockRemoved,
			Body: TileErasedBody{LayerID: int32(o.LayerID), X: o.Points[i][0], Y: o.Points[i][1]},
		})
	}
	return out
}

func (o *UnlockTilesOp) Inverse(_ context.Context, _ Deps) (Op, error) {
	if len(o.prevCells) == 0 {
		return &noopOp{}, nil
	}
	return &LockTilesOp{MapID: o.MapID, Cells: append([]mapsservice.LockedCell(nil), o.prevCells...)}, nil
}

func (o *UnlockTilesOp) Describe() string {
	return fmt.Sprintf("unlock cells ×%d on map=%d layer=%d", len(o.Points), o.MapID, o.LayerID)
}

// ---- compositeOp ----------------------------------------------------

// NewComposite wraps a sequence of child ops as one undo-able
// unit. Used by the WS handlers when a single user action splits
// across multiple service calls (e.g. erase across two layers in
// one stroke).
func NewComposite(label string, children []Op) Op {
	if len(children) == 0 {
		return &noopOp{}
	}
	if len(children) == 1 {
		return children[0]
	}
	return &compositeOp{children: children, label: label}
}

// compositeOp runs a sequence of child ops in order. Apply emits
// the headline diff of the first child + relies on the session's
// per-child fan-out (we're inside one Apply lock so the children
// are serialized cleanly). Inverse reverses the order so undo
// peels off the most recent child first.
type compositeOp struct {
	children []Op
	label    string

	// applied[i] true => Apply succeeded on children[i]; only those
	// children participate in Inverse (a partial failure leaves
	// the persisted DB in a half-applied state, which is the same
	// guarantee the existing levels-service ops have).
	applied []bool
}

func (o *compositeOp) Apply(ctx context.Context, deps Deps) (Diff, error) {
	o.applied = make([]bool, len(o.children))
	var headline Diff
	for i, c := range o.children {
		d, err := c.Apply(ctx, deps)
		if err != nil {
			return Diff{}, fmt.Errorf("composite child %d: %w", i, err)
		}
		o.applied[i] = true
		if i == 0 {
			headline = d
		}
	}
	if len(o.children) == 0 {
		return Diff{Kind: DiffNone}, nil
	}
	return headline, nil
}

func (o *compositeOp) Inverse(ctx context.Context, deps Deps) (Op, error) {
	inverses := make([]Op, 0, len(o.children))
	for i := len(o.children) - 1; i >= 0; i-- {
		if !o.applied[i] {
			continue
		}
		inv, err := o.children[i].Inverse(ctx, deps)
		if err != nil {
			return nil, fmt.Errorf("composite inverse child %d: %w", i, err)
		}
		inverses = append(inverses, inv)
	}
	if len(inverses) == 0 {
		return &noopOp{}, nil
	}
	if len(inverses) == 1 {
		return inverses[0], nil
	}
	return &compositeOp{children: inverses, label: "inverse of " + o.label}, nil
}

func (o *compositeOp) Describe() string {
	return fmt.Sprintf("composite[%s]×%d", o.label, len(o.children))
}

// noopOp is the "nothing to undo" sentinel — safe to push onto the
// undo stack so the surface's depth counter stays accurate even
// when an op's net effect was empty (e.g. erasing already-empty
// cells).
type noopOp struct{}

func (noopOp) Apply(_ context.Context, _ Deps) (Diff, error) {
	return Diff{Kind: DiffHistoryChanged}, nil
}
func (noopOp) Inverse(_ context.Context, _ Deps) (Op, error) {
	return noopOp{}, nil
}
func (noopOp) Describe() string { return "noop" }

// TileErasedBody is the typed Body for DiffTileErased + DiffLockRemoved.
// Carries the (layer, x, y) coordinate of the cell that was cleared.
// The WS encoder turns this into an EditorMapTilePoint FlatBuffer.
type TileErasedBody struct {
	LayerID int32
	X, Y    int32
}

// ---- helpers --------------------------------------------------------

type tileKey struct {
	layerID int64
	x, y    int32
}

type lockKey struct {
	layerID int64
	x, y    int32
}

func bboxOf[T any](items []T, get func(T) (int32, int32)) (int32, int32, int32, int32) {
	if len(items) == 0 {
		return 0, 0, 0, 0
	}
	x0, y0 := get(items[0])
	x1, y1 := x0, y0
	for _, it := range items[1:] {
		x, y := get(it)
		if x < x0 {
			x0 = x
		}
		if y < y0 {
			y0 = y
		}
		if x > x1 {
			x1 = x
		}
		if y > y1 {
			y1 = y
		}
	}
	return x0, y0, x1, y1
}

func bboxOfPoints(points [][2]int32) (int32, int32, int32, int32) {
	if len(points) == 0 {
		return 0, 0, 0, 0
	}
	x0, y0 := points[0][0], points[0][1]
	x1, y1 := x0, y0
	for _, p := range points[1:] {
		if p[0] < x0 {
			x0 = p[0]
		}
		if p[1] < y0 {
			y0 = p[1]
		}
		if p[0] > x1 {
			x1 = p[0]
		}
		if p[1] > y1 {
			y1 = p[1]
		}
	}
	return x0, y0, x1, y1
}
