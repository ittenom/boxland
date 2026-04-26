// Package assets implements the design-tool asset pipeline:
// upload, parsing, palette-variant baking, and CRUD via the generic
// Repo[T] / Artifact[T] frameworks. See PLAN.md §4d.
package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/persistence/repo"
)

// Kind discriminates asset types. Mirrors the assets.kind CHECK constraint.
type Kind string

const (
	KindSprite  Kind = "sprite"
	KindTile    Kind = "tile"
	KindAudio   Kind = "audio"
	// KindUIPanel marks a PNG that is a 9-slice border source for
	// player-facing chrome (HUD frames, dialog boxes, tooltips). The
	// bytes are an ordinary PNG; the kind tells the upload pipeline
	// to skip tile-sheet auto-slicing and tells the previewer to draw
	// the border at the configured slice. See docs/adding-a-component.md
	// "Nine-slice panels".
	KindUIPanel Kind = "ui_panel"
)

// Asset is one row from the assets table.
//
// Tag conventions (see internal/persistence/repo):
//   db:"col"        column name
//   pk:"auto"       auto-generated primary key
//   repo:"readonly" populated by the DB (RETURNING refreshes it on Insert,
//                   excluded from INSERT/UPDATE column lists)
//
// JSON tags use snake_case so the wire representation matches the SQL
// column names + the FlatBuffers conventions in /schemas/.
type Asset struct {
	ID                   int64           `db:"id"                       pk:"auto" json:"id"`
	Kind                 Kind            `db:"kind"                               json:"kind"`
	Name                 string          `db:"name"                               json:"name"`
	ContentAddressedPath string          `db:"content_addressed_path"             json:"content_addressed_path"`
	OriginalFormat       string          `db:"original_format"                    json:"original_format"`
	MetadataJSON         json.RawMessage `db:"metadata_json"                      json:"metadata"`
	Tags                 []string        `db:"tags"                               json:"tags"`
	CreatedBy            int64           `db:"created_by"                         json:"created_by"`
	CreatedAt            time.Time       `db:"created_at" repo:"readonly"         json:"created_at"`
	UpdatedAt            time.Time       `db:"updated_at" repo:"readonly"         json:"updated_at"`
}

// Errors returned by the asset service. Stable for handler mapping.
var (
	ErrAssetNotFound = errors.New("assets: not found")
	ErrNameInUse     = errors.New("assets: (kind, name) already exists")
)

// Service holds dependencies the asset surface needs. Public field so
// tests can swap or inspect the Repo if needed.
//
// Importers, when non-nil, drives the sprite-upload pre-flight: PNGs
// arriving without an Aseprite sidecar get auto-sliced + walk_*
// animations synthesized at upload time. Tests pass nil to skip the
// importer path; production wires `assets.DefaultRegistry()` here in
// cmd/boxland.
type Service struct {
	Pool      *pgxpool.Pool
	Repo      *repo.Repo[Asset]
	Importers *Registry
}

// New constructs a Service. The importer registry is wired separately
// so existing test setup (which passes a bare Pool) keeps working;
// callers wanting upload-time animation synthesis should set
// `svc.Importers = assets.DefaultRegistry()` after New.
func New(pool *pgxpool.Pool) *Service {
	return &Service{
		Pool: pool,
		Repo: repo.New[Asset](pool, "assets"),
	}
}

// CreateInput is what the upload handler hands to Create() after the file
// has been pushed to object storage. The handler is responsible for the
// blob upload; this service owns the metadata row.
type CreateInput struct {
	Kind                 Kind
	Name                 string
	ContentAddressedPath string
	OriginalFormat       string
	MetadataJSON         []byte
	Tags                 []string
	CreatedBy            int64
}

