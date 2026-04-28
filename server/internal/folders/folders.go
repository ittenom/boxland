// Package folders owns the IDE filesystem: folder CRUD, sort modes,
// the recursive-CTE tree fetcher that powers the IDE-style sidebar,
// and bulk-move helpers for moving items into folders.
//
// Design points (also in 0001_init.up.sql):
//
//   - Six "virtual" top-level roots: sprite / tilemap / audio / ui_panel
//     for the asset library, plus level / world for the level/world
//     trees. Roots are NOT real rows — a folder's `kind_root` says
//     which root it belongs to; an item with folder_id NULL lives in
//     the kind root itself.
//
//   - Folder delete cascades to child folders but spares contents:
//     assets/tilemaps/levels/worlds.folder_id are all
//     `ON DELETE SET NULL`. Designers do not lose work by deleting a
//     folder.
//
//   - Cycle prevention on Move uses a recursive-CTE membership check
//     (one query, indexed on parent_id). No application-side ancestor
//     walk → no race window if two designers reparent at once.
//
//   - Tree fetch is one indexed query per kind_root. The shape returned
//     is a flat slice; the view layer builds the tree.
//
// Note on naming: the table is still called `asset_folders` for
// migration economy; semantically it's "folders." Most call sites read
// fine.
package folders

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KindRoot is the six valid top-level kinds a folder can belong to.
// String values mirror the kind discriminators on the back-reference
// tables (assets.kind / tilemaps / levels / worlds) so the FK side of
// decisions stays trivially readable.
type KindRoot string

const (
	// Asset library roots — back-reference is `assets.folder_id`.
	KindSprite  KindRoot = "sprite"
	KindAudio   KindRoot = "audio"
	KindUIPanel KindRoot = "ui_panel"
	// Tilemap library root — back-reference is `tilemaps.folder_id`.
	// Tilemaps are first-class objects (with their own table) rather
	// than asset rows; their backing PNGs are stored as
	// `assets.kind = 'sprite_animated'` and don't get filed
	// independently.
	KindTilemap KindRoot = "tilemap"
	// Object roots — back-reference is `<table>.folder_id`.
	KindLevel KindRoot = "level"
	KindWorld KindRoot = "world"
)

// AllKindRoots returns the six virtual roots in canonical order. Used
// by the rail renderer + tests.
func AllKindRoots() []KindRoot {
	return []KindRoot{KindSprite, KindTilemap, KindAudio, KindUIPanel, KindLevel, KindWorld}
}

// AssetKindRoots returns the kind_roots whose contents live in the
// `assets` table (back-referenced by `assets.folder_id`).
func AssetKindRoots() []KindRoot {
	return []KindRoot{KindSprite, KindAudio, KindUIPanel}
}

// Valid reports whether s names a real kind root.
func (k KindRoot) Valid() bool {
	switch k {
	case KindSprite, KindTilemap, KindAudio, KindUIPanel, KindLevel, KindWorld:
		return true
	}
	return false
}

// IsAssetKind reports whether folders of this root contain `assets`
// rows (vs. tilemaps/levels/worlds rows). The folder service's
// MoveAssets helper rejects moves into non-asset folders.
func (k KindRoot) IsAssetKind() bool {
	switch k {
	case KindSprite, KindAudio, KindUIPanel:
		return true
	}
	return false
}

// SortMode is the per-folder ordering strategy. Persisted as the
// folder's `sort_mode` column.
type SortMode string

const (
	SortAlpha  SortMode = "alpha"
	SortDate   SortMode = "date"
	SortType   SortMode = "type"
	SortColor  SortMode = "color"
	SortLength SortMode = "length"
)

// Valid reports whether s names a real sort mode.
func (m SortMode) Valid() bool {
	switch m {
	case SortAlpha, SortDate, SortType, SortColor, SortLength:
		return true
	}
	return false
}

