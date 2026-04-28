// Package entities owns entity-type CRUD + the publish-pipeline handler.
// Component-kind metadata lives in the sibling components package; this
// package wires components onto entity types and persists the result.
package entities

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

	"boxland/server/internal/entities/components"
	"boxland/server/internal/persistence/repo"
)

// Class is the entity-class discriminator. Mirrors the
// entity_types.entity_class CHECK constraint. Per the holistic
// redesign, ENTITY is the broad parent class with four subtypes:
//
//   tile   — a sprite designed to stamp into a map's tile grid;
//            tilemap_id + cell_col + cell_row link back to the source
//            tilemap.
//   npc    — a non-player character; recipe_id + active_bake_id link
//            into the character pipeline.
//   pc     — a player-controllable character template.
//   logic  — an invisible game-logic entity (spawn point, region
//            trigger, item consumer, level transition).
type Class string

const (
	ClassTile  Class = "tile"
	ClassNPC   Class = "npc"
	ClassPC    Class = "pc"
	ClassLogic Class = "logic"
)

// Valid reports whether c names a real entity class.
func (c Class) Valid() bool {
	switch c {
	case ClassTile, ClassNPC, ClassPC, ClassLogic:
		return true
	}
	return false
}

// EntityType is one row from entity_types. Components are loaded
// separately on demand (most surfaces don't need them).
type EntityType struct {
	ID    int64  `db:"id"   pk:"auto" json:"id"`
	Name  string `db:"name"           json:"name"`
	// EntityClass is the subtype discriminator. Defaults to "logic"
	// (see migration 0001_init).
	EntityClass Class `db:"entity_class" json:"entity_class"`

	// Render link. For tile-class entities, sprite_asset_id is the
	// tilemap's backing PNG and atlas_index is (cell_row*cols + cell_col).
	SpriteAssetID *int64 `db:"sprite_asset_id" json:"sprite_asset_id,omitempty"`
	// AtlasIndex is the row-major 32×32 cell within sprite_asset_id
	// that this entity's sprite uses. 0 for plain 32×32 sprites (the
	// only cell). Wire-format equivalent: Tile.frame.
	AtlasIndex         int32  `db:"atlas_index"          json:"atlas_index"`
	DefaultAnimationID *int64 `db:"default_animation_id" json:"default_animation_id,omitempty"`

	// Tile-class linkage. Populated only when entity_class='tile';
	// set together at tilemap-creation time so a tile entity always
	// knows which tilemap cell produced it.
	TilemapID *int64 `db:"tilemap_id" json:"tilemap_id,omitempty"`
	CellCol   *int32 `db:"cell_col"   json:"cell_col,omitempty"`
	CellRow   *int32 `db:"cell_row"   json:"cell_row,omitempty"`

	// Character linkage. Populated only when entity_class IN ('npc','pc').
	// recipe_id is the editing handle; active_bake_id points at the
	// most recently published bake. Both null on first save; the
	// publish pipeline fills active_bake_id and updates sprite_asset_id.
	RecipeID     *int64 `db:"recipe_id"      json:"recipe_id,omitempty"`
	ActiveBakeID *int64 `db:"active_bake_id" json:"active_bake_id,omitempty"`

	// Collider + render flags.
	ColliderW            int32 `db:"collider_w"             json:"collider_w"`
	ColliderH            int32 `db:"collider_h"             json:"collider_h"`
	ColliderAnchorX      int32 `db:"collider_anchor_x"      json:"collider_anchor_x"`
	ColliderAnchorY      int32 `db:"collider_anchor_y"      json:"collider_anchor_y"`
	DefaultCollisionMask int64 `db:"default_collision_mask" json:"default_collision_mask"`
	// YSortAnchor opts the type into foot-position y-sorting against
	// other entities on the same render layer. Default false preserves
	// the layer-only ordering existing entities rely on.
	YSortAnchor bool `db:"y_sort_anchor" json:"y_sort_anchor"`
	// DrawAbovePlayer pins the type's sprite above the player layer
	// regardless of y-sort. Wins over YSortAnchor when both are true.
	DrawAbovePlayer bool `db:"draw_above_player" json:"draw_above_player"`
	// ProceduralInclude controls whether this entity type appears in
	// procedural-WFC random fill. Default true. Designers flip the
	// eye-icon in the procedural-mode palette to mute tiles they
	// don't want auto-spawned. Hand-painting an excluded tile still
	// works.
	ProceduralInclude bool `db:"procedural_include" json:"procedural_include"`

	Tags      []string  `db:"tags"                       json:"tags"`
	CreatedBy int64     `db:"created_by"                 json:"created_by"`
	CreatedAt time.Time `db:"created_at" repo:"readonly" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" repo:"readonly" json:"updated_at"`
}

