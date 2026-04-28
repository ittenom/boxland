// Boxland — procedural-Mapmaker preview API.
//
// Bridges the maps service (designer authoring surface) with the WFC
// engine (internal/maps/wfc). The Mapmaker UI calls this every time
// the designer changes the seed, the anchors, or the size, so it
// must be FAST: the project's tile-kind entity types and their edge
// sockets are loaded in TWO queries (no N+1 — see PLAN.md §scale-
// concerns), then handed to wfc.Generate.

package maps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/maps/wfc"
)

// ProceduralPreviewInput drives GenerateProceduralPreview.
type ProceduralPreviewInput struct {
	// MapID is required so the service can load the map's gen_algorithm
	// + locked cells. Pre-existing zero-value callers (none in tree)
	// keep working — they just don't get lock anchors mixed in.
	MapID int64

	// Width/Height are in tiles. The Mapmaker enforces sensible limits
	// upstream (typically 16..256); this method only checks > 0.
	Width, Height int32

	// Seed lets the designer iterate ("reroll") deterministically.
	Seed uint64

	// Anchors are designer-painted cells in WORLD coordinates. The
	// caller is responsible for translating designer-region paints
	// into individual cell anchors before invoking. The service
	// additionally merges any locked cells stored under MapID so the
	// caller doesn't have to re-fetch them.
	Anchors []wfc.Cell

	// MaxReseeds caps WFC retry attempts per call. 0 = WFC default.
	MaxReseeds int

}

// ProceduralPreviewResult is what the Mapmaker renders as a ghost overlay.
type ProceduralPreviewResult struct {
	Region *wfc.Region

	// TileSetSize is the number of distinct tile-kind entity types
	// considered. Surfaced so the UI can warn "you only have 1 tile
	// in your project" when a generation looks degenerate.
	TileSetSize int

	// Algorithm is the engine that actually ran. Echoed so the UI can
	// confirm an algorithm override took effect (or warn that the
	// overlapping-mode fallback to socket fired because no sample patch
	// was defined).
	Algorithm string

	// Fallbacks is reserved: legacy pixel-WFC populated it; current
	// engines always set it to 0. Kept on the wire so the JS client
	// doesn't need a coordinated update.
	Fallbacks int

	// PatternCount is populated by the overlapping engine: how many
	// distinct NxN patterns were extracted from the sample. The UI
	// uses this to surface "your sample only produced N patterns —
	// try painting more variety."
	PatternCount int
}

// Errors. Stable for HTTP handler mapping.
var (
	ErrNoTileKinds   = errors.New("maps: project has no tile-kind entity types with edge sockets defined")
	ErrNotProcedural = errors.New("maps: map is not in procedural mode")
	ErrNotPersistent = errors.New("maps: map is not persistent; materialization is only meaningful for mode=procedural + persistence=persistent maps")
	ErrNoBaseLayer   = errors.New("maps: map has no tile-kind layer to materialize into")
)

// MaterializeProceduralInput drives MaterializeProcedural.
type MaterializeProceduralInput struct {
	MapID int64
	Seed  uint64
	// LayerID is the tile-layer to write into. Defaults to the map's
	// lowest-ord tile layer (typically "base") when 0.
	LayerID int64
}

// MaterializeProceduralResult reports what was committed.
type MaterializeProceduralResult struct {
	TilesWritten int
	LayerID      int64
	Seed         uint64
}

