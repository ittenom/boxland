// Package levels owns the LEVEL surface: a level references a MAP for
// geometry plus its own non-tile entity placements, level-scoped HUD,
// instancing/persistence settings, automations, and (optionally) a
// world membership.
//
// Per the holistic redesign:
//
//   MAP   = pure tile geometry (internal/maps).
//   LEVEL = a MAP + entity placements + automations + HUD + instancing.
//   WORLD = a graph of LEVELs connected by transition entities.
//
// One MAP can back many LEVELs (e.g. day/night variants of a town
// share geometry but layer different NPCs and HUD). Tile placements
// stay on the map (they belong to the geometry, not the staging);
// non-tile entity placements live in the level_entities table managed
// here.
package levels

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

// Level is one row of the levels table.
type Level struct {
	ID                     int64           `json:"id"`
	Name                   string          `json:"name"`
	MapID                  int64           `json:"map_id"`
	WorldID                *int64          `json:"world_id,omitempty"`
	Public                 bool            `json:"public"`
	InstancingMode         string          `json:"instancing_mode"`
	PersistenceMode        string          `json:"persistence_mode"`
	RefreshWindowSeconds   *int32          `json:"refresh_window_seconds,omitempty"`
	ResetRulesJSON         json.RawMessage `json:"reset_rules"`
	SpectatorPolicy        string          `json:"spectator_policy"`
	HUDLayoutJSON          json.RawMessage `json:"hud_layout"`
	FolderID               *int64          `json:"folder_id,omitempty"`
	CreatedBy              int64           `json:"created_by"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

// LevelEntity is one placement in a level. NPCs, doors, spawn points,
// region triggers, item consumers — anything with coordinates that
// isn't part of the tile grid.
type LevelEntity struct {
	ID                     int64           `json:"id"`
	LevelID                int64           `json:"level_id"`
	EntityTypeID           int64           `json:"entity_type_id"`
	X                      int32           `json:"x"`
	Y                      int32           `json:"y"`
	RotationDegrees        int16           `json:"rotation_degrees"`
	InstanceOverridesJSON  json.RawMessage `json:"instance_overrides"`
	Tags                   []string        `json:"tags"`
	CreatedAt              time.Time       `json:"created_at"`
}

// Errors. Stable for HTTP handler mapping.
var (
	ErrLevelNotFound       = errors.New("levels: not found")
	ErrLevelEntityNotFound = errors.New("levels: entity placement not found")
	ErrNameInUse           = errors.New("levels: name already exists")
	ErrInvalidName         = errors.New("levels: name is required")
	ErrMapMissing          = errors.New("levels: map_id is required")
	ErrInvalidMode         = errors.New("levels: invalid mode")
)

// Service is the level CRUD facade.
type Service struct {
	Pool *pgxpool.Pool
}

// New constructs a Service.
func New(pool *pgxpool.Pool) *Service { return &Service{Pool: pool} }

// CreateInput drives Create.
type CreateInput struct {
	Name                 string
	MapID                int64
	WorldID              *int64
	Public               bool
	InstancingMode       string
	PersistenceMode      string
	RefreshWindowSeconds *int32
	SpectatorPolicy      string
	FolderID             *int64
	CreatedBy            int64
}

// Create inserts a new level row. Defaults match the schema CHECK
// constraints so most call sites can leave the enum fields zero.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Level, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, ErrInvalidName
	}
	if in.MapID == 0 {
		return nil, ErrMapMissing
	}
	if in.InstancingMode == "" {
		in.InstancingMode = "shared"
	}
	if !validInstancing(in.InstancingMode) {
		return nil, fmt.Errorf("%w: instancing %q", ErrInvalidMode, in.InstancingMode)
	}
	if in.PersistenceMode == "" {
		in.PersistenceMode = "persistent"
	}
	if !validPersistence(in.PersistenceMode) {
		return nil, fmt.Errorf("%w: persistence %q", ErrInvalidMode, in.PersistenceMode)
	}
	if in.SpectatorPolicy == "" {
		in.SpectatorPolicy = "public"
	}
	if !validSpectator(in.SpectatorPolicy) {
		return nil, fmt.Errorf("%w: spectator %q", ErrInvalidMode, in.SpectatorPolicy)
	}

	var lv Level
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO levels (
			name, map_id, world_id, public, instancing_mode, persistence_mode,
			refresh_window_seconds, reset_rules_json, spectator_policy,
			hud_layout_json, folder_id, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, '{}'::jsonb, $8,
			'{"v":1,"anchors":{}}'::jsonb, $9, $10
		)
		RETURNING id, name, map_id, world_id, public, instancing_mode,
		          persistence_mode, refresh_window_seconds, reset_rules_json,
		          spectator_policy, hud_layout_json, folder_id, created_by,
		          created_at, updated_at
	`,
		in.Name, in.MapID, in.WorldID, in.Public,
		in.InstancingMode, in.PersistenceMode, in.RefreshWindowSeconds,
		in.SpectatorPolicy, in.FolderID, in.CreatedBy,
	).Scan(
		&lv.ID, &lv.Name, &lv.MapID, &lv.WorldID, &lv.Public,
		&lv.InstancingMode, &lv.PersistenceMode, &lv.RefreshWindowSeconds,
		&lv.ResetRulesJSON, &lv.SpectatorPolicy, &lv.HUDLayoutJSON,
		&lv.FolderID, &lv.CreatedBy, &lv.CreatedAt, &lv.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err, "levels_name_key") {
			return nil, fmt.Errorf("%w: %q", ErrNameInUse, in.Name)
		}
		return nil, fmt.Errorf("create level: %w", err)
	}
	return &lv, nil
}