// ComponentRow is one row from entity_components.
type ComponentRow struct {
	EntityTypeID int64             `json:"entity_type_id"`
	Kind         components.Kind   `json:"kind"`
	ConfigJSON   json.RawMessage   `json:"config"`
}

// Errors returned by the service. Stable for HTTP handler mapping.
var (
	ErrEntityTypeNotFound = errors.New("entities: not found")
	ErrNameInUse          = errors.New("entities: name already exists")
)

// Service holds dependencies; constructed once at boot.
type Service struct {
	Pool   *pgxpool.Pool
	Repo   *repo.Repo[EntityType]
	Compos *components.Registry
}

// New builds a Service.
func New(pool *pgxpool.Pool, registry *components.Registry) *Service {
	return &Service{
		Pool:   pool,
		Repo:   repo.New[EntityType](pool, "entity_types"),
		Compos: registry,
	}
}

// CreateInput drives Create.
type CreateInput struct {
	Name string
	// EntityClass is the subtype. Defaults to ClassLogic when zero —
	// most call sites that don't set it are creating logic entities
	// (the catch-all). Tilemap auto-slice sets ClassTile; the
	// character publish path sets ClassNPC / ClassPC.
	EntityClass Class

	SpriteAssetID      *int64
	AtlasIndex         int32 // 32×32 cell within SpriteAssetID; 0 for single-sprite assets.
	DefaultAnimationID *int64

	// Tile-class linkage. Set together by the tilemap service.
	TilemapID *int64
	CellCol   *int32
	CellRow   *int32

	// Character-class linkage. Set by the character publish path.
	RecipeID     *int64
	ActiveBakeID *int64

	ColliderW            int32
	ColliderH            int32
	ColliderAnchorX      int32
	ColliderAnchorY      int32
	DefaultCollisionMask int64
	Tags                 []string
	CreatedBy            int64
}

// Create inserts a new entity-type row with the schema defaults filled in
// for any zero collider fields. Returns ErrNameInUse on conflict.
func (s *Service) Create(ctx context.Context, in CreateInput) (*EntityType, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("entities: name is required")
	}
	if in.EntityClass == "" {
		in.EntityClass = ClassLogic
	}
	if !in.EntityClass.Valid() {
		return nil, fmt.Errorf("entities: invalid entity_class %q", in.EntityClass)
	}
	row := &EntityType{
		Name:                 in.Name,
		EntityClass:          in.EntityClass,
		SpriteAssetID:        in.SpriteAssetID,
		AtlasIndex:           in.AtlasIndex,
		DefaultAnimationID:   in.DefaultAnimationID,
		TilemapID:            in.TilemapID,
		CellCol:              in.CellCol,
		CellRow:              in.CellRow,
		RecipeID:             in.RecipeID,
		ActiveBakeID:         in.ActiveBakeID,
		ColliderW:            valOrDefault(in.ColliderW, 16),
		ColliderH:            valOrDefault(in.ColliderH, 16),
		ColliderAnchorX:      valOrDefault(in.ColliderAnchorX, 8),
		ColliderAnchorY:      valOrDefault(in.ColliderAnchorY, 16),
		DefaultCollisionMask: valOrDefault(in.DefaultCollisionMask, 1),
		Tags:                 valOrEmpty(in.Tags),
		ProceduralInclude:    true,
		CreatedBy:            in.CreatedBy,
	}
	if err := s.Repo.Insert(ctx, row); err != nil {
		if isUniqueViolation(err, "entity_types_name_key") {
			return nil, fmt.Errorf("%w: %q", ErrNameInUse, in.Name)
		}
		return nil, fmt.Errorf("create entity type: %w", err)
	}
	return row, nil
}

