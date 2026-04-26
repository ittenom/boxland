// Package maps owns Map + Layer + Tile + Lighting CRUD plus the
// publish-pipeline handler. Per PLAN.md §1 "tiles ARE entities", the
// runtime materializes tiles into the ECS at instance-load time; this
// package is the *authoring* surface that persists their definitions.
package maps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/persistence/repo"
)

// Map is one row from the maps table.
type Map struct {
	ID                   int64           `db:"id"                          pk:"auto" json:"id"`
	Name                 string          `db:"name"                                  json:"name"`
	Width                int32           `db:"width"                                 json:"width"`
	Height               int32           `db:"height"                                json:"height"`
	Public               bool            `db:"public"                                json:"public"`
	InstancingMode       string          `db:"instancing_mode"                       json:"instancing_mode"`
	PersistenceMode      string          `db:"persistence_mode"                      json:"persistence_mode"`
	RefreshWindowSeconds *int32          `db:"refresh_window_seconds"                json:"refresh_window_seconds,omitempty"`
	ResetRulesJSON       json.RawMessage `db:"reset_rules_json"                      json:"reset_rules"`
	Mode                 string          `db:"mode"                                  json:"mode"`
	Seed                 *int64          `db:"seed"                                  json:"seed,omitempty"`
	SpectatorPolicy      string          `db:"spectator_policy"                      json:"spectator_policy"`
	CreatedBy            int64           `db:"created_by"                            json:"created_by"`
	CreatedAt            time.Time       `db:"created_at" repo:"readonly"            json:"created_at"`
	UpdatedAt            time.Time       `db:"updated_at" repo:"readonly"            json:"updated_at"`
}

// Layer is one row from map_layers.
type Layer struct {
	ID    int64  `db:"id"        pk:"auto" json:"id"`
	MapID int64  `db:"map_id"              json:"map_id"`
	Name  string `db:"name"                json:"name"`
	Kind  string `db:"kind"                json:"kind"` // "tile" | "lighting"
	Ord   int32  `db:"ord"                 json:"ord"`
	// YSortEntities turns on foot-position y-sorting for entities on
	// this layer. Designers opt this in for the layer the player walks
	// on (so trees, columns, etc. z-fight by y); leave off for terrain,
	// water, ceilings, lighting. See migration 0028.
	YSortEntities bool      `db:"y_sort_entities"     json:"y_sort_entities"`
	CreatedAt     time.Time `db:"created_at" repo:"readonly" json:"created_at"`
}

// Tile is one (map_id, layer_id, x, y) placement. No surrogate id; the
// composite primary key on the table is the natural identifier.
type Tile struct {
	MapID                  int64           `json:"map_id"`
	LayerID                int64           `json:"layer_id"`
	X                      int32           `json:"x"`
	Y                      int32           `json:"y"`
	EntityTypeID           int64           `json:"entity_type_id"`
	RotationDegrees        int16           `json:"rotation_degrees"`
	AnimOverride           *int16          `json:"anim_override,omitempty"`
	CollisionShapeOverride *int16          `json:"collision_shape_override,omitempty"`
	CollisionMaskOverride  *int64          `json:"collision_mask_override,omitempty"`
	CustomFlagsJSON        json.RawMessage `json:"custom_flags,omitempty"`
}

// LightingCell is one row from map_lighting_cells.
type LightingCell struct {
	MapID     int64 `json:"map_id"`
	LayerID   int64 `json:"layer_id"`
	X         int32 `json:"x"`
	Y         int32 `json:"y"`
	Color     int64 `json:"color"`     // 0xRRGGBBAA
	Intensity int16 `json:"intensity"` // 0..255
}

// Errors. Stable for HTTP handler mapping.
var (
	ErrMapNotFound   = errors.New("maps: map not found")
	ErrLayerNotFound = errors.New("maps: layer not found")
	ErrNameInUse     = errors.New("maps: name already exists")
	ErrLayerNameUsed = errors.New("maps: layer name already exists in this map")
)

// Service is the CRUD facade.
type Service struct {
	Pool *pgxpool.Pool
	Repo *repo.Repo[Map]
}

// New constructs a Service.
func New(pool *pgxpool.Pool) *Service {
	return &Service{Pool: pool, Repo: repo.New[Map](pool, "maps")}
}

// CreateInput drives Create.
type CreateInput struct {
	Name            string
	Width           int32
	Height          int32
	Public          bool
	InstancingMode  string
	PersistenceMode string
	Mode            string // "authored" | "procedural"
	Seed            *int64
	SpectatorPolicy string
	CreatedBy       int64
}