// Create inserts one row. Returns ErrNameInUse on (kind, name) conflict.
//
// Idempotency note: callers should look up an existing row by
// content_addressed_path BEFORE calling Create if they want "uploading the
// same bytes twice doesn't create a duplicate." The upload handler in this
// package implements that policy.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Asset, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("assets: name is required")
	}
	if in.ContentAddressedPath == "" {
		return nil, errors.New("assets: content_addressed_path is required")
	}
	if in.MetadataJSON == nil {
		in.MetadataJSON = []byte("{}")
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	row := &Asset{
		Kind:                 in.Kind,
		Name:                 in.Name,
		ContentAddressedPath: in.ContentAddressedPath,
		OriginalFormat:       in.OriginalFormat,
		MetadataJSON:         in.MetadataJSON,
		Tags:                 in.Tags,
		CreatedBy:            in.CreatedBy,
	}
	if err := s.Repo.Insert(ctx, row); err != nil {
		if isUniqueViolation(err, "assets_kind_name_idx") {
			return nil, fmt.Errorf("%w: kind=%s name=%q", ErrNameInUse, in.Kind, in.Name)
		}
		return nil, fmt.Errorf("create asset: %w", err)
	}
	return row, nil
}

// FindByContentPath returns the asset stored at the given content-addressed
// path, if any. Used by the upload handler to dedup re-uploaded bytes.
//
// `kind` filter is required because the same bytes (e.g. a PNG) may have
// been imported as both a sprite and a tile asset.
func (s *Service) FindByContentPath(ctx context.Context, kind Kind, path string) (*Asset, error) {
	rows, err := s.Repo.List(ctx, repo.ListOpts{
		Where: squirrel.And{
			squirrel.Eq{"kind": string(kind)},
			squirrel.Eq{"content_addressed_path": path},
		},
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrAssetNotFound
	}
	return &rows[0], nil
}

// FindByID returns one asset.
func (s *Service) FindByID(ctx context.Context, id int64) (*Asset, error) {
	a, err := s.Repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrAssetNotFound
		}
		return nil, err
	}
	return a, nil
}

// ListByIDs returns the assets matching the given ids in one query.
// Order is not guaranteed; callers that need a stable order should
// sort the result, but most use a map (id -> Asset) — see the
// Mapmaker palette builder, which is the primary consumer and the
// reason this exists (avoiding an N+1 over entity_types).
//
// Missing ids are silently dropped (no ErrAssetNotFound) — the
// caller decides what an absent id means in context. Empty input
// returns an empty slice without hitting the DB.
func (s *Service) ListByIDs(ctx context.Context, ids []int64) ([]Asset, error) {
	if len(ids) == 0 {
		return []Asset{}, nil
	}
	return s.Repo.List(ctx, repo.ListOpts{
		Where: squirrel.Eq{"id": ids},
		// Bound to len(ids) so a future caller passing a giant slice
		// can't blow the default Limit on Repo.List.
		Limit: uint64(len(ids)),
	})
}

// ListOpts are pagination + filter options exposed to handlers.
type ListOpts struct {
	Kind   Kind     // empty = all kinds
	Tags   []string // ANY-of match against assets.tags
	Search string   // ILIKE on name
	Limit  uint64
	Offset uint64
}

// List returns assets matching opts, ordered by created_at DESC.
func (s *Service) List(ctx context.Context, opts ListOpts) ([]Asset, error) {
	var clauses squirrel.And
	if opts.Kind != "" {
		clauses = append(clauses, squirrel.Eq{"kind": string(opts.Kind)})
	}
	if len(opts.Tags) > 0 {
		// `tags && ARRAY[...]::text[]` -> rows whose tags overlap the filter.
		clauses = append(clauses, squirrel.Expr("tags && ?::text[]", opts.Tags))
	}
	if opts.Search != "" {
		clauses = append(clauses, squirrel.ILike{"name": "%" + opts.Search + "%"})
	}
	listOpts := repo.ListOpts{
		Order:  "created_at DESC, id DESC",
		Limit:  opts.Limit,
		Offset: opts.Offset,
	}
	if len(clauses) > 0 {
		listOpts.Where = clauses
	}
	return s.Repo.List(ctx, listOpts)
}

// Delete removes an asset by id. Variants and animations cascade via FK.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.Repo.Delete(ctx, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrAssetNotFound
		}
		return err
	}
	return nil
}

// isUniqueViolation matches the helper in internal/auth/designer; duplicated
// here so the asset package doesn't depend on the auth package.
func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		return false
	}
	return constraint == "" || pe.ConstraintName == constraint
}