// AvailableSortModes returns the sort modes that make sense for one
// kind_root. The UI uses this to hide irrelevant options.
//
//   - color is image-only (sprite / tilemap / ui_panel).
//   - length is audio-only (length comes from metadata.duration_ms).
//   - type is shown for the asset roots (every asset kind has subtypes
//     — sprites have frame counts, audio has format, ui_panel has
//     slice px). For level/world roots type is omitted (they have no
//     useful subtype).
func AvailableSortModes(kr KindRoot) []SortMode {
	switch kr {
	case KindAudio:
		return []SortMode{SortAlpha, SortDate, SortType, SortLength}
	case KindSprite, KindTilemap, KindUIPanel:
		return []SortMode{SortAlpha, SortDate, SortType, SortColor}
	case KindLevel, KindWorld:
		return []SortMode{SortAlpha, SortDate}
	}
	return []SortMode{SortAlpha, SortDate}
}

// Folder is one row of asset_folders.
type Folder struct {
	ID        int64     `json:"id"`
	ParentID  *int64    `json:"parent_id,omitempty"`
	KindRoot  KindRoot  `json:"kind_root"`
	Name      string    `json:"name"`
	SortMode  SortMode  `json:"sort_mode"`
	CreatedBy int64     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Errors. Stable for HTTP handler mapping.
var (
	ErrNotFound        = errors.New("folders: not found")
	ErrNameInUse       = errors.New("folders: name already exists in this parent")
	ErrInvalidKindRoot = errors.New("folders: invalid kind_root")
	ErrInvalidSortMode = errors.New("folders: invalid sort_mode")
	ErrInvalidName     = errors.New("folders: name is required")
	ErrCycle           = errors.New("folders: move would create a cycle")
	ErrCrossKindMove   = errors.New("folders: cannot move across kind_root")
	ErrNotAssetKind    = errors.New("folders: target folder is not an asset folder")
)

// Service is the public CRUD facade. Constructed once at boot and
// shared across handlers (stateless per call).
type Service struct {
	Pool *pgxpool.Pool
}

// New constructs a Service.
func New(pool *pgxpool.Pool) *Service { return &Service{Pool: pool} }

// CreateInput drives Create.
type CreateInput struct {
	Name      string
	KindRoot  KindRoot
	ParentID  *int64
	SortMode  SortMode // optional; "alpha" used if empty
	CreatedBy int64
}

// Create inserts a new folder. Validates parent shares the same
// kind_root and that the name doesn't collide within the parent.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Folder, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, ErrInvalidName
	}
	if !in.KindRoot.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidKindRoot, in.KindRoot)
	}
	if in.SortMode == "" {
		in.SortMode = SortAlpha
	}
	if !in.SortMode.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidSortMode, in.SortMode)
	}

	if in.ParentID != nil {
		var parentKind KindRoot
		err := s.Pool.QueryRow(ctx,
			`SELECT kind_root FROM asset_folders WHERE id = $1`,
			*in.ParentID,
		).Scan(&parentKind)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: parent %d", ErrNotFound, *in.ParentID)
		}
		if err != nil {
			return nil, fmt.Errorf("lookup parent: %w", err)
		}
		if parentKind != in.KindRoot {
			return nil, fmt.Errorf("%w: parent is %s, child wants %s",
				ErrCrossKindMove, parentKind, in.KindRoot)
		}
	}

	var f Folder
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO asset_folders (parent_id, kind_root, name, sort_mode, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, parent_id, kind_root, name, sort_mode, created_by, created_at, updated_at
	`, in.ParentID, string(in.KindRoot), in.Name, string(in.SortMode), in.CreatedBy,
	).Scan(&f.ID, &f.ParentID, &f.KindRoot, &f.Name, &f.SortMode, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err, "asset_folders_parent_name_idx") {
			return nil, fmt.Errorf("%w: %q", ErrNameInUse, in.Name)
		}
		return nil, fmt.Errorf("create folder: %w", err)
	}
	return &f, nil
}

// FindByID returns one folder, or ErrNotFound.
func (s *Service) FindByID(ctx context.Context, id int64) (*Folder, error) {
	var f Folder
	err := s.Pool.QueryRow(ctx, `
		SELECT id, parent_id, kind_root, name, sort_mode, created_by, created_at, updated_at
		FROM asset_folders WHERE id = $1
	`, id).Scan(&f.ID, &f.ParentID, &f.KindRoot, &f.Name, &f.SortMode, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find folder: %w", err)
	}
	return &f, nil
}

// Rename updates a folder's display name. Hits the per-parent
// uniqueness index; returns ErrNameInUse on collision.
func (s *Service) Rename(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidName
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE asset_folders SET name = $2, updated_at = now() WHERE id = $1
	`, id, name)
	if err != nil {
		if isUniqueViolation(err, "asset_folders_parent_name_idx") {
			return fmt.Errorf("%w: %q", ErrNameInUse, name)
		}
		return fmt.Errorf("rename folder: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSortMode flips the per-folder ordering strategy.
func (s *Service) SetSortMode(ctx context.Context, id int64, mode SortMode) error {
	if !mode.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidSortMode, mode)
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE asset_folders SET sort_mode = $2, updated_at = now() WHERE id = $1
	`, id, string(mode))
	if err != nil {
		return fmt.Errorf("set sort_mode: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Move reparents a folder. Validates:
//   - target parent exists (or is nil = move to kind root)
//   - target parent's kind_root matches the folder's
//   - target is not the folder itself or any descendant (cycle check)
//
// Cycle check uses a recursive CTE so it's one query and uses the
// indexed parent_id column.
func (s *Service) Move(ctx context.Context, id int64, newParentID *int64) error {
	cur, err := s.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if newParentID != nil {
		if *newParentID == id {
			return ErrCycle
		}
		var pKind KindRoot
		err := s.Pool.QueryRow(ctx,
			`SELECT kind_root FROM asset_folders WHERE id = $1`, *newParentID,
		).Scan(&pKind)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: parent %d", ErrNotFound, *newParentID)
		}
		if err != nil {
			return fmt.Errorf("lookup new parent: %w", err)
		}
		if pKind != cur.KindRoot {
			return fmt.Errorf("%w: %s vs %s", ErrCrossKindMove, pKind, cur.KindRoot)
		}
		var cycle bool
		err = s.Pool.QueryRow(ctx, `
			WITH RECURSIVE descendants AS (
				SELECT id FROM asset_folders WHERE id = $1
				UNION ALL
				SELECT f.id FROM asset_folders f
				JOIN descendants d ON f.parent_id = d.id
			)
			SELECT EXISTS(SELECT 1 FROM descendants WHERE id = $2)
		`, id, *newParentID).Scan(&cycle)
		if err != nil {
			return fmt.Errorf("cycle check: %w", err)
		}
		if cycle {
			return ErrCycle
		}
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE asset_folders SET parent_id = $2, updated_at = now() WHERE id = $1
	`, id, newParentID)
	if err != nil {
		if isUniqueViolation(err, "asset_folders_parent_name_idx") {
			return fmt.Errorf("%w: target parent already has a folder named %q", ErrNameInUse, cur.Name)
		}
		return fmt.Errorf("move folder: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a folder. Children cascade (CASCADE on parent_id);
// items in the folder bubble up to the kind root via SET NULL on the
// back-reference columns.
func (s *Service) Delete(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM asset_folders WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByKindRoot returns every folder under one kind_root in
// breadth-first-ish order (parents conceptually before children;
// alphabetical within each level). One indexed query.
//
// The view layer turns the flat slice into a tree by parent_id.
func (s *Service) ListByKindRoot(ctx context.Context, kr KindRoot) ([]Folder, error) {
	if !kr.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidKindRoot, kr)
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, parent_id, kind_root, name, sort_mode, created_by, created_at, updated_at
		FROM asset_folders
		WHERE kind_root = $1
		ORDER BY COALESCE(parent_id, 0) ASC, lower(name) ASC, id ASC
	`, string(kr))
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.ParentID, &f.KindRoot, &f.Name, &f.SortMode, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Path returns the slash-joined ancestor path of `id`, e.g.
// "forest/trees". Used by the exporter so a re-import recreates the
// hierarchy. Returns "" for id == 0 (kind-root level).
//
// One recursive CTE.
func (s *Service) Path(ctx context.Context, id int64) (string, error) {
	if id == 0 {
		return "", nil
	}
	rows, err := s.Pool.Query(ctx, `
		WITH RECURSIVE chain AS (
			SELECT id, parent_id, name, 0 AS depth
			FROM asset_folders WHERE id = $1
			UNION ALL
			SELECT f.id, f.parent_id, f.name, c.depth + 1
			FROM asset_folders f
			JOIN chain c ON c.parent_id = f.id
		)
		SELECT name FROM chain ORDER BY depth DESC
	`, id)
	if err != nil {
		return "", fmt.Errorf("folder path: %w", err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "", err
		}
		parts = append(parts, n)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return strings.Join(parts, "/"), nil
}

// PathsByID returns id → "forest/trees" for every id in `ids`. One
// query (recursive CTE seeded by ANY($1)). Used by the exporter to
// resolve folder paths in bulk without N+1.
//
// Empty input returns an empty map without hitting the DB.
func (s *Service) PathsByID(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `
		WITH RECURSIVE chain AS (
			SELECT id AS root, id, parent_id, name, 0 AS depth
			FROM asset_folders WHERE id = ANY($1::bigint[])
			UNION ALL
			SELECT c.root, f.id, f.parent_id, f.name, c.depth + 1
			FROM asset_folders f
			JOIN chain c ON c.parent_id = f.id
		)
		SELECT root, name, depth FROM chain ORDER BY root, depth DESC
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("paths by id: %w", err)
	}
	defer rows.Close()
	parts := make(map[int64][]string, len(ids))
	for rows.Next() {
		var root int64
		var n string
		var d int
		if err := rows.Scan(&root, &n, &d); err != nil {
			return nil, err
		}
		parts[root] = append(parts[root], n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for k, v := range parts {
		out[k] = strings.Join(v, "/")
	}
	return out, nil
}

// EnsurePath walks down a "/"-separated path under one kind_root,
// creating folders as needed. Returns the leaf folder id (or 0 if
// `path` is empty, meaning "the kind root itself").
//
// Used by the importer when an exported envelope carries a
// folder_path. Idempotent: re-importing the same export twice doesn't
// create duplicate folders (case-insensitive name match within parent).
func (s *Service) EnsurePath(ctx context.Context, kr KindRoot, path string, designerID int64) (int64, error) {
	path = strings.Trim(path, "/")
	if path == "" {
		return 0, nil
	}
	if !kr.Valid() {
		return 0, fmt.Errorf("%w: %q", ErrInvalidKindRoot, kr)
	}
	parts := strings.Split(path, "/")
	var parentID *int64
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var existingID int64
		var query string
		var args []any
		if parentID == nil {
			query = `SELECT id FROM asset_folders
				WHERE parent_id IS NULL AND kind_root = $1 AND lower(name) = lower($2) LIMIT 1`
			args = []any{string(kr), p}
		} else {
			query = `SELECT id FROM asset_folders
				WHERE parent_id = $1 AND kind_root = $2 AND lower(name) = lower($3) LIMIT 1`
			args = []any{*parentID, string(kr), p}
		}
		err := s.Pool.QueryRow(ctx, query, args...).Scan(&existingID)
		switch {
		case err == nil:
			parentID = &existingID
			continue
		case errors.Is(err, pgx.ErrNoRows):
			// Fall through to create.
		default:
			return 0, fmt.Errorf("lookup folder %q: %w", p, err)
		}
		f, err := s.Create(ctx, CreateInput{
			Name:      p,
			KindRoot:  kr,
			ParentID:  parentID,
			CreatedBy: designerID,
		})
		if err != nil {
			return 0, fmt.Errorf("create folder %q: %w", p, err)
		}
		id := f.ID
		parentID = &id
	}
	if parentID == nil {
		return 0, nil
	}
	return *parentID, nil
}

// MoveAssets bulk-reparents assets to a target folder (or kind root if
// targetFolderID is nil). One UPDATE; safe to call with thousands of
// ids. Validates that every asset shares the same kind as the target
// folder so the UI can't accidentally drop a sprite into an Audio
// folder.
//
// Returns the number of rows actually moved (helpful for toast copy).
//
// The target folder must be an asset-kind folder (sprite / audio /
// ui_panel). To move tilemaps/levels/worlds, use the corresponding
// service's move helper.
func (s *Service) MoveAssets(ctx context.Context, assetIDs []int64, targetFolderID *int64) (int64, error) {
	if len(assetIDs) == 0 {
		return 0, nil
	}
	if targetFolderID != nil {
		f, err := s.FindByID(ctx, *targetFolderID)
		if err != nil {
			return 0, err
		}
		if !f.KindRoot.IsAssetKind() {
			return 0, fmt.Errorf("%w: target folder kind=%s", ErrNotAssetKind, f.KindRoot)
		}
		var bad int
		err = s.Pool.QueryRow(ctx, `
			SELECT count(*) FROM assets WHERE id = ANY($1::bigint[]) AND kind <> $2
		`, assetIDs, string(f.KindRoot)).Scan(&bad)
		if err != nil {
			return 0, fmt.Errorf("kind check: %w", err)
		}
		if bad > 0 {
			return 0, fmt.Errorf("%w: %d assets have a different kind from the target folder",
				ErrCrossKindMove, bad)
		}
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE assets SET folder_id = $2, updated_at = now() WHERE id = ANY($1::bigint[])
	`, assetIDs, targetFolderID)
	if err != nil {
		return 0, fmt.Errorf("move assets: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MoveTilemaps, MoveLevels, MoveWorlds reparent the corresponding
// object rows into a folder of the matching kind_root. Bulk-safe.
func (s *Service) MoveTilemaps(ctx context.Context, tilemapIDs []int64, targetFolderID *int64) (int64, error) {
	return s.moveObjects(ctx, "tilemaps", KindTilemap, tilemapIDs, targetFolderID)
}
func (s *Service) MoveLevels(ctx context.Context, levelIDs []int64, targetFolderID *int64) (int64, error) {
	return s.moveObjects(ctx, "levels", KindLevel, levelIDs, targetFolderID)
}
func (s *Service) MoveWorlds(ctx context.Context, worldIDs []int64, targetFolderID *int64) (int64, error) {
	return s.moveObjects(ctx, "worlds", KindWorld, worldIDs, targetFolderID)
}

// moveObjects is the shared implementation: validate target is the
// expected kind_root, then UPDATE folder_id on the named table. The
// table name is hard-coded per call site so this is not a SQL-injection
// vector — only the three exported wrappers above can call it.
func (s *Service) moveObjects(ctx context.Context, table string, expected KindRoot, ids []int64, targetFolderID *int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if targetFolderID != nil {
		f, err := s.FindByID(ctx, *targetFolderID)
		if err != nil {
			return 0, err
		}
		if f.KindRoot != expected {
			return 0, fmt.Errorf("%w: target folder kind=%s, want %s",
				ErrCrossKindMove, f.KindRoot, expected)
		}
	}
	q := fmt.Sprintf(
		`UPDATE %s SET folder_id = $2, updated_at = now() WHERE id = ANY($1::bigint[])`,
		table,
	)
	tag, err := s.Pool.Exec(ctx, q, ids, targetFolderID)
	if err != nil {
		return 0, fmt.Errorf("move %s: %w", table, err)
	}
	return tag.RowsAffected(), nil
}

// isUniqueViolation matches the helper pattern used across the codebase.
func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		return false
	}
	return constraint == "" || pe.ConstraintName == constraint
}
