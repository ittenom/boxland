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

// TilePixelLoader is the maps service's view of "give me edge fingerprints
// for one tile-kind entity type." Implemented by the designer wiring
// against assets.Service + persistence.ObjectStore so the maps package
// stays free of asset-pipeline dependencies. Nil is acceptable: pixel-WFC
// then falls back to socket-mode for that map and logs a warning.
type TilePixelLoader interface {
	// FingerprintFor returns the four-edge fingerprint for the tile
	// stored on entity-type `entityTypeID`. The implementation owns its
	// own cache; callers may invoke this once per generation per tile.
	FingerprintFor(ctx context.Context, entityTypeID int64) ([4]wfc.EdgeFingerprint, error)
}

// SetPixelLoader installs the dependency. cmd/boxland calls this once at
// boot; tests can pass nil to keep socket mode the only working option.
func (s *Service) SetPixelLoader(l TilePixelLoader) { s.pixelLoader = l }

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
	// Ignored by the pixel-WFC engine (which never reseeds).
	MaxReseeds int

	// AlgorithmOverride lets the caller force a specific engine
	// regardless of the map's stored gen_algorithm. Empty = use the
	// stored value. Useful for "preview as pixel WFC before I commit
	// to switching" UX.
	AlgorithmOverride string
}

// ProceduralPreviewResult is what the Mapmaker renders as a ghost overlay.
type ProceduralPreviewResult struct {
	Region *wfc.Region

	// TileSetSize is the number of distinct tile-kind entity types
	// considered. Surfaced so the UI can warn "you only have 1 tile
	// in your project" when a generation looks degenerate.
	TileSetSize int

	// Algorithm is the engine that actually ran. Echoed so the UI can
	// confirm the pixel-mode override took effect (or warn that the
	// fallback to socket fired because no pixel loader was wired).
	Algorithm string

	// Fallbacks counts cells the pixel engine had to fill from its
	// nearest-neighbour fallback because propagation pruned them empty.
	// Always 0 for socket mode. The Mapmaker shows it as an "increase
	// tile variety" hint when non-zero.
	Fallbacks int
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
	if m.PersistenceMode != "persistent" {
		return nil, ErrNotPersistent
	}

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

	region, algorithm, _, err := s.runProcedural(ctx, m, in.Seed, anchors, "")
	if err != nil {
		// PLAN.md §139: failures get a structured slog entry with
		// seed + map id so the designer can reproduce the failure.
		slog.Warn("wfc materialize failed",
			"map_id", m.ID, "seed", in.Seed, "algorithm", algorithm,
			"width", m.Width, "height", m.Height, "err", err)
		return nil, err
	}

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
	region, _, _, err := s.runProcedural(ctx, m, seed, anchors, "")
	if err != nil {
		return nil, err
	}
	return region, nil
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
		// matching the requested dimensions; algorithm defaults to socket.
		m = &Map{
			Width:        in.Width,
			Height:       in.Height,
			Mode:         "procedural",
			GenAlgorithm: GenAlgorithmSocket,
		}
	}

	// Width/Height in the input override the map's stored dims. Useful
	// for shrunk previews while iterating on a seed.
	gen := *m
	gen.Width = in.Width
	gen.Height = in.Height

	region, algorithm, fallbacks, err := s.runProcedural(ctx, &gen, in.Seed, allAnchors, in.AlgorithmOverride)
	if err != nil {
		return nil, err
	}

	tileCount, err := s.countProceduralTiles(ctx)
	if err != nil {
		return nil, err
	}
	return &ProceduralPreviewResult{
		Region:      region,
		TileSetSize: tileCount,
		Algorithm:   algorithm,
		Fallbacks:   fallbacks,
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

// runProcedural dispatches by m.GenAlgorithm (or the override) and runs
// the right engine. Returns the expanded region, the algorithm that ran,
// the fallback count (pixel only), and any error.
func (s *Service) runProcedural(
	ctx context.Context,
	m *Map,
	seed uint64,
	anchors []wfc.Cell,
	override string,
) (*wfc.Region, string, int, error) {
	algo := override
	if algo == "" {
		algo = m.GenAlgorithm
	}
	if algo == "" {
		algo = GenAlgorithmSocket
	}

	procSet, err := s.loadTileSetForProcedural(ctx)
	if err != nil {
		return nil, algo, 0, err
	}
	if len(procSet.tiles) == 0 {
		return nil, algo, 0, ErrNoTileKinds
	}

	switch algo {
	case GenAlgorithmPixelWFC:
		if s.pixelLoader == nil {
			slog.Warn("pixel-WFC requested but no pixel loader wired; falling back to socket",
				"map_id", m.ID)
			algo = GenAlgorithmSocket
			break
		}
		pixelTiles, perr := s.buildPixelTiles(ctx, procSet)
		if perr != nil {
			return nil, algo, 0, fmt.Errorf("build pixel tiles: %w", perr)
		}
		if len(pixelTiles) == 0 {
			// Every tile failed to produce a fingerprint; degrade.
			slog.Warn("no pixel tiles produced; falling back to socket", "map_id", m.ID)
			algo = GenAlgorithmSocket
			break
		}
		res, gerr := wfc.GeneratePixel(wfc.NewPixelTileSet(pixelTiles, wfc.PixelTileSetOptions{}), wfc.GenerateOptions{
			Width:   m.Width,
			Height:  m.Height,
			Seed:    seed,
			Anchors: wfc.Anchors{Cells: anchors},
		})
		if gerr != nil {
			return nil, algo, 0, fmt.Errorf("pixel wfc: %w", gerr)
		}
		region := expandProceduralGroups(res.Region, procSet.groups)
		return region, algo, res.Fallbacks, nil
	}

	// Socket path (default + fallback).
	region, gerr := wfc.Generate(wfc.NewTileSet(procSet.tiles), wfc.GenerateOptions{
		Width:   m.Width,
		Height:  m.Height,
		Seed:    seed,
		Anchors: wfc.Anchors{Cells: anchors},
	})
	if gerr != nil {
		return nil, algo, 0, fmt.Errorf("wfc generate: %w", gerr)
	}
	region = expandProceduralGroups(region, procSet.groups)
	return region, algo, 0, nil
}

// buildPixelTiles loads edge fingerprints for every tile in `procSet`.
// Tiles missing a fingerprint (e.g. their sprite asset is gone) get
// silently dropped — they were unpaintable anyway.
//
// Tile groups participate by averaging their member tiles' edge
// fingerprints (so a 2x2 group's south edge is the mean of its bottom-
// row members' south edges). Groups whose member tiles all lack
// fingerprints are also dropped.
func (s *Service) buildPixelTiles(ctx context.Context, procSet *proceduralTileSet) ([]wfc.PixelTile, error) {
	out := make([]wfc.PixelTile, 0, len(procSet.tiles))
	// Tiles with positive entity-type IDs are real entity types we can
	// fingerprint. Synthetic group ids are negative; for those we
	// composite member fingerprints.
	memberFingerprints := make(map[wfc.EntityTypeID][4]wfc.EdgeFingerprint, len(procSet.tiles))
	for _, t := range procSet.tiles {
		if t.EntityType <= 0 {
			continue
		}
		fp, err := s.pixelLoader.FingerprintFor(ctx, int64(t.EntityType))
		if err != nil {
			slog.Debug("pixel fingerprint unavailable; skipping tile",
				"entity_type_id", t.EntityType, "err", err)
			continue
		}
		memberFingerprints[t.EntityType] = fp
		out = append(out, wfc.PixelTile{
			EntityType:  t.EntityType,
			Fingerprint: fp,
			Weight:      t.Weight,
		})
	}
	for _, t := range procSet.tiles {
		if t.EntityType >= 0 {
			continue
		}
		g, ok := procSet.groups[t.EntityType]
		if !ok {
			continue
		}
		fp, ok := compositeGroupFingerprint(g, memberFingerprints)
		if !ok {
			continue
		}
		out = append(out, wfc.PixelTile{
			EntityType:  t.EntityType,
			Fingerprint: fp,
			Weight:      t.Weight,
		})
	}
	return out, nil
}

// compositeGroupFingerprint averages the edge fingerprints of a group's
// boundary cells. Returns false if no boundary cell on any edge has a
// fingerprint (group entirely composed of fingerprint-less tiles).
func compositeGroupFingerprint(g proceduralGroup, members map[wfc.EntityTypeID][4]wfc.EdgeFingerprint) ([4]wfc.EdgeFingerprint, bool) {
	var out [4]wfc.EdgeFingerprint
	hasAny := false
	for edge := wfc.Edge(0); edge < 4; edge++ {
		var parts []wfc.EdgeFingerprint
		for r := int32(0); r < g.Height; r++ {
			for c := int32(0); c < g.Width; c++ {
				if (edge == wfc.EdgeN && r != 0) ||
					(edge == wfc.EdgeS && r != g.Height-1) ||
					(edge == wfc.EdgeW && c != 0) ||
					(edge == wfc.EdgeE && c != g.Width-1) {
					continue
				}
				et := g.Layout[r][c]
				if et == 0 {
					continue
				}
				fp, ok := members[et]
				if !ok {
					continue
				}
				parts = append(parts, fp[edge])
			}
		}
		if len(parts) == 0 {
			continue
		}
		out[edge] = wfc.CompositeFingerprint(parts)
		hasAny = true
	}
	return out, hasAny
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

// loadTileSetForProcedural reads every entity-type that can be painted as a
// tile and joins its edge-socket assignments. Upload-time tile-sheet slicing
// marks paintable cells with the stable "tile" tag; placed tile instances get
// the Tile component later when materialized into a map. Accept both markers so
// procedural generation sees the same tile palette designers see in Mapmaker.
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
		WHERE et.tags @> ARRAY['tile']::text[]
		   OR EXISTS (
		        SELECT 1
		        FROM entity_components ec
		        WHERE ec.entity_type_id = et.id
		          AND ec.component_kind = 'tile'
		   )
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