// MaterializeProcedural runs the configured generation algorithm against
// the project's tile-kind entity types and persists the result into
// map_tiles for `mapID`. Only applies to procedural+persistent maps —
// transient procedural maps regenerate in-memory per refresh window
// (PLAN.md §4g) and never touch map_tiles.
//
// The previously-persisted tiles in the target layer are wiped first so
// re-materializing with a new seed produces a clean replacement (designers
// commonly preview half a dozen seeds, then commit one). Locked cells on
// that layer survive the wipe (they're re-asserted from
// `map_locked_cells` after the bulk DELETE) so the runtime loader can
// continue reading map_tiles directly without joining.
//
// The map's `seed` column is updated so subsequent loads of this map
// reproduce the same world even when the materialized tiles get cleared
// by an admin. All writes are in one transaction.
func (s *Service) MaterializeProcedural(ctx context.Context, in MaterializeProceduralInput) (*MaterializeProceduralResult, error) {
	m, err := s.FindByID(ctx, in.MapID)
	if err != nil {
		return nil, err
	}
	if m.Mode != "procedural" {
		return nil, ErrNotProcedural
	}
	// Maps now own only geometry; persistence/instancing live on
	// LEVELs. Materializing always writes to map_tiles (the canonical
	// authored geometry); transient *runtime* generation lives in
	// GenerateTransientRegion / GenerateProceduralPreview.

	layerID, err := s.resolveTargetTileLayer(ctx, m.ID, in.LayerID)
	if err != nil {
		return nil, err
	}

	// Pull locks for this layer; we use them as anchors for the
	// generator AND re-assert them after the layer wipe.
	locks, err := s.LockedCellsForLayer(ctx, m.ID, layerID)
	if err != nil {
		return nil, fmt.Errorf("load locked cells: %w", err)
	}
	anchors := lockedCellsToAnchors(locks)

	res, err := s.runProcedural(ctx, m, in.Seed, anchors)
	if err != nil {
		// PLAN.md §139: failures get a structured slog entry with
		// seed + map id so the designer can reproduce the failure.
		slog.Warn("wfc materialize failed",
			"map_id", m.ID, "seed", in.Seed, "algorithm", res.Algorithm,
			"width", m.Width, "height", m.Height, "err", err)
		return nil, err
	}
	region := res.Region

	// Persist.
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM map_tiles WHERE map_id = $1 AND layer_id = $2`, m.ID, layerID,
	); err != nil {
		return nil, fmt.Errorf("clear layer: %w", err)
	}

	// Index locks by (x,y) so we can override the generator's pick at
	// each locked coord with the designer's exact entity + rotation.
	lockedAt := make(map[[2]int32]LockedCell, len(locks))
	for _, l := range locks {
		lockedAt[[2]int32{l.X, l.Y}] = l
	}

	for _, c := range region.Cells {
		if l, ok := lockedAt[[2]int32{c.X, c.Y}]; ok {
			if _, err := tx.Exec(ctx, `
				INSERT INTO map_tiles (map_id, layer_id, x, y, entity_type_id, rotation_degrees)
				VALUES ($1, $2, $3, $4, $5, $6)
			`, m.ID, layerID, l.X, l.Y, l.EntityTypeID, l.RotationDegrees); err != nil {
				return nil, fmt.Errorf("insert locked tile (%d,%d): %w", l.X, l.Y, err)
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO map_tiles (map_id, layer_id, x, y, entity_type_id)
			VALUES ($1, $2, $3, $4, $5)
		`, m.ID, layerID, c.X, c.Y, int64(c.EntityType)); err != nil {
			return nil, fmt.Errorf("insert tile (%d,%d): %w", c.X, c.Y, err)
		}
	}

	// Update the map's stored seed so re-materialization (or a transient
	// map's per-refresh regeneration) starts from the same state.
	seedI64 := int64(in.Seed) // safe: WFC seeds round-trip through int64 column
	if _, err := tx.Exec(ctx,
		`UPDATE maps SET seed = $1, updated_at = now() WHERE id = $2`,
		seedI64, m.ID,
	); err != nil {
		return nil, fmt.Errorf("update seed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &MaterializeProceduralResult{
		TilesWritten: len(region.Cells),
		LayerID:      layerID,
		Seed:         in.Seed,
	}, nil
}

