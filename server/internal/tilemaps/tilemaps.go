// Package tilemaps owns the structured "tilemap" object: a backing
// PNG asset (kind=sprite_animated) sliced into 32×32 cells, with one
// tile-class entity_type per non-empty cell, plus the adjacency graph
// implied by the source sheet's layout.
//
// Per the holistic redesign, a TILEMAP is a "collection of sprites
// meant to be used as tiles" whose layout is semantically significant
// in both axes — adjacent cells are likely connecting tiles. We keep
// that information machine-readable instead of throwing it away at
// slice time:
//
//   * Each non-empty cell carries a pixel_hash (sha256 of the cell's
//     RGBA bytes) plus four edge_hash_n/e/s/w (sha256 of each 32-pixel
//     edge strip). The edge hashes power auto-extracted edge sockets:
//     cells whose north edge equals another cell's south edge get the
//     same socket assignment automatically.
//
//   * Adjacency is derived from cell coordinates on demand
//     (AdjacencyGraph) rather than persisted as a graph table — the
//     coordinates are the canonical source of truth and we never want
//     them to drift from a derived index.
//
//   * Replace re-uploads the backing PNG and diffs cells by pixel_hash:
//     unchanged cells keep their entity_type_id (preserving every
//     map_tiles reference), changed cells get re-hashed in place,
//     vanished cells are removed (or left if still referenced by a
//     map_tiles row).
package tilemaps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
)