// ListByClass returns every entity_type of the given class, ordered by
// name. The Library (Entities → Tiles / NPCs / PCs / Logic) and the
// Mapmaker palette (Tiles only) use this to scope the entity lookup
// without leaking unrelated rows.
func (s *Service) ListByClass(ctx context.Context, class Class, opts ListOpts) ([]EntityType, error) {
	if !class.Valid() {
		return nil, fmt.Errorf("entities: invalid entity_class %q", class)
	}
	clauses := squirrel.And{squirrel.Eq{"entity_class": string(class)}}
	if len(opts.Tags) > 0 {
		clauses = append(clauses, squirrel.Expr("tags && ?::text[]", opts.Tags))
	}
	if opts.Search != "" {
		clauses = append(clauses, squirrel.ILike{"name": "%" + opts.Search + "%"})
	}
	listOpts := repo.ListOpts{
		Where:  clauses,
		Order:  "lower(name) ASC, id ASC",
		Limit:  opts.Limit,
		Offset: opts.Offset,
	}
	return s.Repo.List(ctx, listOpts)
}

// FindByTilemapCell looks up the tile-class entity_type that came from
// a given tilemap cell. Returns ErrEntityTypeNotFound if the cell hasn't
// been sliced (yet) or the tilemap doesn't own a tile at that cell.
//
// Used by the tilemap service's Replace flow: when re-uploading a
// tilemap, cells whose pixel hash is unchanged keep the same
// entity_type_id (preserving every map_tiles reference), so we look
// the existing entity up by (tilemap_id, cell_col, cell_row) instead
// of by sprite_asset_id + atlas_index (which can collide across
// tilemap re-uploads when the backing PNG is replaced).
func (s *Service) FindByTilemapCell(ctx context.Context, tilemapID int64, col, row int32) (*EntityType, error) {
	out, err := s.Repo.List(ctx, repo.ListOpts{
		Where: squirrel.And{
			squirrel.Eq{"tilemap_id": tilemapID},
			squirrel.Eq{"cell_col": col},
			squirrel.Eq{"cell_row": row},
		},
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrEntityTypeNotFound
	}
	return &out[0], nil
}

// SetProceduralInclude flips the procedural_include flag on one entity
// type. Single indexed UPDATE; n+1 safe by construction.
func (s *Service) SetProceduralInclude(ctx context.Context, id int64, include bool) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE entity_types SET procedural_include = $2, updated_at = now() WHERE id = $1`,
		id, include,
	)
	if err != nil {
		return fmt.Errorf("set procedural_include: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrEntityTypeNotFound
	}
	return nil
}

// FindByID returns the entity type with the given id.
func (s *Service) FindByID(ctx context.Context, id int64) (*EntityType, error) {
	got, err := s.Repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrEntityTypeNotFound
		}
		return nil, err
	}
	return got, nil
}

// FindByName returns the entity type with the given name.
func (s *Service) FindByName(ctx context.Context, name string) (*EntityType, error) {
	out, err := s.Repo.List(ctx, repo.ListOpts{
		Where: squirrel.Eq{"name": name},
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrEntityTypeNotFound
	}
	return &out[0], nil
}

// ListOpts mirrors the asset surface for filter ergonomics.
type ListOpts struct {
	Tags   []string
	Search string
	Limit  uint64
	Offset uint64
}

// List returns entity types matching opts, ordered by name ASC.
func (s *Service) List(ctx context.Context, opts ListOpts) ([]EntityType, error) {
	var clauses squirrel.And
	if len(opts.Tags) > 0 {
		clauses = append(clauses, squirrel.Expr("tags && ?::text[]", opts.Tags))
	}
	if opts.Search != "" {
		clauses = append(clauses, squirrel.ILike{"name": "%" + opts.Search + "%"})
	}
	listOpts := repo.ListOpts{
		Order:  "name ASC, id ASC",
		Limit:  opts.Limit,
		Offset: opts.Offset,
	}
	if len(clauses) > 0 {
		listOpts.Where = clauses
	}
	return s.Repo.List(ctx, listOpts)
}

// EntityTypeMeta is the minimal subset of EntityType the chunked map
// loader needs (PLAN.md §4f). Returning a small struct avoids exposing
// the full row to callers that only need collider + sprite metadata.
type EntityTypeMeta struct {
	ID                   int64
	SpriteAssetID        *int64
	AtlasIndex           int32
	DefaultAnimationID   *int64
	ColliderW            int32
	ColliderH            int32
	ColliderAnchorX      int32
	ColliderAnchorY      int32
	DefaultCollisionMask int64
}

// EntityTypeMeta returns the loader-shaped subset for `id`.
// Implements the EntityTypeLookup interface defined in
// internal/maps/loader.go.
func (s *Service) EntityTypeMeta(ctx context.Context, id int64) (*EntityTypeMeta, error) {
	got, err := s.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &EntityTypeMeta{
		ID:                   got.ID,
		SpriteAssetID:        got.SpriteAssetID,
		AtlasIndex:           got.AtlasIndex,
		DefaultAnimationID:   got.DefaultAnimationID,
		ColliderW:            got.ColliderW,
		ColliderH:            got.ColliderH,
		ColliderAnchorX:      got.ColliderAnchorX,
		ColliderAnchorY:      got.ColliderAnchorY,
		DefaultCollisionMask: got.DefaultCollisionMask,
	}, nil
}

// FindBySpriteAtlas returns every entity_type that already references
// a given (sprite_asset_id, atlas_index) cell. The auto-slice tile-
// upload pipeline uses this for idempotency: re-uploading the same
// sheet (or splitting it later) won't produce duplicate palette
// entries for cells that already have an entity.
//
// Returned slice is keyed by atlas_index in the caller — usually a
// quick map[int32]EntityType — so we ORDER BY atlas_index for
// deterministic iteration.
func (s *Service) FindBySpriteAtlas(ctx context.Context, spriteAssetID int64) ([]EntityType, error) {
	return s.Repo.List(ctx, repo.ListOpts{
		Where: squirrel.Eq{"sprite_asset_id": spriteAssetID},
		Order: "atlas_index ASC, id ASC",
	})
}

// Delete removes one entity type. Components cascade via FK.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.Repo.Delete(ctx, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrEntityTypeNotFound
		}
		return err
	}
	return nil
}

// ---- Components ----

// Components returns every (kind, config) row for the entity type, ordered
// alphabetically by kind for stable UI presentation.
func (s *Service) Components(ctx context.Context, entityTypeID int64) ([]ComponentRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT entity_type_id, component_kind, config_json
		FROM entity_components WHERE entity_type_id = $1
		ORDER BY component_kind
	`, entityTypeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ComponentRow
	for rows.Next() {
		var r ComponentRow
		var kindStr string
		if err := rows.Scan(&r.EntityTypeID, &kindStr, &r.ConfigJSON); err != nil {
			return nil, err
		}
		r.Kind = components.Kind(kindStr)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetComponents replaces every component row for the entity type within
// `tx`. Validates each kind against the registry first; returns the first
// error before mutating any row. Pass nil tx for a one-shot transaction.
func (s *Service) SetComponents(ctx context.Context, tx pgx.Tx, entityTypeID int64, configs map[components.Kind]json.RawMessage) error {
	if err := s.Compos.ValidateAll(configs); err != nil {
		return err
	}

	owned := tx == nil
	if owned {
		var err error
		tx, err = s.Pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM entity_components WHERE entity_type_id = $1`, entityTypeID,
	); err != nil {
		return fmt.Errorf("clear components: %w", err)
	}
	for k, raw := range configs {
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO entity_components (entity_type_id, component_kind, config_json)
			VALUES ($1, $2, $3::jsonb)
		`, entityTypeID, string(k), raw); err != nil {
			return fmt.Errorf("insert component %s: %w", k, err)
		}
	}
	if owned {
		return tx.Commit(ctx)
	}
	return nil
}

// ---- helpers ----

func valOrDefault[T comparable](v, d T) T {
	var zero T
	if v == zero {
		return d
	}
	return v
}

func valOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		return false
	}
	return constraint == "" || pe.ConstraintName == constraint
}