// GenerateTransientRegion is the in-memory analogue used by the runtime
// when a procedural+transient map needs fresh tiles for a refresh window.
// No persistence; callers feed the Region directly into the chunked map
// loader's spawn path. Reset-rules engine integration (PLAN.md §automations
// task) keys off this method's seed argument so the same refresh window
// always produces the same world.
//
// Locked cells are still honoured: the loader reads map_locked_cells and
// the generator anchors them. Designers can author "always-here" set
// pieces in transient maps too.
func (s *Service) GenerateTransientRegion(ctx context.Context, mapID int64, seed uint64) (*wfc.Region, error) {
	m, err := s.FindByID(ctx, mapID)
	if err != nil {
		return nil, err
	}
	if m.Mode != "procedural" {
		return nil, ErrNotProcedural
	}
	locks, err := s.LockedCells(ctx, mapID)
	if err != nil {
		return nil, fmt.Errorf("load locked cells: %w", err)
	}
	anchors := lockedCellsToAnchors(locks)
	res, err := s.runProcedural(ctx, m, seed, anchors)
	if err != nil {
		return nil, err
	}
	return res.Region, nil
}

// GenerateProceduralPreview builds the configured engine's tileset from
// the project's tile-kind entity types and runs it. Returns the
// fully-collapsed Region.
//
// "Tile-kind entity type" = any entity_type tagged "tile" or with a Tile
// component attached. The Mapmaker UI is expected to point designers at
// the Entity Manager when the project has zero such types.
func (s *Service) GenerateProceduralPreview(ctx context.Context, in ProceduralPreviewInput) (*ProceduralPreviewResult, error) {
	if in.Width <= 0 || in.Height <= 0 {
		return nil, wfc.ErrInvalidRegion
	}

	// Merge any caller-supplied anchors with the map's locked cells so a
	// preview from the designer panel always reflects "what would commit."
	allAnchors := make([]wfc.Cell, 0, len(in.Anchors))
	allAnchors = append(allAnchors, in.Anchors...)

	var m *Map
	if in.MapID > 0 {
		mm, err := s.FindByID(ctx, in.MapID)
		if err != nil {
			return nil, err
		}
		m = mm
		locks, err := s.LockedCells(ctx, in.MapID)
		if err != nil {
			return nil, fmt.Errorf("load locked cells: %w", err)
		}
		// Lock anchors take precedence over caller-supplied anchors at
		// the same coordinate. Caller anchors are usually empty for the
		// preview path; this is belt-and-suspenders.
		seen := make(map[[2]int32]struct{}, len(locks))
		for _, l := range locks {
			seen[[2]int32{l.X, l.Y}] = struct{}{}
			allAnchors = append(allAnchors, wfc.Cell{X: l.X, Y: l.Y, EntityType: wfc.EntityTypeID(l.EntityTypeID)})
		}
		filtered := allAnchors[:0]
		for _, a := range allAnchors {
			if _, lockedHere := seen[[2]int32{a.X, a.Y}]; lockedHere {
				// Drop caller version; lock-version was appended above.
				// The lock entries themselves are kept.
				continue
			}
			filtered = append(filtered, a)
		}
		// Re-append the lock-derived anchors that were dropped above.
		for _, l := range locks {
			filtered = append(filtered, wfc.Cell{X: l.X, Y: l.Y, EntityType: wfc.EntityTypeID(l.EntityTypeID)})
		}
		allAnchors = filtered
	} else {
		// No MapID supplied (legacy/tests). Synthesise a transient Map
		// matching the requested dimensions.
		m = &Map{
			Width: in.Width,
			Height: in.Height,
			Mode:   "procedural",
		}
	}

	// Width/Height in the input override the map's stored dims. Useful
	// for shrunk previews while iterating on a seed.
	gen := *m
	gen.Width = in.Width
	gen.Height = in.Height

	res, err := s.runProcedural(ctx, &gen, in.Seed, allAnchors)
	if err != nil {
		return nil, err
	}

	tileCount, err := s.countProceduralTiles(ctx)
	if err != nil {
		return nil, err
	}
	return &ProceduralPreviewResult{
		Region:       res.Region,
		TileSetSize:  tileCount,
		Algorithm:    res.Algorithm,
		Fallbacks:    res.Fallbacks,
		PatternCount: res.PatternCount,
	}, nil
}