// Tilemap is one row from the tilemaps table.
type Tilemap struct {
	ID            int64     `json:"id"`
	AssetID       int64     `json:"asset_id"`
	Name          string    `json:"name"`
	Cols          int32     `json:"cols"`
	Rows          int32     `json:"rows"`
	TileSize      int32     `json:"tile_size"`
	NonEmptyCount int32     `json:"non_empty_count"`
	FolderID      *int64    `json:"folder_id,omitempty"`
	CreatedBy     int64     `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Cell is one row of tilemap_tiles. Carries the hashes used by the
// adjacency / socket extraction logic.
type Cell struct {
	TilemapID    int64
	CellCol      int32
	CellRow      int32
	EntityTypeID int64
	PixelHash    [32]byte
	EdgeHashN    [32]byte
	EdgeHashE    [32]byte
	EdgeHashS    [32]byte
	EdgeHashW    [32]byte
}

// Direction names a cell's edge for adjacency / socket reasoning.
type Direction string

const (
	DirNorth Direction = "n"
	DirEast  Direction = "e"
	DirSouth Direction = "s"
	DirWest  Direction = "w"
)

// Adjacency is one edge in a tilemap's adjacency graph: cell (FromCol,
// FromRow) is `Dir`-adjacent to cell (ToCol, ToRow), both non-empty.
type Adjacency struct {
	FromCol, FromRow int32
	ToCol, ToRow     int32
	Dir              Direction
}

// Errors. Stable for HTTP handler mapping.
var (
	ErrTilemapNotFound  = errors.New("tilemaps: not found")
	ErrNameInUse        = errors.New("tilemaps: name already exists")
	ErrAssetAlreadyUsed = errors.New("tilemaps: backing asset already powers another tilemap")
	ErrInvalidName      = errors.New("tilemaps: name is required")
	ErrEmptySheet       = errors.New("tilemaps: every cell is empty")
)

// Service is the tilemap CRUD facade. It composes assets + entities so
// "create a tilemap" is one call from the upload handler.
type Service struct {
	Pool     *pgxpool.Pool
	Assets   *assets.Service
	Entities *entities.Service
}

// New constructs a Service. Caller is responsible for sharing a single
// instance across handlers (stateless per call).
func New(pool *pgxpool.Pool, assetsSvc *assets.Service, entitiesSvc *entities.Service) *Service {
	return &Service{Pool: pool, Assets: assetsSvc, Entities: entitiesSvc}
}

// CreateInput drives Create.
type CreateInput struct {
	Name      string
	AssetID   int64
	FolderID  *int64
	CreatedBy int64
	// Cells + Meta come from the upload pipeline (assets.SliceTileSheet
	// already ran during upload pre-flight). We accept them as input
	// so we don't have to re-decode the PNG here. PngBody is also
	// required so the service can compute the per-cell pixel and edge
	// hashes — those hashes can't be reconstructed from cell metadata
	// alone.
	Cells   []assets.TileCell
	Meta    assets.TileSheetMetadata
	PngBody []byte
}

// Create writes a new tilemap row, slices the PNG into per-cell
// tile-class entity_types, and writes the corresponding tilemap_tiles
// rows with pixel + edge hashes. All in one transaction; partial
// failures roll back cleanly.
//
// Idempotency: re-creating a tilemap pointing at an asset_id that
// already powers a tilemap returns ErrAssetAlreadyUsed. Use Replace
// to swap the backing PNG of an existing tilemap.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Tilemap, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, ErrInvalidName
	}
	if in.AssetID == 0 {
		return nil, errors.New("tilemaps: asset_id is required")
	}
	if len(in.Cells) == 0 || in.Meta.NonEmptyCount == 0 {
		return nil, ErrEmptySheet
	}

	_, hashes, err := decodeAndHash(in.PngBody, in.Meta)
	if err != nil {
		return nil, err
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tm Tilemap
	err = tx.QueryRow(ctx, `
		INSERT INTO tilemaps (asset_id, name, cols, rows, tile_size, non_empty_count, folder_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, asset_id, name, cols, rows, tile_size, non_empty_count, folder_id,
		          created_by, created_at, updated_at
	`,
		in.AssetID, in.Name,
		int32(in.Meta.Cols), int32(in.Meta.Rows),
		int32(in.Meta.TileSize), int32(in.Meta.NonEmptyCount),
		in.FolderID, in.CreatedBy,
	).Scan(
		&tm.ID, &tm.AssetID, &tm.Name, &tm.Cols, &tm.Rows, &tm.TileSize, &tm.NonEmptyCount,
		&tm.FolderID, &tm.CreatedBy, &tm.CreatedAt, &tm.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err, "tilemaps_name_key") {
			return nil, fmt.Errorf("%w: %q", ErrNameInUse, in.Name)
		}
		if isUniqueViolation(err, "tilemaps_asset_id_key") {
			return nil, fmt.Errorf("%w: asset_id=%d", ErrAssetAlreadyUsed, in.AssetID)
		}
		return nil, fmt.Errorf("insert tilemap: %w", err)
	}

	// Per-cell fan-out. One entity_type row + one tilemap_tiles row
	// per non-empty cell. Skip empty cells entirely.
	for _, c := range in.Cells {
		if !c.NonEmpty {
			continue
		}
		col, row := int32(c.Col), int32(c.Row)
		entityName := defaultTileEntityName(in.Name, col, row)
		atlasIndex := int32(c.Index)
		assetID := in.AssetID
		entityID, err := s.createTileEntity(ctx, tx, entityName, &assetID, atlasIndex, &tm.ID, col, row, in.CreatedBy)
		if err != nil {
			return nil, fmt.Errorf("create cell entity (col=%d row=%d): %w", col, row, err)
		}
		h := hashes[hashKey(c.Col, c.Row)]
		if _, err := tx.Exec(ctx, `
			INSERT INTO tilemap_tiles (tilemap_id, cell_col, cell_row, entity_type_id,
			                           pixel_hash, edge_hash_n, edge_hash_e, edge_hash_s, edge_hash_w)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, tm.ID, col, row, entityID,
			h.Pixel[:], h.EdgeN[:], h.EdgeE[:], h.EdgeS[:], h.EdgeW[:],
		); err != nil {
			return nil, fmt.Errorf("insert tilemap_tiles row (col=%d row=%d): %w", col, row, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tilemap: %w", err)
	}
	return &tm, nil
}

// FindByID returns one tilemap, or ErrTilemapNotFound.
func (s *Service) FindByID(ctx context.Context, id int64) (*Tilemap, error) {
	var tm Tilemap
	err := s.Pool.QueryRow(ctx, `
		SELECT id, asset_id, name, cols, rows, tile_size, non_empty_count, folder_id,
		       created_by, created_at, updated_at
		FROM tilemaps WHERE id = $1
	`, id).Scan(
		&tm.ID, &tm.AssetID, &tm.Name, &tm.Cols, &tm.Rows, &tm.TileSize, &tm.NonEmptyCount,
		&tm.FolderID, &tm.CreatedBy, &tm.CreatedAt, &tm.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTilemapNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find tilemap: %w", err)
	}
	return &tm, nil
}

// FindByAssetID returns the tilemap whose backing PNG is the given
// asset, if any. ErrTilemapNotFound when the asset isn't a tilemap's
// backing. Used by the upload handler to detect "this PNG was already
// imported as a tilemap; treat the upload as a re-import."
func (s *Service) FindByAssetID(ctx context.Context, assetID int64) (*Tilemap, error) {
	var tm Tilemap
	err := s.Pool.QueryRow(ctx, `
		SELECT id, asset_id, name, cols, rows, tile_size, non_empty_count, folder_id,
		       created_by, created_at, updated_at
		FROM tilemaps WHERE asset_id = $1
	`, assetID).Scan(
		&tm.ID, &tm.AssetID, &tm.Name, &tm.Cols, &tm.Rows, &tm.TileSize, &tm.NonEmptyCount,
		&tm.FolderID, &tm.CreatedBy, &tm.CreatedAt, &tm.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTilemapNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find tilemap by asset: %w", err)
	}
	return &tm, nil
}

// ListOpts mirrors the asset surface for filter ergonomics.
type ListOpts struct {
	Search   string
	FolderID *int64 // nil = all; pass &id to scope to one folder
	Limit    uint64
	Offset   uint64
}

// List returns tilemaps matching opts, ordered by name.
func (s *Service) List(ctx context.Context, opts ListOpts) ([]Tilemap, error) {
	q := `SELECT id, asset_id, name, cols, rows, tile_size, non_empty_count, folder_id,
	             created_by, created_at, updated_at
	      FROM tilemaps WHERE 1=1`
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
		return nil, fmt.Errorf("list tilemaps: %w", err)
	}
	defer rows.Close()
	var out []Tilemap
	for rows.Next() {
		var tm Tilemap
		if err := rows.Scan(
			&tm.ID, &tm.AssetID, &tm.Name, &tm.Cols, &tm.Rows, &tm.TileSize, &tm.NonEmptyCount,
			&tm.FolderID, &tm.CreatedBy, &tm.CreatedAt, &tm.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, tm)
	}
	return out, rows.Err()
}

