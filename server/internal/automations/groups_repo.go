package automations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GroupsRepo owns CRUD on map_action_groups. Tenant isolation: every
// API takes mapID as the first argument and the underlying SQL never
// omits map_id from WHERE.
//
// Indie-RPG research §P1 #10. Migration 0030.
type GroupsRepo struct {
	Pool *pgxpool.Pool
}

func NewGroupsRepo(pool *pgxpool.Pool) *GroupsRepo {
	return &GroupsRepo{Pool: pool}
}

// GroupRow is one persisted action group, with timestamps. Use Decode
// to convert into the compile-friendly ActionGroup.
type GroupRow struct {
	ID          int64
	MapID       int64
	Name        string
	ActionsJSON json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Decode parses ActionsJSON into typed ActionNodes.
func (r GroupRow) Decode() (ActionGroup, error) {
	var nodes []ActionNode
	if len(r.ActionsJSON) > 0 && string(r.ActionsJSON) != "null" {
		if err := json.Unmarshal(r.ActionsJSON, &nodes); err != nil {
			return ActionGroup{}, fmt.Errorf("decode actions for group %q: %w", r.Name, err)
		}
	}
	return ActionGroup{
		ID:      r.ID,
		MapID:   r.MapID,
		Name:    r.Name,
		Actions: nodes,
	}, nil
}

// Errors returned by the groups repo.
var (
	ErrGroupNotFound  = errors.New("action_groups: not found")
	ErrGroupNameInUse = errors.New("action_groups: name already exists in this map")
)

// ListByMap returns every action group on a map, ordered by name.
func (r *GroupsRepo) ListByMap(ctx context.Context, mapID int64) ([]GroupRow, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT id, map_id, name, actions_json, created_at, updated_at
		  FROM map_action_groups
		 WHERE map_id = $1
		 ORDER BY name ASC`, mapID)
	if err != nil {
		return nil, fmt.Errorf("action groups list: %w", err)
	}
	defer rows.Close()
	var out []GroupRow
	for rows.Next() {
		var g GroupRow
		if err := rows.Scan(&g.ID, &g.MapID, &g.Name, &g.ActionsJSON, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// LoadCompiled is the convenience hot-path for the sim: list + decode +
// compile + return the name index in one call. One DB query.
func (r *GroupsRepo) LoadCompiled(ctx context.Context, mapID int64, actions *Registry) (CompiledActionGroups, error) {
	rows, err := r.ListByMap(ctx, mapID)
	if err != nil {
		return nil, err
	}
	groups := make([]ActionGroup, 0, len(rows))
	for _, row := range rows {
		g, derr := row.Decode()
		if derr != nil {
			return nil, derr
		}
		groups = append(groups, g)
	}
	return CompileActionGroups(groups, actions)
}

// Upsert inserts a new group or updates an existing one in-place by
// (map_id, name). Returns ErrGroupNameInUse only when the underlying
// constraint is hit by a concurrent insert (vanishingly rare).
func (r *GroupsRepo) Upsert(ctx context.Context, mapID int64, name string, actionsJSON json.RawMessage) (GroupRow, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return GroupRow{}, errors.New("action_groups: name must be 1..64 chars")
	}
	if len(actionsJSON) == 0 {
		actionsJSON = []byte("[]")
	}
	var g GroupRow
	row := r.Pool.QueryRow(ctx, `
		INSERT INTO map_action_groups (map_id, name, actions_json)
		VALUES ($1, $2, $3::jsonb)
		ON CONFLICT (map_id, name) DO UPDATE
		   SET actions_json = EXCLUDED.actions_json,
		       updated_at   = now()
		RETURNING id, map_id, name, actions_json, created_at, updated_at`,
		mapID, name, actionsJSON,
	)
	if err := row.Scan(&g.ID, &g.MapID, &g.Name, &g.ActionsJSON, &g.CreatedAt, &g.UpdatedAt); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return GroupRow{}, fmt.Errorf("%w: %q", ErrGroupNameInUse, name)
		}
		return GroupRow{}, fmt.Errorf("action_groups upsert: %w", err)
	}
	return g, nil
}

// Delete removes one group by name.
func (r *GroupsRepo) Delete(ctx context.Context, mapID int64, name string) error {
	tag, err := r.Pool.Exec(ctx,
		`DELETE FROM map_action_groups WHERE map_id = $1 AND name = $2`,
		mapID, name,
	)
	if err != nil {
		return fmt.Errorf("action_groups delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrGroupNotFound
	}
	return nil
}