// resolveTargetTileLayer returns the requested layer id, falling back to
// the lowest-ord tile layer when caller passes 0.
func (s *Service) resolveTargetTileLayer(ctx context.Context, mapID, requested int64) (int64, error) {
	if requested != 0 {
		return requested, nil
	}
	layers, err := s.Layers(ctx, mapID)
	if err != nil {
		return 0, fmt.Errorf("layers: %w", err)
	}
	for _, l := range layers {
		if l.Kind == "tile" {
			return l.ID, nil
		}
	}
	return 0, ErrNoBaseLayer
}

// countProceduralTiles returns the size of the tile-kind palette without
// rebuilding the full TileSet. Used by the preview response so the UI can
// surface "you have N tiles in this project."
func (s *Service) countProceduralTiles(ctx context.Context) (int, error) {
	procSet, err := s.loadTileSetForProcedural(ctx)
	if err != nil {
		return 0, err
	}
	return len(procSet.tiles), nil
}

// proceduralRunResult is the internal shape returned by runProcedural.
// We use a named struct (rather than four positional returns) so adding
// future engine-specific fields — pattern count today, biome metadata
// later — doesn't fan out to every caller.
type proceduralRunResult struct {
	Region       *wfc.Region
	Algorithm    string
	Fallbacks    int // reserved; current engines always set 0
	PatternCount int // populated by overlapping engine; 0 otherwise
}

// procChunkSizeMax is the largest per-axis chunk size we'll use. A
// 64×48 map (the customer's screenshot) at chunkSize=32 becomes 2×2
// chunks — enough seam-aware chunking to break the "one big noise
// sample" feel without fragmenting small maps.
const procChunkSizeMax int32 = 32

// pickChunkLayout returns (chunkW, chunkH, countX, countY) such that
// chunkW*countX == width and chunkH*countY == height (the chunked
// engine requires chunk * count == total — partial chunks are out).
// We pick the largest divisor ≤ procChunkSizeMax for each axis, which
// gives sensible coverage across irregular dimensions:
//
//   64 → 32 (×2)        50 → 25 (×2)
//   48 → 24 (×2)        30 → 15 (×2)        16 → 16 (×1)
//   17 → 17 (×1, prime)
func pickChunkLayout(width, height int32) (int32, int32, int32, int32) {
	chunkW := largestDivisorAtMost(width, procChunkSizeMax)
	chunkH := largestDivisorAtMost(height, procChunkSizeMax)
	return chunkW, chunkH, width / chunkW, height / chunkH
}

// largestDivisorAtMost returns the largest d in (0, max] that divides n.
// n itself is the answer when n ≤ max (so a 24×24 map runs as a single
// 24×24 chunk rather than fragmenting). Worst case for big primes is
// O(sqrt(n)) but maps are at most a few hundred cells per axis.
func largestDivisorAtMost(n, max int32) int32 {
	if n <= 0 {
		return 1
	}
	if n <= max {
		return n
	}
	for d := max; d >= 1; d-- {
		if n%d == 0 {
			return d
		}
	}
	return 1
}

// proceduralAlgorithmLabel is the string the API returns for the
// "what engine ran" field. The designer never picks; we just report.
//   * "chunked-overlapping" — chunked WFC with sample-based patterns
//   * "chunked-socket"      — chunked WFC with socket-only adjacency
const (
	proceduralAlgorithmOverlapping = "chunked-overlapping"
	proceduralAlgorithmSocket      = "chunked-socket"
)