// FindByID returns one level.
func (s *Service) FindByID(ctx context.Context, id int64) (*Level, error) {
	var lv Level
	err := s.Pool.QueryRow(ctx, `
		SELECT id, name, map_id, world_id, public, instancing_mode,
		       persistence_mode, refresh_window_seconds, reset_rules_json,
		       spectator_policy, hud_layout_json, folder_id, created_by,
		       created_at, updated_at
		FROM levels WHERE id = $1
	`, id).Scan(
		&lv.ID, &lv.Name, &lv.MapID, &lv.WorldID, &lv.Public,
		&lv.InstancingMode, &lv.PersistenceMode, &lv.RefreshWindowSeconds,
		&lv.ResetRulesJSON, &lv.SpectatorPolicy, &lv.HUDLayoutJSON,
		&lv.FolderID, &lv.CreatedBy, &lv.CreatedAt, &lv.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrLevelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find level: %w", err)
	}
	return &lv, nil
}

// ListOpts gates List queries.
type ListOpts struct {
	WorldID  *int64 // nil = all; pass &id to scope to one world
	FolderID *int64
	Search   string
	OnlyPublic bool
	Limit    uint64
	Offset   uint64
}

// List returns levels matching opts, ordered by name.
func (s *Service) List(ctx context.Context, opts ListOpts) ([]Level, error) {
	q := `SELECT id, name, map_id, world_id, public, instancing_mode,
	             persistence_mode, refresh_window_seconds, reset_rules_json,
	             spectator_policy, hud_layout_json, folder_id, created_by,
	             created_at, updated_at
	      FROM levels WHERE 1=1`
	args := []any{}
	idx := 1
	if opts.WorldID != nil {
		q += fmt.Sprintf(" AND world_id = $%d", idx)
		args = append(args, *opts.WorldID)
		idx++
	}
	if opts.FolderID != nil {
		q += fmt.Sprintf(" AND folder_id = $%d", idx)
		args = append(args, *opts.FolderID)
		idx++
	}
	if opts.OnlyPublic {
		q += " AND public = true"
	}
	if opts.Search != "" {
		q += fmt.Sprintf(" AND name ILIKE $%d", idx)
		args = append(args, "%"+opts.Search+"%")
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
		return nil, fmt.Errorf("list levels: %w", err)
	}
	defer rows.Close()
	var out []Level
	for rows.Next() {
		var lv Level
		if err := rows.Scan(
			&lv.ID, &lv.Name, &lv.MapID, &lv.WorldID, &lv.Public,
			&lv.InstancingMode, &lv.PersistenceMode, &lv.RefreshWindowSeconds,
			&lv.ResetRulesJSON, &lv.SpectatorPolicy, &lv.HUDLayoutJSON,
			&lv.FolderID, &lv.CreatedBy, &lv.CreatedAt, &lv.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, lv)
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
		`UPDATE levels SET name = $2, updated_at = now() WHERE id = $1`,
		id, name,
	)
	if err != nil {
		if isUniqueViolation(err, "levels_name_key") {
			return fmt.Errorf("%w: %q", ErrNameInUse, name)
		}
		return fmt.Errorf("rename level: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLevelNotFound
	}
	return nil
}

// SetWorld attaches/detaches a level from a world. Pass nil to detach.
func (s *Service) SetWorld(ctx context.Context, levelID int64, worldID *int64) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE levels SET world_id = $2, updated_at = now() WHERE id = $1`,
		levelID, worldID,
	)
	if err != nil {
		return fmt.Errorf("set world: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLevelNotFound
	}
	return nil
}

// SetPublic flips the public flag on a level.
func (s *Service) SetPublic(ctx context.Context, levelID int64, public bool) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE levels SET public = $2, updated_at = now() WHERE id = $1`,
		levelID, public,
	)
	if err != nil {
		return fmt.Errorf("set public: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLevelNotFound
	}
	return nil
}