// Create inserts a new map row. Default layer set ("base", "decoration",
// "lighting") is created in the same transaction so designers always have
// somewhere to paint immediately.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Map, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("maps: name is required")
	}
	if in.Width < 1 || in.Height < 1 {
		return nil, errors.New("maps: width and height must be >= 1")
	}
	row := &Map{
		Name:            in.Name,
		Width:           in.Width,
		Height:          in.Height,
		Public:          in.Public,
		InstancingMode:  defaultStr(in.InstancingMode, "shared"),
		PersistenceMode: defaultStr(in.PersistenceMode, "persistent"),
		Mode:            defaultStr(in.Mode, "authored"),
		Seed:            in.Seed,
		SpectatorPolicy: defaultStr(in.SpectatorPolicy, "public"),
		ResetRulesJSON:  []byte("{}"),
		CreatedBy:       in.CreatedBy,
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	insertSQL := `
		INSERT INTO maps
			(name, width, height, public, instancing_mode, persistence_mode,
			 reset_rules_json, mode, seed, spectator_policy, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at
	`
	if err := tx.QueryRow(ctx, insertSQL,
		row.Name, row.Width, row.Height, row.Public,
		row.InstancingMode, row.PersistenceMode, row.ResetRulesJSON,
		row.Mode, row.Seed, row.SpectatorPolicy, row.CreatedBy,
	).Scan(&row.ID, &row.CreatedAt, &row.UpdatedAt); err != nil {
		if isUniqueViolation(err, "maps_name_key") {
			return nil, fmt.Errorf("%w: %q", ErrNameInUse, in.Name)
		}
		return nil, fmt.Errorf("create map: %w", err)
	}

	// Default layers. Designers can rename / add / remove later.
	defaults := []struct {
		Name string
		Kind string
		Ord  int32
	}{
		{"base", "tile", 0},
		{"decoration", "tile", 1},
		{"lighting", "lighting", 2},
	}
	for _, d := range defaults {
		if _, err := tx.Exec(ctx,
			`INSERT INTO map_layers (map_id, name, kind, ord) VALUES ($1, $2, $3, $4)`,
			row.ID, d.Name, d.Kind, d.Ord,
		); err != nil {
			return nil, fmt.Errorf("create default layer %q: %w", d.Name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return row, nil
}

// FindByID returns one map.
func (s *Service) FindByID(ctx context.Context, id int64) (*Map, error) {
	got, err := s.Repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrMapNotFound
		}
		return nil, err
	}
	return got, nil
}

// List returns every map ordered by name.
func (s *Service) List(ctx context.Context, search string) ([]Map, error) {
	opts := repo.ListOpts{Order: "name ASC, id ASC"}
	if search != "" {
		opts.Where = squirrel.ILike{"name": "%" + search + "%"}
	}
	return s.Repo.List(ctx, opts)
}

// ListPublic returns every public map ordered by name. Used by the
// player map picker (PLAN.md §6h); private maps stay invisible to the
// player realm even before the spectator-policy gate runs.
func (s *Service) ListPublic(ctx context.Context, search string) ([]Map, error) {
	opts := repo.ListOpts{Order: "name ASC, id ASC"}
	conds := squirrel.And{squirrel.Eq{"public": true}}
	if search != "" {
		conds = append(conds, squirrel.ILike{"name": "%" + search + "%"})
	}
	opts.Where = conds
	return s.Repo.List(ctx, opts)
}

// Delete removes a map. Layers + tiles cascade.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.Repo.Delete(ctx, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrMapNotFound
		}
		return err
	}
	return nil
}

// ---- Layers ----

// Layers returns every layer for the map ordered by ord.
func (s *Service) Layers(ctx context.Context, mapID int64) ([]Layer, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, map_id, name, kind, ord, y_sort_entities, created_at
		 FROM map_layers WHERE map_id = $1 ORDER BY ord ASC, id ASC`,
		mapID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Layer
	for rows.Next() {
		var l Layer
		if err := rows.Scan(&l.ID, &l.MapID, &l.Name, &l.Kind, &l.Ord, &l.YSortEntities, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// AddLayer inserts a new layer.
func (s *Service) AddLayer(ctx context.Context, mapID int64, name, kind string, ord int32) (*Layer, error) {
	row := s.Pool.QueryRow(ctx,
		`INSERT INTO map_layers (map_id, name, kind, ord) VALUES ($1, $2, $3, $4)
		 RETURNING id, map_id, name, kind, ord, y_sort_entities, created_at`,
		mapID, name, kind, ord,
	)
	var l Layer
	if err := row.Scan(&l.ID, &l.MapID, &l.Name, &l.Kind, &l.Ord, &l.YSortEntities, &l.CreatedAt); err != nil {
		if isUniqueViolation(err, "map_layers_map_id_name_key") {
			return nil, fmt.Errorf("%w: %q", ErrLayerNameUsed, name)
		}
		return nil, err
	}
	return &l, nil
}

// SetLayerYSort updates the y-sort flag on a layer. Tenant isolation is
// the caller's responsibility (the handler ensures the layer belongs to
// a map the designer owns).
func (s *Service) SetLayerYSort(ctx context.Context, layerID int64, on bool) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE map_layers SET y_sort_entities = $2 WHERE id = $1`,
		layerID, on,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLayerNotFound
	}
	return nil
}

