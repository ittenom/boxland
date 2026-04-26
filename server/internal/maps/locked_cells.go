// Boxland — locked-cell CRUD for procedural maps.
//
// Locked cells are designer-painted tile placements that survive
// procedural regeneration. The Mapmaker's Lock brush writes to this
// table; both procedural engines (socket + pixel) read it on every
// generation and feed the rows in as anchors so the procedural fill
// flows around them.
//
// Migration 0037 owns the schema. Composite PK (map_id, layer_id, x, y)
// matches map_tiles so per-cell brushing is O(touched rows).

package maps

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LockedCell is one row of map_locked_cells.
type LockedCell struct {
	MapID           int64 `json:"map_id"`
	LayerID         int64 `json:"layer_id"`
	X               int32 `json:"x"`
	Y               int32 `json:"y"`
	EntityTypeID    int64 `json:"entity_type_id"`
	RotationDegrees int16 `json:"rotation_degrees"`
}

// ErrLockedCellInvalid is returned when a payload fails per-cell validation.
var ErrLockedCellInvalid = errors.New("maps: locked cell payload invalid")

// LockedCells returns every locked cell on `mapID`. Single indexed query.
func (s *Service) LockedCells(ctx context.Context, mapID int64) ([]LockedCell, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT map_id, layer_id, x, y, entity_type_id, rotation_degrees
		FROM map_locked_cells
		WHERE map_id = $1
		ORDER BY layer_id, y, x
	`, mapID)
	if err != nil {
		return nil, fmt.Errorf("locked cells: %w", err)
	}
	defer rows.Close()
	var out []LockedCell
	for rows.Next() {
		var c LockedCell
		if err := rows.Scan(&c.MapID, &c.LayerID, &c.X, &c.Y, &c.EntityTypeID, &c.RotationDegrees); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LockedCellsForLayer is the per-layer variant. Used by the procedural
// path which only operates on one tile layer at a time.
func (s *Service) LockedCellsForLayer(ctx context.Context, mapID, layerID int64) ([]LockedCell, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT map_id, layer_id, x, y, entity_type_id, rotation_degrees
		FROM map_locked_cells
		WHERE map_id = $1 AND layer_id = $2
		ORDER BY y, x
	`, mapID, layerID)
	if err != nil {
		return nil, fmt.Errorf("locked cells for layer: %w", err)
	}
	defer rows.Close()
	var out []LockedCell
	for rows.Next() {
		var c LockedCell
		if err := rows.Scan(&c.MapID, &c.LayerID, &c.X, &c.Y, &c.EntityTypeID, &c.RotationDegrees); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LockCells upserts a batch of cells in a single transaction. Used by the
// Mapmaker's lock brush (which can emit dozens of cells per stroke).
//
// Designed for n+1 avoidance: when there are >= 32 cells we use a
// pgx.CopyFrom into a temp table and merge, otherwise a single multi-row
// INSERT … ON CONFLICT.
func (s *Service) LockCells(ctx context.Context, cells []LockedCell) error {
	if len(cells) == 0 {
		return nil
	}
	for _, c := range cells {
		if !ValidRotationDegrees(c.RotationDegrees) {
			return fmt.Errorf("%w: invalid rotation %d at (%d,%d)", ErrLockedCellInvalid, c.RotationDegrees, c.X, c.Y)
		}
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if len(cells) >= 32 {
		// Stage into a TEMP table then merge. CopyFrom doesn't itself
		// support ON CONFLICT; the temp-table dance is the standard
		// idiom and stays a single round-trip per row from the client's
		// perspective (CopyFrom batches internally).
		if _, err := tx.Exec(ctx, `
			CREATE TEMP TABLE _stage_locked_cells (
				map_id BIGINT, layer_id BIGINT, x INT, y INT,
				entity_type_id BIGINT, rotation_degrees SMALLINT
			) ON COMMIT DROP
		`); err != nil {
			return fmt.Errorf("stage temp: %w", err)
		}
		rows := make([][]any, 0, len(cells))
		for _, c := range cells {
			rows = append(rows, []any{c.MapID, c.LayerID, c.X, c.Y, c.EntityTypeID, c.RotationDegrees})
		}
		if _, err := tx.CopyFrom(ctx,
			pgx.Identifier{"_stage_locked_cells"},
			[]string{"map_id", "layer_id", "x", "y", "entity_type_id", "rotation_degrees"},
			pgx.CopyFromRows(rows),
		); err != nil {
			return fmt.Errorf("copy stage: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO map_locked_cells (map_id, layer_id, x, y, entity_type_id, rotation_degrees)
			SELECT map_id, layer_id, x, y, entity_type_id, rotation_degrees
			FROM _stage_locked_cells
			ON CONFLICT (map_id, layer_id, x, y) DO UPDATE
			SET entity_type_id = EXCLUDED.entity_type_id,
			    rotation_degrees = EXCLUDED.rotation_degrees
		`); err != nil {
			return fmt.Errorf("merge stage: %w", err)
		}
	} else {
		// Multi-row INSERT … VALUES (), (), … with one round trip.
		// Build args + placeholders.
		args := make([]any, 0, len(cells)*6)
		ph := make([]byte, 0, len(cells)*30)
		for i, c := range cells {
			if i > 0 {
				ph = append(ph, ',')
			}
			base := i*6 + 1
			ph = append(ph, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d)",
				base, base+1, base+2, base+3, base+4, base+5)...)
			args = append(args, c.MapID, c.LayerID, c.X, c.Y, c.EntityTypeID, c.RotationDegrees)
		}
		query := `INSERT INTO map_locked_cells
				(map_id, layer_id, x, y, entity_type_id, rotation_degrees)
				VALUES ` + string(ph) + `
				ON CONFLICT (map_id, layer_id, x, y) DO UPDATE
				SET entity_type_id = EXCLUDED.entity_type_id,
				    rotation_degrees = EXCLUDED.rotation_degrees`
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return fmt.Errorf("insert lock cells: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// UnlockCells deletes the listed cells. Single round trip.
func (s *Service) UnlockCells(ctx context.Context, mapID, layerID int64, points [][2]int32) error {
	if len(points) == 0 {
		return nil
	}
	xs := make([]int32, len(points))
	ys := make([]int32, len(points))
	for i, p := range points {
		xs[i] = p[0]
		ys[i] = p[1]
	}
	// UNNEST keeps this a single statement regardless of stroke length.
	_, err := s.Pool.Exec(ctx, `
		DELETE FROM map_locked_cells
		WHERE map_id = $1 AND layer_id = $2
		  AND (x, y) IN (SELECT * FROM UNNEST($3::int[], $4::int[]))
	`, mapID, layerID, xs, ys)
	if err != nil {
		return fmt.Errorf("unlock cells: %w", err)
	}
	return nil
}

// ClearLockedCells removes every lock on a map (or one layer when layerID > 0).
func (s *Service) ClearLockedCells(ctx context.Context, mapID, layerID int64) error {
	if layerID > 0 {
		_, err := s.Pool.Exec(ctx,
			`DELETE FROM map_locked_cells WHERE map_id = $1 AND layer_id = $2`, mapID, layerID)
		return err
	}
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM map_locked_cells WHERE map_id = $1`, mapID)
	return err
}

// LockedCellCount returns the total locks on `mapID`. Cheap; used by the
// Mapmaker panel to render "12 cells locked" without shipping all rows.
func (s *Service) LockedCellCount(ctx context.Context, mapID int64) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM map_locked_cells WHERE map_id = $1`, mapID,
	).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}
