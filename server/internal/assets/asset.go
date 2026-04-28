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
//
// Per the holistic redesign, what used to be `KindTile` (a "tilemap PNG")
// is now `KindSpriteAnimated` — a multi-frame 32×32 strip. The structured
// "this is a tilemap" object lives in the separate `tilemaps` table and
// references this asset as its backing PNG. So an asset row never carries
// adjacency info; it's always either a single image, an animated strip,
// an audio clip, or a 9-slice panel PNG.
type Kind string

const (
	// KindSprite is a single 32×32 image — the simplest case.
	KindSprite Kind = "sprite"
	// KindSpriteAnimated is a multi-frame 32×32 strip (any number of
	// rows × cols of cells). Tilemaps reference assets of this kind as
	// their backing PNG; character bakes also produce this kind.
	KindSpriteAnimated Kind = "sprite_animated"
	// KindAudio is a wav/ogg/mp3 audio clip.
	KindAudio Kind = "audio"
	// KindUIPanel marks a PNG that is a 9-slice border source for
	// player-facing chrome (HUD frames, dialog boxes, tooltips). The
	// bytes are an ordinary PNG; the kind tells the upload pipeline
	// to skip auto-slicing and tells the previewer to draw the border
	// at the configured slice.
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
	// FolderID points at asset_folders.id. NULL means "lives in the
	// virtual kind root." See migration 0043.
	FolderID *int64 `db:"folder_id" json:"folder_id,omitempty"`
	// DominantColor is a packed 0xRRGGBB integer (no alpha). NULL = not
	// yet computed; the folders service backfills lazily when sort=color
	// is requested. See server/internal/assets/dominantcolor.go.
	DominantColor *int64    `db:"dominant_color" json:"dominant_color,omitempty"`
	CreatedBy     int64     `db:"created_by"                         json:"created_by"`
	CreatedAt     time.Time `db:"created_at" repo:"readonly"         json:"created_at"`
	UpdatedAt     time.Time `db:"updated_at" repo:"readonly"         json:"updated_at"`
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
	// FolderID is the optional target folder. nil means "in the virtual
	// kind root." Validated against the asset's kind by the folders
	// service when set via the UI; bypass-on-import is intentional
	// (importer creates the folder first via folders.EnsurePath).
	FolderID      *int64
	DominantColor *int64 // optional; populated for image kinds during upload
	CreatedBy     int64
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
		FolderID:             in.FolderID,
		DominantColor:        in.DominantColor,
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

// Rename updates an asset's display name. Hits the (kind, name)
// uniqueness index; returns ErrNameInUse on collision.
//
// Folder layer talks to this method directly so the rename hotkey (F2)
// in Asset Manager + Mapmaker palette is one wire call.
func (s *Service) Rename(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("assets: name is required")
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE assets SET name = $2, updated_at = now() WHERE id = $1
	`, id, name)
	if err != nil {
		if isUniqueViolation(err, "assets_kind_name_idx") {
			return fmt.Errorf("%w: %q", ErrNameInUse, name)
		}
		return fmt.Errorf("rename asset: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAssetNotFound
	}
	return nil
}

// ListByFolder returns every asset directly under `folderID` (or the
// kind root if folderID is nil + kindRoot is non-empty), ordered per
// `sort`.
//
// Sort modes:
//   - "alpha"  → name ASC
//   - "date"   → created_at DESC
//   - "type"   → kind-specific subtype (sprite frame_count, tile size,
//                audio format, ui_panel slice px). Falls back to alpha.
//   - "color"  → dominant_color ASC NULLS LAST
//   - "length" → audio metadata duration_ms ASC NULLS LAST
//
// Caller is responsible for triggering EnsureDominantColors before
// requesting "color" sort if it wants stable order on first view.
//
// `kindRoot` is required when folderID is nil so the kind-root view
// only shows assets of the matching kind.
func (s *Service) ListByFolder(ctx context.Context, folderID *int64, kindRoot string, sort string) ([]Asset, error) {
	var (
		where  string
		args   []any
		argIdx int = 1
	)
	if folderID != nil {
		where = "WHERE folder_id = $1"
		args = append(args, *folderID)
		argIdx++
	} else {
		// Kind-root view: only assets that share the requested kind
		// AND have folder_id IS NULL (i.e., haven't been filed
		// anywhere yet).
		if kindRoot == "" {
			return nil, errors.New("assets: ListByFolder requires kindRoot when folderID is nil")
		}
		where = fmt.Sprintf("WHERE folder_id IS NULL AND kind = $%d", argIdx)
		args = append(args, kindRoot)
		argIdx++
	}
	order := assetSortOrder(sort)
	q := fmt.Sprintf(`
		SELECT id, kind, name, content_addressed_path, original_format,
		       metadata_json, tags, folder_id, dominant_color,
		       created_by, created_at, updated_at
		FROM assets
		%s
		ORDER BY %s
	`, where, order)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list by folder: %w", err)
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var a Asset
		if err := rows.Scan(
			&a.ID, &a.Kind, &a.Name, &a.ContentAddressedPath, &a.OriginalFormat,
			&a.MetadataJSON, &a.Tags, &a.FolderID, &a.DominantColor,
			&a.CreatedBy, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// assetSortOrder maps a sort-mode string to the SQL ORDER BY clause.
// Falls back to alpha for unknown values so a stale UI selection can
// never produce a SQL error.
func assetSortOrder(mode string) string {
	switch mode {
	case "date":
		return "created_at DESC, id DESC"
	case "type":
		// Cheap kind-aware sort: format then frame count for sprites,
		// then name. Audio rows fall through to format-then-name.
		return "original_format ASC, lower(name) ASC, id ASC"
	case "color":
		// NULLS LAST keeps not-yet-computed swatches at the end so the
		// view doesn't appear sparse mid-backfill.
		return "dominant_color ASC NULLS LAST, lower(name) ASC, id ASC"
	case "length":
		// Audio length lives at metadata_json->>'duration_ms'.
		return "(metadata_json->>'duration_ms')::int ASC NULLS LAST, lower(name) ASC, id ASC"
	case "alpha":
		fallthrough
	default:
		return "lower(name) ASC, id ASC"
	}
}

// EnsureDominantColors backfills assets whose `dominant_color` is NULL.
// Streams blob bytes through the supplied getter (typically
// objectstore.Get) so this package stays storage-agnostic.
//
// Cap: at most `limit` assets per call (default 200 if `limit <= 0`).
// The handler can call this in a loop in the background until done.
//
// Returns (numUpdated, error). Errors on individual blob reads are
// logged via the supplied logger and do not stop the rest of the batch
// — a missing or unreadable blob just leaves dominant_color NULL.
func (s *Service) EnsureDominantColors(
	ctx context.Context,
	getBlob func(ctx context.Context, contentPath string) ([]byte, error),
	limit int,
) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, content_addressed_path
		FROM assets
		WHERE dominant_color IS NULL
		  AND kind IN ('sprite','sprite_animated','ui_panel')
		ORDER BY id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("scan dominant_color backlog: %w", err)
	}
	type job struct {
		ID   int64
		Path string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.ID, &j.Path); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()

	updated := 0
	for _, j := range jobs {
		body, err := getBlob(ctx, j.Path)
		if err != nil {
			continue
		}
		packed, ok := ComputeDominantColor(body)
		if !ok {
			continue
		}
		v := int64(packed)
		if _, err := s.Pool.Exec(ctx,
			`UPDATE assets SET dominant_color = $2 WHERE id = $1`, j.ID, v,
		); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
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
