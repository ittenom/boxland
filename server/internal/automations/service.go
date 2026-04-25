package automations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service is the persistence facade for entity_automations rows.
// Stateless; safe to share.
type Service struct {
	Pool      *pgxpool.Pool
	Triggers  *Registry
	Actions   *Registry
}

// New constructs a Service. Pass DefaultTriggers() / DefaultActions()
// in production; tests inject fresh registries.
func New(pool *pgxpool.Pool, triggers, actions *Registry) *Service {
	return &Service{Pool: pool, Triggers: triggers, Actions: actions}
}

// Get returns the AutomationSet for an entity type, or an empty set if
// no row exists yet.
func (s *Service) Get(ctx context.Context, entityTypeID int64) (AutomationSet, error) {
	var raw json.RawMessage
	err := s.Pool.QueryRow(ctx,
		`SELECT automation_ast_json FROM entity_automations WHERE entity_type_id = $1`,
		entityTypeID,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AutomationSet{}, nil
		}
		return AutomationSet{}, fmt.Errorf("get automations: %w", err)
	}
	return DecodeSet(raw)
}

// Save validates + UPSERTs the AutomationSet for an entity type. Empty
// input is valid (deletes all automations for the type).
func (s *Service) Save(ctx context.Context, entityTypeID int64, set AutomationSet) error {
	if err := set.Validate(s.Triggers, s.Actions); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	body, err := json.Marshal(set)
	if err != nil {
		return fmt.Errorf("marshal automations: %w", err)
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO entity_automations (entity_type_id, automation_ast_json, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (entity_type_id) DO UPDATE
		SET automation_ast_json = EXCLUDED.automation_ast_json,
		    updated_at          = now()
	`, entityTypeID, body)
	if err != nil {
		return fmt.Errorf("save automations: %w", err)
	}
	return nil
}

// Delete removes the row entirely (idempotent).
func (s *Service) Delete(ctx context.Context, entityTypeID int64) error {
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM entity_automations WHERE entity_type_id = $1`, entityTypeID)
	if err != nil {
		return fmt.Errorf("delete automations: %w", err)
	}
	return nil
}
