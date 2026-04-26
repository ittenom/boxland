// Boxland — sample-patch CRUD for the overlapping-model WFC.
//
// One row per procedural map. The row is a (layer_id, x, y, width,
// height, pattern_n) reference into a rectangular region of an existing
// map layer; the overlapping engine reads the tiles in that rectangle
// (from map_tiles, with map_locked_cells overlaid) and uses them as
// the learning sample.
//
// Schema in migration 0039.

package maps

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/maps/wfc"
)

// SamplePatch is one row of map_sample_patches plus the read-side
// derived "tiles" slice (only populated by LoadSamplePatchTiles).
type SamplePatch struct {
	MapID     int64 `json:"map_id"`
	LayerID   int64 `json:"layer_id"`
	X         int32 `json:"x"`
	Y         int32 `json:"y"`
	Width     int32 `json:"width"`
	Height    int32 `json:"height"`
	PatternN  int16 `json:"pattern_n"`
}

// SamplePatchInput drives UpsertSamplePatch.
type SamplePatchInput struct {
	MapID    int64
	LayerID  int64
	X        int32
	Y        int32
	Width    int32
	Height   int32
	PatternN int16 // 0 = use default (2)
}

// Errors. Stable for HTTP handler mapping.
var (
	ErrSamplePatchInvalid = errors.New("maps: sample patch payload invalid")
	ErrNoSamplePatch      = errors.New("maps: no sample patch defined for this map")
)

// SamplePatchByMap returns the sample patch row for a map, or
// ErrNoSamplePatch if none is defined.
func (s *Service) SamplePatchByMap(ctx context.Context, mapID int64) (*SamplePatch, error) {
	var p SamplePatch
	err := s.Pool.QueryRow(ctx, `
		SELECT map_id, layer_id, x, y, width, height, pattern_n
		FROM map_sample_patches
		WHERE map_id = $1
	`, mapID).Scan(&p.MapID, &p.LayerID, &p.X, &p.Y, &p.Width, &p.Height, &p.PatternN)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSamplePatch
	}
	if err != nil {
		return nil, fmt.Errorf("sample patch by map: %w", err)
	}
	return &p, nil
}

// UpsertSamplePatch sets (or replaces) the sample patch for a map.
// Width/Height are clamped to [2, 32] by the SQL CHECK constraint —
// we validate in Go too so the error is friendlier than a Postgres
// constraint violation.
func (s *Service) UpsertSamplePatch(ctx context.Context, in SamplePatchInput) error {
	if in.Width < 2 || in.Width > 32 || in.Height < 2 || in.Height > 32 {
		return fmt.Errorf("%w: width/height must be in [2, 32], got %dx%d",
			ErrSamplePatchInvalid, in.Width, in.Height)
	}
	if in.X < 0 || in.Y < 0 {
		return fmt.Errorf("%w: x/y must be >= 0, got (%d, %d)",
			ErrSamplePatchInvalid, in.X, in.Y)
	}
	patternN := in.PatternN
	if patternN == 0 {
		patternN = 2
	}
	if patternN != 2 && patternN != 3 {
		return fmt.Errorf("%w: pattern_n must be 2 or 3, got %d",
			ErrSamplePatchInvalid, patternN)
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO map_sample_patches
		    (map_id, layer_id, x, y, width, height, pattern_n)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (map_id) DO UPDATE
		SET layer_id   = EXCLUDED.layer_id,
		    x          = EXCLUDED.x,
		    y          = EXCLUDED.y,
		    width      = EXCLUDED.width,
		    height     = EXCLUDED.height,
		    pattern_n  = EXCLUDED.pattern_n,
		    updated_at = now()
	`, in.MapID, in.LayerID, in.X, in.Y, in.Width, in.Height, patternN)
	if err != nil {
		return fmt.Errorf("upsert sample patch: %w", err)
	}
	return nil
}

// DeleteSamplePatch removes the patch row for a map. No-op if none.
func (s *Service) DeleteSamplePatch(ctx context.Context, mapID int64) error {
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM map_sample_patches WHERE map_id = $1`, mapID,
	)
	if err != nil {
		return fmt.Errorf("delete sample patch: %w", err)
	}
	return nil
}

// LoadSamplePatchTiles reads the actual tile cells inside the patch's
// rectangle and returns them as a wfc.SamplePatch ready for the
// overlapping engine. Locked cells override map_tiles at the same
// coordinate (the lock brush wins). Cells with no row in either table
// are returned as EntityType 0 — the overlapping engine treats those
// as wildcards.
//
// One indexed query per call against map_tiles, plus one against
// map_locked_cells. The patch is at most 32×32 = 1024 cells, so this
// is a tiny payload.
func (s *Service) LoadSamplePatchTiles(ctx context.Context, mapID int64) (wfc.SamplePatch, error) {
	patch, err := s.SamplePatchByMap(ctx, mapID)
	if err != nil {
		return wfc.SamplePatch{}, err
	}

	tiles := make([]wfc.EntityTypeID, int(patch.Width)*int(patch.Height))
	x0, y0 := patch.X, patch.Y
	x1 := patch.X + patch.Width - 1
	y1 := patch.Y + patch.Height - 1

	// Base layer: map_tiles within the rect.
	rows, err := s.Pool.Query(ctx, `
		SELECT x, y, entity_type_id
		FROM map_tiles
		WHERE map_id = $1 AND layer_id = $2
		  AND x BETWEEN $3 AND $4
		  AND y BETWEEN $5 AND $6
	`, mapID, patch.LayerID, x0, x1, y0, y1)
	if err != nil {
		return wfc.SamplePatch{}, fmt.Errorf("sample tiles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var x, y int32
		var et int64
		if err := rows.Scan(&x, &y, &et); err != nil {
			return wfc.SamplePatch{}, err
		}
		idx := (y-y0)*patch.Width + (x - x0)
		if idx < 0 || int(idx) >= len(tiles) {
			continue // defensive: shouldn't happen with the WHERE clause
		}
		tiles[idx] = wfc.EntityTypeID(et)
	}
	if err := rows.Err(); err != nil {
		return wfc.SamplePatch{}, err
	}

	// Lock overlay — wins over map_tiles at the same coordinate.
	lrows, err := s.Pool.Query(ctx, `
		SELECT x, y, entity_type_id
		FROM map_locked_cells
		WHERE map_id = $1 AND layer_id = $2
		  AND x BETWEEN $3 AND $4
		  AND y BETWEEN $5 AND $6
	`, mapID, patch.LayerID, x0, x1, y0, y1)
	if err != nil {
		return wfc.SamplePatch{}, fmt.Errorf("sample lock overlay: %w", err)
	}
	defer lrows.Close()
	for lrows.Next() {
		var x, y int32
		var et int64
		if err := lrows.Scan(&x, &y, &et); err != nil {
			return wfc.SamplePatch{}, err
		}
		idx := (y-y0)*patch.Width + (x - x0)
		if idx < 0 || int(idx) >= len(tiles) {
			continue
		}
		tiles[idx] = wfc.EntityTypeID(et)
	}
	if err := lrows.Err(); err != nil {
		return wfc.SamplePatch{}, err
	}

	return wfc.SamplePatch{
		Width:  patch.Width,
		Height: patch.Height,
		Tiles:  tiles,
	}, nil
}
