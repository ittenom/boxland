// Boxland — asset_animations service.
//
// The asset_animations table (migration 0007) stores one row per named
// animation tag a sprite-sheet importer extracted at upload time
// (`walk_north`, `idle`, etc.). The runtime never re-parses the source
// PNG for animation metadata; it reads these rows.
//
// This file is the CRUD surface. The most performance-sensitive caller
// is the `/play/asset-catalog` endpoint, which fans an entity-type's
// referenced asset ids out to the client renderer; that path uses
// `ListByAssetIDs` to dodge an N+1 over assets.

package assets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// AnimationRow is one persisted row from asset_animations. Distinct
// from the importer's `Animation` struct because that one carries the
// pre-persistence shape (no id, no asset_id), and conflating the two
// would force the importer to know about ids.
type AnimationRow struct {
	ID        int64     `json:"id"`
	AssetID   int64     `json:"asset_id"`
	Name      string    `json:"name"`
	FrameFrom int32     `json:"frame_from"`
	FrameTo   int32     `json:"frame_to"`
	Direction Direction `json:"direction"`
	FPS       int32     `json:"fps"`
}

// ReplaceAnimations replaces every asset_animations row for `assetID`
// in one transaction. Idempotent: re-importing a sheet ends with the
// new row set and nothing else, even if names collide with what was
// there before.
//
// Bulk insert via `unnest(...)` so a sheet with 30 walk/attack/death
// clips is one round trip, not 30. The DELETE+INSERT pair runs inside
// a single tx so the table is never observed empty mid-replace.
func (s *Service) ReplaceAnimations(ctx context.Context, assetID int64, anims []Animation) error {
	if assetID == 0 {
		return errors.New("assets: ReplaceAnimations: asset_id required")
	}
	// Validate before opening a tx — caller mistakes shouldn't open
	// (and roll back) a transaction.
	for i, a := range anims {
		if a.Name == "" {
			return fmt.Errorf("assets: ReplaceAnimations: animations[%d]: name required", i)
		}
		if a.FrameFrom < 0 || a.FrameTo < a.FrameFrom {
			return fmt.Errorf("assets: ReplaceAnimations: animations[%d] %q: bad frame range [%d, %d]",
				i, a.Name, a.FrameFrom, a.FrameTo)
		}
		if a.FPS <= 0 || a.FPS > 60 {
			return fmt.Errorf("assets: ReplaceAnimations: animations[%d] %q: fps %d outside (0,60]",
				i, a.Name, a.FPS)
		}
		switch a.Direction {
		case "", DirForward, DirReverse, DirPingpong:
		default:
			return fmt.Errorf("assets: ReplaceAnimations: animations[%d] %q: unknown direction %q",
				i, a.Name, a.Direction)
		}
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM asset_animations WHERE asset_id = $1`, assetID); err != nil {
		return fmt.Errorf("clear animations: %w", err)
	}
	if len(anims) > 0 {
		// Dedup by name (last write wins): importers occasionally emit
		// duplicate tag names from wildly-malformed sidecars, and the
		// UNIQUE (asset_id, name) constraint would otherwise fail the
		// whole insert. Picking last-wins matches the SQL DO UPDATE
		// pattern designers expect from "later overrides earlier".
		byName := make(map[string]Animation, len(anims))
		order := make([]string, 0, len(anims))
		for _, a := range anims {
			key := strings.ToLower(strings.TrimSpace(a.Name))
			if _, seen := byName[key]; !seen {
				order = append(order, key)
			}
			byName[key] = Animation{
				Name:      strings.TrimSpace(a.Name),
				FrameFrom: a.FrameFrom,
				FrameTo:   a.FrameTo,
				Direction: defaultDirection(a.Direction),
				FPS:       a.FPS,
			}
		}
		names := make([]string, 0, len(order))
		fromArr := make([]int32, 0, len(order))
		toArr := make([]int32, 0, len(order))
		dirArr := make([]string, 0, len(order))
		fpsArr := make([]int32, 0, len(order))
		for _, k := range order {
			a := byName[k]
			names = append(names, a.Name)
			fromArr = append(fromArr, int32(a.FrameFrom))
			toArr = append(toArr, int32(a.FrameTo))
			dirArr = append(dirArr, string(a.Direction))
			fpsArr = append(fpsArr, int32(a.FPS))
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO asset_animations (asset_id, name, frame_from, frame_to, direction, fps)
			SELECT $1, name, frame_from, frame_to, direction, fps
			FROM unnest($2::text[], $3::int[], $4::int[], $5::text[], $6::int[])
				AS t(name, frame_from, frame_to, direction, fps)
		`, assetID, names, fromArr, toArr, dirArr, fpsArr); err != nil {
			return fmt.Errorf("insert animations: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ListAnimations returns every animation row for one asset, ordered by
// name (stable for UI lists + diff previews).
func (s *Service) ListAnimations(ctx context.Context, assetID int64) ([]AnimationRow, error) {
	if assetID == 0 {
		return nil, errors.New("assets: ListAnimations: asset_id required")
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, asset_id, name, frame_from, frame_to, direction, fps
		FROM asset_animations
		WHERE asset_id = $1
		ORDER BY name ASC, id ASC
	`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AnimationRow, 0, 8)
	for rows.Next() {
		var r AnimationRow
		var dir string
		if err := rows.Scan(&r.ID, &r.AssetID, &r.Name, &r.FrameFrom, &r.FrameTo, &dir, &r.FPS); err != nil {
			return nil, err
		}
		r.Direction = Direction(dir)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAnimationsByAssetIDs returns every animation row for every asset
// in `assetIDs`, keyed by asset_id. Used by the catalog endpoint to
// avoid an N+1 across an entity-type's referenced sheets.
//
// Empty input returns an empty map without hitting the DB. Missing
// asset ids are simply absent from the result (no error).
func (s *Service) ListAnimationsByAssetIDs(ctx context.Context, assetIDs []int64) (map[int64][]AnimationRow, error) {
	out := make(map[int64][]AnimationRow, len(assetIDs))
	if len(assetIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, asset_id, name, frame_from, frame_to, direction, fps
		FROM asset_animations
		WHERE asset_id = ANY($1::bigint[])
		ORDER BY asset_id ASC, name ASC, id ASC
	`, assetIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r AnimationRow
		var dir string
		if err := rows.Scan(&r.ID, &r.AssetID, &r.Name, &r.FrameFrom, &r.FrameTo, &dir, &r.FPS); err != nil {
			return nil, err
		}
		r.Direction = Direction(dir)
		out[r.AssetID] = append(out[r.AssetID], r)
	}
	return out, rows.Err()
}

// FindAnimationByName returns the single row matching (asset_id, name),
// or ErrAssetNotFound when missing. Used by the runtime animation
// system when resolving "which anim_id is `walk_east` on this sheet".
func (s *Service) FindAnimationByName(ctx context.Context, assetID int64, name string) (*AnimationRow, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, asset_id, name, frame_from, frame_to, direction, fps
		FROM asset_animations
		WHERE asset_id = $1 AND lower(name) = lower($2)
		LIMIT 1
	`, assetID, name)
	var r AnimationRow
	var dir string
	if err := row.Scan(&r.ID, &r.AssetID, &r.Name, &r.FrameFrom, &r.FrameTo, &dir, &r.FPS); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAssetNotFound
		}
		return nil, err
	}
	r.Direction = Direction(dir)
	return &r, nil
}

// defaultDirection backfills DirForward when the importer left direction
// blank (raw / strip parsers do this — only Aseprite carries a value).
func defaultDirection(d Direction) Direction {
	if d == "" {
		return DirForward
	}
	return d
}