// DeleteLayer removes a layer (and all tiles + lighting cells on it).
func (s *Service) DeleteLayer(ctx context.Context, layerID int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM map_layers WHERE id = $1`, layerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLayerNotFound
	}
	return nil
}

// ---- Tiles ----

// PlaceTiles upserts a batch of tile placements. Used by the Mapmaker's
// paint/rect/fill tools (which can each emit dozens of placements per
// stroke). Single transaction; existing (map_id, layer_id, x, y) rows
// are replaced.
func (s *Service) PlaceTiles(ctx context.Context, tiles []Tile) error {
	if len(tiles) == 0 {
		return nil
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, t := range tiles {
		flags := t.CustomFlagsJSON
		if len(flags) == 0 {
			flags = []byte("null")
		}
		if !ValidRotationDegrees(t.RotationDegrees) {
			return fmt.Errorf("place tile (%d,%d): invalid rotation %d", t.X, t.Y, t.RotationDegrees)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO map_tiles (map_id, layer_id, x, y, entity_type_id,
			                       rotation_degrees, anim_override, collision_shape_override,
			                       collision_mask_override, custom_flags_json)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
			ON CONFLICT (map_id, layer_id, x, y) DO UPDATE
			SET entity_type_id            = EXCLUDED.entity_type_id,
			    rotation_degrees         = EXCLUDED.rotation_degrees,
			    anim_override             = EXCLUDED.anim_override,
			    collision_shape_override  = EXCLUDED.collision_shape_override,
			    collision_mask_override   = EXCLUDED.collision_mask_override,
			    custom_flags_json         = EXCLUDED.custom_flags_json
		`, t.MapID, t.LayerID, t.X, t.Y, t.EntityTypeID, t.RotationDegrees,
			t.AnimOverride, t.CollisionShapeOverride, t.CollisionMaskOverride, flags,
		); err != nil {
			return fmt.Errorf("place tile (%d,%d): %w", t.X, t.Y, err)
		}
	}
	return tx.Commit(ctx)
}

// EraseTiles deletes the named (map_id, layer_id, x, y) cells.
func (s *Service) EraseTiles(ctx context.Context, mapID, layerID int64, points [][2]int32) error {
	if len(points) == 0 {
		return nil
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, p := range points {
		if _, err := tx.Exec(ctx,
			`DELETE FROM map_tiles WHERE map_id = $1 AND layer_id = $2 AND x = $3 AND y = $4`,
			mapID, layerID, p[0], p[1],
		); err != nil {
			return fmt.Errorf("erase tile (%d,%d): %w", p[0], p[1], err)
		}
	}
	return tx.Commit(ctx)
}

// ChunkTiles returns every tile across every layer of `mapID` whose grid
// coords fall in [x0..x1, y0..y1] (inclusive). Used by the chunked map
// loader (task #103) to materialize one chunk into the ECS in a single
// query.
func (s *Service) ChunkTiles(ctx context.Context, mapID int64, x0, y0, x1, y1 int32) ([]Tile, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT map_id, layer_id, x, y, entity_type_id, rotation_degrees, anim_override,
		       collision_shape_override, collision_mask_override, custom_flags_json
		FROM map_tiles
		WHERE map_id = $1 AND x BETWEEN $2 AND $3 AND y BETWEEN $4 AND $5
	`, mapID, x0, x1, y0, y1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tile
	for rows.Next() {
		var t Tile
		if err := rows.Scan(&t.MapID, &t.LayerID, &t.X, &t.Y, &t.EntityTypeID, &t.RotationDegrees,
			&t.AnimOverride, &t.CollisionShapeOverride, &t.CollisionMaskOverride, &t.CustomFlagsJSON,
		); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- Lighting cells ----

// PlaceLightingCells upserts a batch.
func (s *Service) PlaceLightingCells(ctx context.Context, cells []LightingCell) error {
	if len(cells) == 0 {
		return nil
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, c := range cells {
		if _, err := tx.Exec(ctx, `
			INSERT INTO map_lighting_cells (map_id, layer_id, x, y, color, intensity)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (map_id, layer_id, x, y) DO UPDATE
			SET color = EXCLUDED.color, intensity = EXCLUDED.intensity
		`, c.MapID, c.LayerID, c.X, c.Y, c.Color, c.Intensity,
		); err != nil {
			return fmt.Errorf("place lighting (%d,%d): %w", c.X, c.Y, err)
		}
	}
	return tx.Commit(ctx)
}

// ---- helpers ----

func ValidRotationDegrees(v int16) bool {
	switch v {
	case 0, 90, 180, 270:
		return true
	default:
		return false
	}
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		return false
	}
	return constraint == "" || pe.ConstraintName == constraint
}