// runProcedural is the one and only generation path. It builds a
// sample patch from designer-painted tiles when possible (zero-config:
// "paint a few cells in the style you want, hit Generate") and falls
// through to socket-adjacency when there's nothing to learn from.
// Either way it always calls GenerateChunked so seam-aware generation
// is the default for every map.
func (s *Service) runProcedural(
	ctx context.Context,
	m *Map,
	seed uint64,
	anchors []wfc.Cell,
) (proceduralRunResult, error) {
	procSet, err := s.loadTileSetForProcedural(ctx)
	if err != nil {
		return proceduralRunResult{}, err
	}
	if len(procSet.tiles) == 0 {
		return proceduralRunResult{}, ErrNoTileKinds
	}

	// Per-map non-local constraints (border / path). Stays a quiet
	// engine-side feature; the panel doesn't expose it for now.
	var constraints []wfc.Constraint
	if m.ID > 0 {
		constraints, err = s.loadMapConstraints(ctx, m.ID)
		if err != nil {
			return proceduralRunResult{}, fmt.Errorf("load constraints: %w", err)
		}
	}

	// Resolve the sample. Precedence:
	//   1. Explicit map_sample_patches row (Sample tool wrote it).
	//   2. Bounding box of painted tiles on the lowest-ord tile layer
	//      (auto-pick: "paint a few tiles, hit Generate").
	//   3. Nothing — socket-adjacency mode.
	sample, sampleSource := s.resolveProceduralSample(ctx, m.ID)

	chunkW, chunkH, countX, countY := pickChunkLayout(m.Width, m.Height)

	algo := proceduralAlgorithmSocket
	if sample.Width > 0 && sampleHasContent(sample) {
		algo = proceduralAlgorithmOverlapping
	} else if sampleSource != "" {
		// Sample existed but was empty. Surface in logs so a designer
		// can tell why the output reverted to plain socket.
		slog.Debug("procedural sample exists but is empty; using socket path",
			"map_id", m.ID, "source", sampleSource)
	}

	region, gerr := wfc.GenerateChunked(wfc.NewTileSet(procSet.tiles), wfc.ChunkedOptions{
		ChunkW: chunkW,
		ChunkH: chunkH,
		CountX: countX,
		CountY: countY,
		Seed:   seed,
		Anchors: wfc.Anchors{Cells: anchors},
		Constraints: constraints,
		// OverlappingSample is honoured per-chunk by the chunked engine
		// (zero-value SamplePatch falls through to socket mode).
		OverlappingSample: sample,
	})
	if gerr != nil {
		return proceduralRunResult{Algorithm: algo}, fmt.Errorf("chunked wfc: %w", gerr)
	}
	region = expandProceduralGroups(region, procSet.groups)

	patternCount := 0
	if algo == proceduralAlgorithmOverlapping {
		// Per-chunk pattern count varies; report the sample's potential
		// pattern count for the UI hint. Cheap to recompute, no extra
		// state to thread through the chunked engine.
		patternCount = approxPatternCount(sample)
	}
	return proceduralRunResult{
		Region:       region,
		Algorithm:    algo,
		PatternCount: patternCount,
	}, nil
}

// resolveProceduralSample picks a sample patch for the engine. Returns
// a zero-value patch when no source applies; the second return is a
// short label for diagnostics ("explicit", "auto-painted", "").
func (s *Service) resolveProceduralSample(ctx context.Context, mapID int64) (wfc.SamplePatch, string) {
	if mapID == 0 {
		return wfc.SamplePatch{}, ""
	}
	if patch, err := s.LoadSamplePatchTiles(ctx, mapID); err == nil {
		return patch, "explicit"
	} else if !errors.Is(err, ErrNoSamplePatch) {
		// DB error reading the patch is non-fatal — log + fall through.
		slog.Warn("load explicit sample patch failed; falling through to auto-pick",
			"map_id", mapID, "err", err)
	}
	if patch, ok := s.autoSampleFromPaintedTiles(ctx, mapID); ok {
		return patch, "auto-painted"
	}
	return wfc.SamplePatch{}, ""
}

