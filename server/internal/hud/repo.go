package hud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repo persists per-realm HUD layouts. One row per level (column on the
// existing levels row). Tenant isolation is enforced by always scoping
// queries to (id, created_by) — handlers MUST pass the requesting
// designer id from session context, never trust the URL alone.
type Repo struct {
	Pool *pgxpool.Pool
}

// ErrNotFound is returned when a level id doesn't exist OR the requester
// doesn't own it. We collapse the two so we don't leak existence; this
// matches the codebase pattern from internal/levels + internal/automations.
var ErrNotFound = errors.New("hud: level not found")

// Get loads the live layout for a realm. Tenant-scoped.
func (r *Repo) Get(ctx context.Context, levelID, ownerID int64) (Layout, error) {
	var raw json.RawMessage
	err := r.Pool.QueryRow(ctx, `
		SELECT hud_layout_json
		FROM levels
		WHERE id = $1 AND created_by = $2
	`, levelID, ownerID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Layout{}, ErrNotFound
		}
		return Layout{}, fmt.Errorf("hud: get layout: %w", err)
	}
	return Decode(raw)
}

// GetForPlayer loads the layout for the player WS path. Players don't
// own the level, so we don't filter by created_by; the realm-membership
// check is upstream (in the WS JoinMap handler). Returns the empty
// layout for unknown levels so the player gets a clean default rather
// than a hard error mid-join.
func (r *Repo) GetForPlayer(ctx context.Context, levelID int64) (Layout, error) {
	var raw json.RawMessage
	err := r.Pool.QueryRow(ctx, `
		SELECT hud_layout_json FROM levels WHERE id = $1
	`, levelID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return NewEmpty(), nil
		}
		return Layout{}, fmt.Errorf("hud: get layout (player): %w", err)
	}
	return Decode(raw)
}

// Save replaces the live layout for a realm. Caller is responsible for
// having validated the layout (typically via ResolveAndValidate).
// Returns ErrNotFound on miss.
//
// Note: in the staged-publish world this is what the publish handler
// invokes after the diff is committed. Designer-side editing writes to
// the drafts table via the existing artifact pipeline; this Save is
// the publish-time apply step.
func (r *Repo) Save(ctx context.Context, levelID, ownerID int64, l Layout) error {
	raw, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("hud: marshal layout: %w", err)
	}
	tag, err := r.Pool.Exec(ctx, `
		UPDATE levels
		SET hud_layout_json = $3, updated_at = now()
		WHERE id = $1 AND created_by = $2
	`, levelID, ownerID, raw)
	if err != nil {
		return fmt.Errorf("hud: save layout: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Mutate runs `mut` on the current layout under a single SELECT ... FOR
// UPDATE ... transaction. Use this for every editor mutation (add /
// save / delete / reorder) so concurrent designer tabs can't trample
// each other. Returns the new layout (helpful for HTMX swaps).
//
// `mut` may return an error to abort the transaction without writing.
func (r *Repo) Mutate(ctx context.Context, levelID, ownerID int64, mut func(l *Layout) error) (Layout, error) {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Layout{}, fmt.Errorf("hud: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var raw json.RawMessage
	err = tx.QueryRow(ctx, `
		SELECT hud_layout_json
		FROM levels
		WHERE id = $1 AND created_by = $2
		FOR UPDATE
	`, levelID, ownerID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Layout{}, ErrNotFound
		}
		return Layout{}, fmt.Errorf("hud: select for update: %w", err)
	}
	layout, err := Decode(raw)
	if err != nil {
		return Layout{}, err
	}
	if err := mut(&layout); err != nil {
		return Layout{}, err
	}
	newRaw, err := json.Marshal(layout)
	if err != nil {
		return Layout{}, fmt.Errorf("hud: marshal: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE levels SET hud_layout_json = $3, updated_at = now()
		WHERE id = $1 AND created_by = $2
	`, levelID, ownerID, newRaw); err != nil {
		return Layout{}, fmt.Errorf("hud: update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Layout{}, fmt.Errorf("hud: commit: %w", err)
	}
	return layout, nil
}

// SaveTx is the transactional variant the publish pipeline uses.
// Identical isolation rules; takes a pgx.Tx so it can land alongside
// the publish_diffs row in one commit.
func SaveTx(ctx context.Context, tx pgx.Tx, levelID, ownerID int64, l Layout) error {
	raw, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("hud: marshal layout: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE levels
		SET hud_layout_json = $3, updated_at = now()
		WHERE id = $1 AND created_by = $2
	`, levelID, ownerID, raw)
	if err != nil {
		return fmt.Errorf("hud: save layout (tx): %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