// Rename updates the display name. Returns ErrNameInUse on collision.
func (s *Service) Rename(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidName
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE tilemaps SET name = $2, updated_at = now() WHERE id = $1`,
		id, name,
	)
	if err != nil {
		if isUniqueViolation(err, "tilemaps_name_key") {
			return fmt.Errorf("%w: %q", ErrNameInUse, name)
		}
		return fmt.Errorf("rename tilemap: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTilemapNotFound
	}
	return nil
}

// Delete removes a tilemap. Cascades to tilemap_tiles AND to the
// per-cell tile-class entity_types (FK CASCADE on
// entity_types.tilemap_id). map_tiles rows that reference any of
// those entity_types will block the delete via the
// `ON DELETE RESTRICT` on map_tiles.entity_type_id; the handler
// surfaces that as "this tilemap is in use on map X."
func (s *Service) Delete(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM tilemaps WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete tilemap: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTilemapNotFound
	}
	return nil
}

// AdjacencyGraph returns every (non-empty cell) → (non-empty neighbor)
// edge in the tilemap's grid. Derived from cell coordinates; no
// neighbor table to drift.
//
// Each adjacency is reported once per direction (so a cell pair shows
// up twice — once as (A → B north) and once as (B → A south) — which
// keeps downstream socket-extraction code simple).
func (s *Service) AdjacencyGraph(ctx context.Context, tilemapID int64) ([]Adjacency, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT cell_col, cell_row FROM tilemap_tiles
		WHERE tilemap_id = $1
	`, tilemapID)
	if err != nil {
		return nil, fmt.Errorf("load cells: %w", err)
	}
	defer rows.Close()
	type pt struct{ Col, Row int32 }
	cells := map[pt]struct{}{}
	for rows.Next() {
		var c, r int32
		if err := rows.Scan(&c, &r); err != nil {
			return nil, err
		}
		cells[pt{c, r}] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Adjacency
	for cell := range cells {
		neighbors := [4]struct {
			Dir      Direction
			Col, Row int32
		}{
			{DirNorth, cell.Col, cell.Row - 1},
			{DirEast, cell.Col + 1, cell.Row},
			{DirSouth, cell.Col, cell.Row + 1},
			{DirWest, cell.Col - 1, cell.Row},
		}
		for _, n := range neighbors {
			if _, ok := cells[pt{n.Col, n.Row}]; !ok {
				continue
			}
			out = append(out, Adjacency{
				FromCol: cell.Col, FromRow: cell.Row,
				ToCol: n.Col, ToRow: n.Row,
				Dir: n.Dir,
			})
		}
	}
	return out, nil
}