// approxPatternCount returns the number of distinct N=2 patterns the
// overlapping engine would extract from `s`. Cheap (<1ms for 32×32).
// We recompute because the chunked engine doesn't surface per-chunk
// pattern counts and an aggregate would be misleading anyway.
func approxPatternCount(s wfc.SamplePatch) int {
	if s.Width < 2 || s.Height < 2 {
		return 0
	}
	seen := make(map[uint64]struct{})
	w, h := int(s.Width), int(s.Height)
	for y := 0; y < h-1; y++ {
		for x := 0; x < w-1; x++ {
			a := uint64(s.Tiles[y*w+x])
			b := uint64(s.Tiles[y*w+x+1])
			c := uint64(s.Tiles[(y+1)*w+x])
			d := uint64(s.Tiles[(y+1)*w+x+1])
			seen[a^(b<<16)^(c<<32)^(d<<48)] = struct{}{}
		}
	}
	return len(seen)
}

// sampleHasContent reports whether the patch has at least one non-zero
// (non-wildcard) cell. An all-zero patch tells the overlapping engine
// nothing useful — every NxN window is wildcards-only.
func sampleHasContent(s wfc.SamplePatch) bool {
	for _, et := range s.Tiles {
		if et != 0 {
			return true
		}
	}
	return false
}

// lockedCellsToAnchors converts a slice of LockedCell to the WFC anchor
// shape the engine expects.
func lockedCellsToAnchors(cells []LockedCell) []wfc.Cell {
	out := make([]wfc.Cell, 0, len(cells))
	for _, c := range cells {
		out = append(out, wfc.Cell{
			X:          c.X,
			Y:          c.Y,
			EntityType: wfc.EntityTypeID(c.EntityTypeID),
		})
	}
	return out
}

// loadTileSetForProcedural reads every entity-type that can be painted
// as a tile and joins its edge-socket assignments. Per the holistic
// redesign, paintable tiles are identified by entity_class='tile' —
// the tilemap service sets the column when it slices a sheet, and the
// procedural-include flag lets designers mute individual cells from
// the random-fill candidate pool without retagging.
//
// One query total — no per-tile follow-up.
type proceduralTileSet struct {
	tiles  []wfc.Tile
	groups map[wfc.EntityTypeID]proceduralGroup
}

type proceduralGroup struct {
	ID     int64
	Width  int32
	Height int32
	Layout [][]wfc.EntityTypeID
}

