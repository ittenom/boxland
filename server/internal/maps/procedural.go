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
	// Width/Height are in tiles. The Mapmaker enforces sensible limits
	// upstream (typically 16..256); this method only checks > 0.
	Width, Height int32

	// Seed lets the designer iterate ("reroll") deterministically.
	Seed uint64

	// Anchors are designer-painted cells in WORLD coordinates. The
	// caller is responsible for translating designer-region paints
	// into individual cell anchors before invoking.
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

// MaterializeProcedural runs WFC against the project's tile-kind entity
// types and persists the result into map_tiles for `mapID`. Only applies
// to procedural+persistent maps — transient procedural maps regenerate
// in-memory per refresh window (PLAN.md §4g) and never touch map_tiles.
//
// The previously-persisted tiles in the target layer are wiped first so
// re-materializing with a new seed produces a clean replacement (designers
// commonly preview half a dozen seeds, then commit one). The map's
// `seed` column is updated so subsequent loads of this map reproduce the
// same world even when the materialized tiles get cleared by an admin.
//
// All writes are in one transaction so a partially-materialized layer
// can never be observed.
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

	// Resolve target layer.
	layerID := in.LayerID
	if layerID == 0 {
		layers, err := s.Layers(ctx, m.ID)
		if err != nil {
			return nil, fmt.Errorf("layers: %w", err)
		}
		for _, l := range layers {
			if l.Kind == "tile" {
				layerID = l.ID
				break
			}
		}
		if layerID == 0 {
			return nil, ErrNoBaseLayer
		}
	}

	// Build tileset + run WFC. Reuses the preview path so persistent
	// materialization and live preview can never disagree about the
	// solver's decisions for a given seed.
	procSet, err := s.loadTileSetForProcedural(ctx)
	if err != nil {
		return nil, err
	}
	if len(procSet.tiles) == 0 {
		return nil, ErrNoTileKinds
	}
	region, err := wfc.Generate(wfc.NewTileSet(procSet.tiles), wfc.GenerateOptions{
		Width:  m.Width,
		Height: m.Height,
		Seed:   in.Seed,
	})
	if err != nil {
		// PLAN.md §139: WFC failures get a structured slog entry with
		// seed + map id so the designer can reproduce the failure.
		slog.Warn("wfc materialize failed",
			"map_id", m.ID, "seed", in.Seed,
			"width", m.Width, "height", m.Height, "err", err)
		return nil, fmt.Errorf("wfc: %w", err)
	}

	region = expandProceduralGroups(region, procSet.groups)

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

	for _, c := range region.Cells {
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
func (s *Service) GenerateTransientRegion(ctx context.Context, mapID int64, seed uint64) (*wfc.Region, error) {
	m, err := s.FindByID(ctx, mapID)
	if err != nil {
		return nil, err
	}
	if m.Mode != "procedural" {
		return nil, ErrNotProcedural
	}
	procSet, err := s.loadTileSetForProcedural(ctx)
	if err != nil {
		return nil, err
	}
	if len(procSet.tiles) == 0 {
		return nil, ErrNoTileKinds
	}
	region, err := wfc.Generate(wfc.NewTileSet(procSet.tiles), wfc.GenerateOptions{
		Width:  m.Width,
		Height: m.Height,
		Seed:   seed,
	})
	if err != nil {
		return nil, err
	}
	return expandProceduralGroups(region, procSet.groups), nil
}

// GenerateProceduralPreview builds a TileSet from the project's tile-kind
// entity types + their tile_edge_assignments and runs WFC. Returns the
// fully-collapsed Region.
//
// "Tile-kind entity type" = any entity_type with a Tile component
// attached. The Mapmaker UI is expected to point designers at the Entity
// Manager when the project has zero such types.
func (s *Service) GenerateProceduralPreview(ctx context.Context, in ProceduralPreviewInput) (*ProceduralPreviewResult, error) {
	if in.Width <= 0 || in.Height <= 0 {
		return nil, wfc.ErrInvalidRegion
	}

	procSet, err := s.loadTileSetForProcedural(ctx)
	if err != nil {
		return nil, err
	}
	if len(procSet.tiles) == 0 {
		return nil, ErrNoTileKinds
	}

	region, err := wfc.Generate(wfc.NewTileSet(procSet.tiles), wfc.GenerateOptions{
		Width:      in.Width,
		Height:     in.Height,
		Seed:       in.Seed,
		Anchors:    wfc.Anchors{Cells: in.Anchors},
		MaxReseeds: in.MaxReseeds,
	})
	if err != nil {
		return nil, fmt.Errorf("wfc generate: %w", err)
	}
	region = expandProceduralGroups(region, procSet.groups)
	return &ProceduralPreviewResult{Region: region, TileSetSize: len(procSet.tiles)}, nil
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
