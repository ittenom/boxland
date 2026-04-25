// Package settings is the per-user preferences store. Used by both
// realms (designer + player) through one schema (PLAN.md §5g + §6h):
// font picker, audio defaults, spectator preferences, control
// rebindings.
//
// The wire format is a tagged JSON blob; the Go service is intentionally
// dumb about its content so adding a new preference doesn't require a
// migration -- only a new field in the TS Settings type and a Default()
// update for fallback values.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Realm scopes the settings row. Identical names to the WS realm enum
// so logs read consistently across packages.
type Realm string

const (
	RealmDesigner Realm = "designer"
	RealmPlayer   Realm = "player"
)

// ErrInvalidRealm is returned when a caller supplies an unknown realm.
var ErrInvalidRealm = errors.New("settings: invalid realm")

// Service is the CRUD surface. Stateless; safe to share across handlers.
type Service struct {
	Pool *pgxpool.Pool
}

// New constructs a Service.
func New(pool *pgxpool.Pool) *Service {
	return &Service{Pool: pool}
}

// Get returns the JSON blob for (realm, subjectID), or the default blob
// if no row exists yet. The default blob is `{}` -- the client merges
// with its baked-in Defaults before applying.
func (s *Service) Get(ctx context.Context, realm Realm, subjectID int64) (json.RawMessage, error) {
	if realm != RealmDesigner && realm != RealmPlayer {
		return nil, ErrInvalidRealm
	}
	var payload json.RawMessage
	err := s.Pool.QueryRow(ctx, `
		SELECT payload_json FROM user_settings
		 WHERE realm = $1 AND subject_id = $2
	`, string(realm), subjectID).Scan(&payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return json.RawMessage("{}"), nil
		}
		return nil, fmt.Errorf("get settings: %w", err)
	}
	return payload, nil
}

// Save UPSERTs the blob. Validates that the input is a JSON object so
// callers can't accidentally store a bare string or array (the client
// always sends an object).
func (s *Service) Save(ctx context.Context, realm Realm, subjectID int64, payload []byte) error {
	if realm != RealmDesigner && realm != RealmPlayer {
		return ErrInvalidRealm
	}
	if !isJSONObject(payload) {
		return errors.New("settings: payload must be a JSON object")
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO user_settings (realm, subject_id, payload_json, updated_at)
		VALUES ($1, $2, $3::jsonb, now())
		ON CONFLICT (realm, subject_id) DO UPDATE
		SET payload_json = EXCLUDED.payload_json,
		    updated_at   = now()
	`, string(realm), subjectID, payload)
	if err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

// isJSONObject reports whether `payload` is a syntactically valid JSON
// object (not array, not bare string/number/etc). Cheap streaming check
// so we don't double-allocate via Unmarshal.
func isJSONObject(payload []byte) bool {
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return false
	}
	_, ok := v.(map[string]any)
	return ok
}