func (s *Service) loadTileSetForProcedural(ctx context.Context) (*proceduralTileSet, error) {
	// Query 1: tile-kind entity types.
	// We left-join tile_edge_assignments so types missing assignments
	// still show up (with all-zero sockets). The WFC engine treats
	// SocketID(0) as "no socket assigned" — see wfc/types.go.
	rows, err := s.Pool.Query(ctx, `
		SELECT et.id,
		       COALESCE(tea.north_socket_id, 0) AS north_id,
		       COALESCE(tea.east_socket_id,  0) AS east_id,
		       COALESCE(tea.south_socket_id, 0) AS south_id,
		       COALESCE(tea.west_socket_id,  0) AS west_id
		FROM entity_types et
		LEFT JOIN tile_edge_assignments tea
		        ON tea.entity_type_id = et.id
		WHERE et.entity_class = 'tile'
		  AND et.procedural_include
		ORDER BY et.id
	`)
	if err != nil {
		return nil, fmt.Errorf("load tile entity types: %w", err)
	}
	defer rows.Close()

	var tiles []wfc.Tile
	for rows.Next() {
		var id int64
		var n, e, sa, w int64
		if err := rows.Scan(&id, &n, &e, &sa, &w); err != nil {
			return nil, err
		}
		tiles = append(tiles, wfc.Tile{
			EntityType: wfc.EntityTypeID(id),
			Sockets: [4]wfc.SocketID{
				wfc.SocketID(n),
				wfc.SocketID(e),
				wfc.SocketID(sa),
				wfc.SocketID(w),
			},
			Weight: 1, // future: pull from a per-type weight column
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tiles, groups, err := s.applyProceduralTileGroups(ctx, tiles)
	if err != nil {
		return nil, err
	}

	// Stable iteration order so identical (seed, anchors, db state)
	// always produces identical output. Rows are already ORDER BY id
	// at the SQL level but a defensive sort here protects against
	// driver-level reordering during streaming.
	sort.Slice(tiles, func(i, j int) bool { return tiles[i].EntityType < tiles[j].EntityType })
	return &proceduralTileSet{tiles: tiles, groups: groups}, nil
}

func (s *Service) applyProceduralTileGroups(ctx context.Context, base []wfc.Tile) ([]wfc.Tile, map[wfc.EntityTypeID]proceduralGroup, error) {
	byID := make(map[wfc.EntityTypeID]wfc.Tile, len(base))
	for _, tile := range base {
		byID[tile.EntityType] = tile
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT id, width, height, layout_json,
		       exclude_members_from_procedural, use_group_in_procedural
		FROM tile_groups
		ORDER BY id
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("load procedural tile groups: %w", err)
	}
	defer rows.Close()

	excluded := make(map[wfc.EntityTypeID]struct{})
	groups := make(map[wfc.EntityTypeID]proceduralGroup)
	nextSynthetic := wfc.EntityTypeID(-1)
	var groupTiles []wfc.Tile

	for rows.Next() {
		var id int64
		var width, height int32
		var body []byte
		var excludeMembers, useGroup bool
		if err := rows.Scan(&id, &width, &height, &body, &excludeMembers, &useGroup); err != nil {
			return nil, nil, err
		}
		var raw [][]int64
		if err := json.Unmarshal(body, &raw); err != nil {
			continue
		}
		layout, ok := normalizeProceduralGroupLayout(raw, width, height)
		if !ok {
			continue
		}
		for _, row := range layout {
			for _, et := range row {
				if et != 0 && excludeMembers {
					excluded[et] = struct{}{}
				}
			}
		}
		if !useGroup {
			continue
		}
		gt, ok := proceduralGroupTile(id, width, height, layout, byID, nextSynthetic)
		if !ok {
			continue
		}
		nextSynthetic--
		groups[gt.EntityType] = proceduralGroup{ID: id, Width: width, Height: height, Layout: layout}
		groupTiles = append(groupTiles, gt)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	out := make([]wfc.Tile, 0, len(base)+len(groupTiles))
	for _, tile := range base {
		if _, skip := excluded[tile.EntityType]; skip {
			continue
		}
		out = append(out, tile)
	}
	out = append(out, groupTiles...)
	return out, groups, nil
}

func normalizeProceduralGroupLayout(raw [][]int64, width, height int32) ([][]wfc.EntityTypeID, bool) {
	if int32(len(raw)) != height {
		return nil, false
	}
	out := make([][]wfc.EntityTypeID, len(raw))
	hasTile := false
	for r, row := range raw {
		if int32(len(row)) != width {
			return nil, false
		}
		out[r] = make([]wfc.EntityTypeID, len(row))
		for c, v := range row {
			out[r][c] = wfc.EntityTypeID(v)
			if v != 0 {
				hasTile = true
			}
		}
	}
	return out, hasTile
}

func proceduralGroupTile(id int64, width, height int32, layout [][]wfc.EntityTypeID, byID map[wfc.EntityTypeID]wfc.Tile, synthetic wfc.EntityTypeID) (wfc.Tile, bool) {
	var sockets [4]wfc.SocketID
	for edge := wfc.Edge(0); edge < 4; edge++ {
		seen := false
		var socket wfc.SocketID
		for r := int32(0); r < height; r++ {
			for c := int32(0); c < width; c++ {
				if (edge == wfc.EdgeN && r != 0) || (edge == wfc.EdgeS && r != height-1) || (edge == wfc.EdgeW && c != 0) || (edge == wfc.EdgeE && c != width-1) {
					continue
				}
				et := layout[r][c]
				if et == 0 {
					continue
				}
				tile, ok := byID[et]
				if !ok {
					return wfc.Tile{}, false
				}
				s := tile.Sockets[edge]
				if !seen {
					seen = true
					socket = s
					continue
				}
				if socket != s {
					// Mixed boundary sockets cannot be represented by the current
					// one-cell WFC vocabulary without breaking compatibility.
					return wfc.Tile{}, false
				}
			}
		}
		if seen {
			sockets[edge] = socket
		}
	}
	return wfc.Tile{EntityType: synthetic, Sockets: sockets, Weight: float64(maxInt32(1, width*height))}, true
}

func expandProceduralGroups(region *wfc.Region, groups map[wfc.EntityTypeID]proceduralGroup) *wfc.Region {
	if region == nil || len(groups) == 0 {
		return region
	}
	cells := make([]wfc.Cell, 0, len(region.Cells))
	occupied := make(map[[2]int32]struct{}, len(region.Cells))

	// Place groups first so single tiles from the one-cell WFC pass cannot
	// split a chunk. If WFC picked a group on an edge where it does not fit,
	// relocate it to the first free fitting slot. That keeps groups atomic; at
	// worst a group is omitted rather than emitted partially.
	for _, c := range region.Cells {
		g, ok := groups[c.EntityType]
		if !ok {
			continue
		}
		x, y, ok := fittingGroupOrigin(c.X, c.Y, region.Width, region.Height, g, occupied)
		if !ok {
			continue
		}
		appendGroupCells(&cells, occupied, x, y, g)
	}

	// Fill the remaining cells with ordinary WFC picks. Synthetic group ids are
	// never surfaced to callers; they either expanded above or were omitted.
	for _, c := range region.Cells {
		if _, isGroup := groups[c.EntityType]; isGroup || c.EntityType <= 0 {
			continue
		}
		pos := [2]int32{c.X, c.Y}
		if _, done := occupied[pos]; done {
			continue
		}
		cells = append(cells, c)
		occupied[pos] = struct{}{}
	}

	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Y == cells[j].Y {
			return cells[i].X < cells[j].X
		}
		return cells[i].Y < cells[j].Y
	})
	return &wfc.Region{Width: region.Width, Height: region.Height, Cells: cells}
}

func fittingGroupOrigin(preferredX, preferredY, width, height int32, g proceduralGroup, occupied map[[2]int32]struct{}) (int32, int32, bool) {
	if groupFitsAt(preferredX, preferredY, width, height, g, occupied) {
		return preferredX, preferredY, true
	}
	for y := int32(0); y <= height-g.Height; y++ {
		for x := int32(0); x <= width-g.Width; x++ {
			if groupFitsAt(x, y, width, height, g, occupied) {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

func groupFitsAt(x, y, width, height int32, g proceduralGroup, occupied map[[2]int32]struct{}) bool {
	if x < 0 || y < 0 || x+g.Width > width || y+g.Height > height {
		return false
	}
	for r := int32(0); r < g.Height; r++ {
		for c := int32(0); c < g.Width; c++ {
			if g.Layout[r][c] == 0 {
				continue
			}
			if _, done := occupied[[2]int32{x + c, y + r}]; done {
				return false
			}
		}
	}
	return true
}

func appendGroupCells(cells *[]wfc.Cell, occupied map[[2]int32]struct{}, x, y int32, g proceduralGroup) {
	for r := int32(0); r < g.Height; r++ {
		for c := int32(0); c < g.Width; c++ {
			et := g.Layout[r][c]
			if et == 0 {
				continue
			}
			pos := [2]int32{x + c, y + r}
			*cells = append(*cells, wfc.Cell{X: pos[0], Y: pos[1], EntityType: et})
			occupied[pos] = struct{}{}
		}
	}
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
