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
	ErrNoTileKinds       = errors.New("maps: project has no tile-kind entity types with edge sockets defined")
	ErrNotProcedural     = errors.New("maps: map is not in procedural mode")
	ErrNotPersistent     = errors.New("maps: map is not persistent; materialization is only meaningful for mode=procedural + persistence=persistent maps")
	ErrNoBaseLayer       = errors.New("maps: map has no tile-kind layer to materialize into")
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
	tiles, err := s.loadTileSetForProcedural(ctx)
	if err != nil {
		return nil, err
	}
	if len(tiles) == 0 {
		return nil, ErrNoTileKinds
	}
	region, err := wfc.Generate(wfc.NewTileSet(tiles), wfc.GenerateOptions{
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
	tiles, err := s.loadTileSetForProcedural(ctx)
	if err != nil {
		return nil, err
	}
	if len(tiles) == 0 {
		return nil, ErrNoTileKinds
	}
	return wfc.Generate(wfc.NewTileSet(tiles), wfc.GenerateOptions{
		Width:  m.Width,
		Height: m.Height,
		Seed:   seed,
	})
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

	tiles, err := s.loadTileSetForProcedural(ctx)
	if err != nil {
		return nil, err
	}
	if len(tiles) == 0 {
		return nil, ErrNoTileKinds
	}

	region, err := wfc.Generate(wfc.NewTileSet(tiles), wfc.GenerateOptions{
		Width:      in.Width,
		Height:     in.Height,
		Seed:       in.Seed,
		Anchors:    wfc.Anchors{Cells: in.Anchors},
		MaxReseeds: in.MaxReseeds,
	})
	if err != nil {
		return nil, fmt.Errorf("wfc generate: %w", err)
	}
	return &ProceduralPreviewResult{Region: region, TileSetSize: len(tiles)}, nil
}

// loadTileSetForProcedural reads every entity-type that has a Tile
// component and joins its edge-socket assignments. Two queries total —
// no per-tile follow-up.
func (s *Service) loadTileSetForProcedural(ctx context.Context) ([]wfc.Tile, error) {
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
		INNER JOIN entity_components ec
		        ON ec.entity_type_id = et.id AND ec.component_kind = 'tile'
		LEFT JOIN tile_edge_assignments tea
		        ON tea.entity_type_id = et.id
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

	// Stable iteration order so identical (seed, anchors, db state)
	// always produces identical output. Rows are already ORDER BY id
	// at the SQL level but a defensive sort here protects against
	// driver-level reordering during streaming.
	sort.Slice(tiles, func(i, j int) bool { return tiles[i].EntityType < tiles[j].EntityType })
	return tiles, nil
}