// Cells returns every tilemap_tiles row for the given tilemap, ordered
// by (cell_row, cell_col).
func (s *Service) Cells(ctx context.Context, tilemapID int64) ([]Cell, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT tilemap_id, cell_col, cell_row, entity_type_id,
		       pixel_hash, edge_hash_n, edge_hash_e, edge_hash_s, edge_hash_w
		FROM tilemap_tiles WHERE tilemap_id = $1
		ORDER BY cell_row ASC, cell_col ASC
	`, tilemapID)
	if err != nil {
		return nil, fmt.Errorf("load cells: %w", err)
	}
	defer rows.Close()
	var out []Cell
	for rows.Next() {
		var c Cell
		var px, en, ee, es, ew []byte
		if err := rows.Scan(
			&c.TilemapID, &c.CellCol, &c.CellRow, &c.EntityTypeID,
			&px, &en, &ee, &es, &ew,
		); err != nil {
			return nil, err
		}
		copy(c.PixelHash[:], px)
		copy(c.EdgeHashN[:], en)
		copy(c.EdgeHashE[:], ee)
		copy(c.EdgeHashS[:], es)
		copy(c.EdgeHashW[:], ew)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- helpers ----

// cellHashes carries the per-cell hash quintet computed once at
// slice/replace time.
type cellHashes struct {
	Pixel [32]byte
	EdgeN [32]byte
	EdgeE [32]byte
	EdgeS [32]byte
	EdgeW [32]byte
}

// hashKey packs (col, row) into a single int64 map key. Reasonable for
// cells up to 2^31 wide / 2^31 tall — far beyond the largest tilemap a
// designer will build.
func hashKey(col, row int) int64 {
	return int64(col)<<32 | int64(uint32(row))
}

// decodeAndHash decodes the PNG and computes the per-cell pixel + edge
// hashes for every non-empty cell. Returns (image, hashesByCellKey).
//
// We hash each cell from a normalized RGBA byte slice (4 bytes per
// pixel, row-major) so the hash is independent of the source PNG's
// color model (paletted vs. RGBA produce identical hashes for visually
// identical pixels).
func decodeAndHash(pngBody []byte, meta assets.TileSheetMetadata) (image.Image, map[int64]cellHashes, error) {
	img, err := png.Decode(bytes.NewReader(pngBody))
	if err != nil {
		return nil, nil, fmt.Errorf("decode png: %w", err)
	}
	if meta.TileSize == 0 {
		meta.TileSize = assets.TileSize
	}
	ts := meta.TileSize
	hashes := make(map[int64]cellHashes, meta.NonEmptyCount)

	for _, idx := range meta.NonEmptyIndex {
		col := idx % meta.Cols
		row := idx / meta.Cols
		h := hashCell(img, col*ts, row*ts, ts)
		hashes[hashKey(col, row)] = h
	}
	return img, hashes, nil
}

// hashCell builds the pixel + edge hashes for one ts×ts cell starting
// at (x0, y0).
func hashCell(img image.Image, x0, y0, ts int) cellHashes {
	cell := make([]byte, ts*ts*4)
	for y := 0; y < ts; y++ {
		for x := 0; x < ts; x++ {
			r, g, b, a := img.At(x0+x, y0+y).RGBA()
			i := (y*ts + x) * 4
			cell[i+0] = byte(r >> 8)
			cell[i+1] = byte(g >> 8)
			cell[i+2] = byte(b >> 8)
			cell[i+3] = byte(a >> 8)
		}
	}
	edgeN := make([]byte, ts*4)
	edgeS := make([]byte, ts*4)
	edgeW := make([]byte, ts*4)
	edgeE := make([]byte, ts*4)
	for c := 0; c < ts; c++ {
		copy(edgeN[c*4:c*4+4], cell[c*4:c*4+4])
		copy(edgeS[c*4:c*4+4], cell[(ts-1)*ts*4+c*4:(ts-1)*ts*4+c*4+4])
	}
	for r := 0; r < ts; r++ {
		copy(edgeW[r*4:r*4+4], cell[r*ts*4:r*ts*4+4])
		copy(edgeE[r*4:r*4+4], cell[r*ts*4+(ts-1)*4:r*ts*4+(ts-1)*4+4])
	}
	return cellHashes{
		Pixel: sha256.Sum256(cell),
		EdgeN: sha256.Sum256(edgeN),
		EdgeE: sha256.Sum256(edgeE),
		EdgeS: sha256.Sum256(edgeS),
		EdgeW: sha256.Sum256(edgeW),
	}
}

// createTileEntity is the shared helper that mints a tile-class
// entity_type for one cell. Uses raw SQL inside the supplied tx
// because entities.Service.Create wants its own pool — and a
// half-committed tilemap with stray entity rows would be much worse
// than the brief duplication.
func (s *Service) createTileEntity(
	ctx context.Context,
	tx pgx.Tx,
	name string,
	spriteAssetID *int64,
	atlasIndex int32,
	tilemapID *int64,
	col, row int32,
	createdBy int64,
) (int64, error) {
	// Suffix-loop on name collisions so two tilemaps named "Forest"
	// don't fight over "Forest #r0c0". Cheap because we hit the unique
	// index at most a handful of times in practice.
	base := name
	for attempt := 0; attempt < 100; attempt++ {
		var id int64
		err := tx.QueryRow(ctx, `
			INSERT INTO entity_types (
				name, entity_class, sprite_asset_id, atlas_index,
				tilemap_id, cell_col, cell_row, created_by
			) VALUES ($1, 'tile', $2, $3, $4, $5, $6, $7)
			RETURNING id
		`, name, spriteAssetID, atlasIndex, tilemapID, col, row, createdBy).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !isUniqueViolation(err, "entity_types_name_key") {
			return 0, err
		}
		name = fmt.Sprintf("%s #%d", base, attempt+2)
	}
	return 0, fmt.Errorf("create tile entity: too many name collisions for %q", base)
}

// defaultTileEntityName builds the canonical "<tilemap> #r{R}c{C}"
// name for an auto-sliced tile entity.
func defaultTileEntityName(tilemapName string, col, row int32) string {
	return fmt.Sprintf("%s #r%dc%d", tilemapName, row, col)
}

// isUniqueViolation matches the helper pattern used across the codebase.
func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		return false
	}
	return constraint == "" || pe.ConstraintName == constraint
}