// SetHUDLayout writes the level's HUD layout JSON. Caller validates
// the JSON shape via internal/hud before passing it here.
func (s *Service) SetHUDLayout(ctx context.Context, levelID int64, layoutJSON []byte) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE levels SET hud_layout_json = $2::jsonb, updated_at = now() WHERE id = $1
	`, levelID, layoutJSON)
	if err != nil {
		return fmt.Errorf("set HUD layout: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLevelNotFound
	}
	return nil
}

// Delete removes a level. Cascades to level_entities, level_action_groups,
// level_flags, level_spectator_invites, level_state.
func (s *Service) Delete(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM levels WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete level: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLevelNotFound
	}
	return nil
}

// ---- level_entities (placements) ----

// PlaceEntityInput is one placement insertion.
type PlaceEntityInput struct {
	LevelID               int64
	EntityTypeID          int64
	X, Y                  int32
	RotationDegrees       int16
	InstanceOverridesJSON json.RawMessage
	Tags                  []string
}

// PlaceEntity inserts one level_entities row.
func (s *Service) PlaceEntity(ctx context.Context, in PlaceEntityInput) (*LevelEntity, error) {
	if in.RotationDegrees == 0 {
		// No-op; allowed.
	} else if in.RotationDegrees != 90 && in.RotationDegrees != 180 && in.RotationDegrees != 270 {
		return nil, fmt.Errorf("levels: invalid rotation %d", in.RotationDegrees)
	}
	if in.InstanceOverridesJSON == nil {
		in.InstanceOverridesJSON = []byte("{}")
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	var le LevelEntity
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO level_entities (
			level_id, entity_type_id, x, y, rotation_degrees,
			instance_overrides_json, tags
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, level_id, entity_type_id, x, y, rotation_degrees,
		          instance_overrides_json, tags, created_at
	`, in.LevelID, in.EntityTypeID, in.X, in.Y, in.RotationDegrees,
		in.InstanceOverridesJSON, in.Tags,
	).Scan(
		&le.ID, &le.LevelID, &le.EntityTypeID, &le.X, &le.Y, &le.RotationDegrees,
		&le.InstanceOverridesJSON, &le.Tags, &le.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("place entity: %w", err)
	}
	return &le, nil
}

// MoveEntity updates position + rotation of one placement.
func (s *Service) MoveEntity(ctx context.Context, id int64, x, y int32, rotation int16) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE level_entities SET x = $2, y = $3, rotation_degrees = $4 WHERE id = $1
	`, id, x, y, rotation)
	if err != nil {
		return fmt.Errorf("move entity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLevelEntityNotFound
	}
	return nil
}

// SetEntityOverrides replaces the instance_overrides_json blob on one
// placement. Useful for editing a door's transition target without
// re-creating the placement.
func (s *Service) SetEntityOverrides(ctx context.Context, id int64, overrides json.RawMessage) error {
	if overrides == nil {
		overrides = []byte("{}")
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE level_entities SET instance_overrides_json = $2::jsonb WHERE id = $1
	`, id, overrides)
	if err != nil {
		return fmt.Errorf("set overrides: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLevelEntityNotFound
	}
	return nil
}

// RemoveEntity deletes one level_entities row.
func (s *Service) RemoveEntity(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM level_entities WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("remove entity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLevelEntityNotFound
	}
	return nil
}

