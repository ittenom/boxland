// Package worlds owns the WORLD surface: a graph of LEVELs connected
// by transition entities (e.g. door entities placed in a level whose
// `level_transition` action points at a target level).
//
// Per the holistic redesign, worlds are optional. A level can exist
// without a world (for solo iteration / sandbox testing); only levels
// reachable from a world's start_level participate in a published
// world's playable graph. The graph between levels is *implicit* —
// transitions live in level_entities' instance_overrides_json — so
// this package owns no edge table.
package worlds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// World is one row of the worlds table.
type World struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	StartLevelID  *int64          `json:"start_level_id,omitempty"`
	SettingsJSON  json.RawMessage `json:"settings"`
	FolderID      *int64          `json:"folder_id,omitempty"`
	CreatedBy     int64           `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// Errors. Stable for HTTP handler mapping.
var (
	ErrWorldNotFound = errors.New("worlds: not found")
	ErrNameInUse     = errors.New("worlds: name already exists")
	ErrInvalidName   = errors.New("worlds: name is required")
)

// Service is the world CRUD facade.
type Service struct {
	Pool *pgxpool.Pool
}

// New constructs a Service.
func New(pool *pgxpool.Pool) *Service { return &Service{Pool: pool} }

// CreateInput drives Create.
type CreateInput struct {
	Name      string
	FolderID  *int64
	CreatedBy int64
}

// Create inserts a new world row. Settings + start_level start blank
// and are configured via the world editor.
func (s *Service) Create(ctx context.Context, in CreateInput) (*World, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, ErrInvalidName
	}
	var w World
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO worlds (name, settings_json, folder_id, created_by)
		VALUES ($1, '{}'::jsonb, $2, $3)
		RETURNING id, name, start_level_id, settings_json, folder_id,
		          created_by, created_at, updated_at
	`, in.Name, in.FolderID, in.CreatedBy).Scan(
		&w.ID, &w.Name, &w.StartLevelID, &w.SettingsJSON, &w.FolderID,
		&w.CreatedBy, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err, "worlds_name_key") {
			return nil, fmt.Errorf("%w: %q", ErrNameInUse, in.Name)
		}
		return nil, fmt.Errorf("create world: %w", err)
	}
	return &w, nil
}

// FindByID returns one world.
func (s *Service) FindByID(ctx context.Context, id int64) (*World, error) {
	var w World
	err := s.Pool.QueryRow(ctx, `
		SELECT id, name, start_level_id, settings_json, folder_id,
		       created_by, created_at, updated_at
		FROM worlds WHERE id = $1
	`, id).Scan(
		&w.ID, &w.Name, &w.StartLevelID, &w.SettingsJSON, &w.FolderID,
		&w.CreatedBy, &w.CreatedAt, &w.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrWorldNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find world: %w", err)
	}
	return &w, nil
}

// ListOpts gates List queries.
type ListOpts struct {
	Search   string
	FolderID *int64
	Limit    uint64
	Offset   uint64
}

// List returns worlds matching opts, ordered by name.
func (s *Service) List(ctx context.Context, opts ListOpts) ([]World, error) {
	q := `SELECT id, name, start_level_id, settings_json, folder_id,
	             created_by, created_at, updated_at
	      FROM worlds WHERE 1=1`
	args := []any{}
	idx := 1
	if opts.Search != "" {
		q += fmt.Sprintf(" AND name ILIKE $%d", idx)
		args = append(args, "%"+opts.Search+"%")
		idx++
	}
	if opts.FolderID != nil {
		q += fmt.Sprintf(" AND folder_id = $%d", idx)
		args = append(args, *opts.FolderID)
		idx++
	}
	q += " ORDER BY lower(name) ASC, id ASC"
	if opts.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}
	if opts.Offset > 0 {
		q += fmt.Sprintf(" OFFSET %d", opts.Offset)
	}
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list worlds: %w", err)
	}
	defer rows.Close()
	var out []World
	for rows.Next() {
		var w World
		if err := rows.Scan(
			&w.ID, &w.Name, &w.StartLevelID, &w.SettingsJSON, &w.FolderID,
			&w.CreatedBy, &w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Rename updates the display name.
func (s *Service) Rename(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidName
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE worlds SET name = $2, updated_at = now() WHERE id = $1`,
		id, name,
	)
	if err != nil {
		if isUniqueViolation(err, "worlds_name_key") {
			return fmt.Errorf("%w: %q", ErrNameInUse, name)
		}
		return fmt.Errorf("rename world: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWorldNotFound
	}
	return nil
}

// SetStartLevel records which level a player drops into when first
// entering this world. Pass nil to clear. Caller should validate the
// level belongs to this world (or doesn't yet, if attaching).
func (s *Service) SetStartLevel(ctx context.Context, worldID int64, levelID *int64) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE worlds SET start_level_id = $2, updated_at = now() WHERE id = $1`,
		worldID, levelID,
	)
	if err != nil {
		return fmt.Errorf("set start level: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWorldNotFound
	}
	return nil
}

// Delete removes a world. Levels in the world get their world_id
// NULL'd via ON DELETE SET NULL on levels.world_id; they survive but
// are no longer wired into the published graph.
func (s *Service) Delete(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM worlds WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete world: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWorldNotFound
	}
	return nil
}

// ---- helpers ----

func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		return false
	}
	return constraint == "" || pe.ConstraintName == constraint
}