// ListEntities returns every placement on the level.
func (s *Service) ListEntities(ctx context.Context, levelID int64) ([]LevelEntity, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, level_id, entity_type_id, x, y, rotation_degrees,
		       instance_overrides_json, tags, created_at
		FROM level_entities WHERE level_id = $1
		ORDER BY id ASC
	`, levelID)
	if err != nil {
		return nil, fmt.Errorf("list entities: %w", err)
	}
	defer rows.Close()
	var out []LevelEntity
	for rows.Next() {
		var le LevelEntity
		if err := rows.Scan(
			&le.ID, &le.LevelID, &le.EntityTypeID, &le.X, &le.Y, &le.RotationDegrees,
			&le.InstanceOverridesJSON, &le.Tags, &le.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, le)
	}
	return out, rows.Err()
}

// EntitiesInRect returns placements whose (x, y) falls in
// [x0..x1, y0..y1] inclusive. Used by the runtime chunk loader to
// merge non-tile placements with map_tiles for the chunk.
func (s *Service) EntitiesInRect(ctx context.Context, levelID int64, x0, y0, x1, y1 int32) ([]LevelEntity, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, level_id, entity_type_id, x, y, rotation_degrees,
		       instance_overrides_json, tags, created_at
		FROM level_entities
		WHERE level_id = $1 AND x BETWEEN $2 AND $3 AND y BETWEEN $4 AND $5
	`, levelID, x0, x1, y0, y1)
	if err != nil {
		return nil, fmt.Errorf("entities in rect: %w", err)
	}
	defer rows.Close()
	var out []LevelEntity
	for rows.Next() {
		var le LevelEntity
		if err := rows.Scan(
			&le.ID, &le.LevelID, &le.EntityTypeID, &le.X, &le.Y, &le.RotationDegrees,
			&le.InstanceOverridesJSON, &le.Tags, &le.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, le)
	}
	return out, rows.Err()
}

// ---- spectator invites (level-scoped) ----

// IsPlayerSpectatorAllowed reports whether the given player may open a
// player-realm spectate connection against the level. Mirrors the old
// map-scoped check but keyed on level_id and using
// level_spectator_invites.
func (s *Service) IsPlayerSpectatorAllowed(ctx context.Context, levelID, playerID int64, policy string) (bool, error) {
	switch policy {
	case "public":
		return true, nil
	case "private":
		return false, nil
	case "invite":
		var ok bool
		err := s.Pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM level_spectator_invites
				WHERE level_id = $1 AND player_id = $2
			)
		`, levelID, playerID).Scan(&ok)
		if err != nil {
			return false, fmt.Errorf("check spectator invite: %w", err)
		}
		return ok, nil
	default:
		return false, fmt.Errorf("levels: unknown spectator_policy %q", policy)
	}
}

// GrantSpectatorInvite records that `playerID` may spectate `levelID`
// when the level's spectator_policy is "invite". Idempotent;
// re-granting is a no-op.
func (s *Service) GrantSpectatorInvite(ctx context.Context, levelID, playerID, grantedBy int64) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO level_spectator_invites (level_id, player_id, granted_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (level_id, player_id) DO NOTHING
	`, levelID, playerID, grantedBy)
	if err != nil {
		return fmt.Errorf("grant spectator: %w", err)
	}
	return nil
}

// RevokeSpectatorInvite removes a (level, player) pair.
func (s *Service) RevokeSpectatorInvite(ctx context.Context, levelID, playerID int64) error {
	_, err := s.Pool.Exec(ctx, `
		DELETE FROM level_spectator_invites WHERE level_id = $1 AND player_id = $2
	`, levelID, playerID)
	return err
}

// ListSpectatorInvites returns every player_id invited to spectate a
// given level.
func (s *Service) ListSpectatorInvites(ctx context.Context, levelID int64) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT player_id FROM level_spectator_invites WHERE level_id = $1 ORDER BY player_id
	`, levelID)
	if err != nil {
		return nil, fmt.Errorf("list spectator invites: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var pid int64
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		out = append(out, pid)
	}
	return out, rows.Err()
}

// ---- helpers ----

func validInstancing(s string) bool {
	switch s {
	case "shared", "per_user", "per_party":
		return true
	}
	return false
}

func validPersistence(s string) bool {
	switch s {
	case "persistent", "transient":
		return true
	}
	return false
}

func validSpectator(s string) bool {
	switch s {
	case "public", "private", "invite":
		return true
	}
	return false
}

func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		return false
	}
	return constraint == "" || pe.ConstraintName == constraint
}
