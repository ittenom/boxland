// Package flags is the per-realm switches+variables service.
//
// The genre's no-code event systems (RPG Maker / Bakin / Smile Game
// Builder) rest on three primitives: switches (booleans), variables
// (ints), and common events (callable trigger groups). This package
// owns the first two; common events live in the automations package
// (call_action_group + map_action_groups).
//
// Indie-RPG research §P1 #9. PLAN.md (Automations).
//
// # Tenant isolation
//
// Every API takes mapID as the first positional argument. There is
// deliberately no "list flags across maps" surface -- a sim that needs
// to read flags from another realm should fetch them by visiting that
// realm's instance, not by sneaking a cross-realm query. The
// underlying SQL never omits map_id from the WHERE clause.
//
// # Performance
//
// LoadAll(mapID) is the only N-row query; sim startup uses it to seed
// an in-memory map. Per-trigger Get is a hash lookup against that
// snapshot. Set/Add write through to the DB and invalidate the cached
// row in one shot. We do NOT N+1 (one query per Get/Set per tick) --
// that's the whole point of the cache.
package flags

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Kind discriminates the flag's value type. Mirrors the
// map_flags.kind CHECK constraint.
type Kind string

const (
	KindBool Kind = "bool"
	KindInt  Kind = "int"
)

// MaxKeyLen mirrors the map_flags.key length CHECK at column level.
// Validated client-side so the DB error never reaches the user.
const MaxKeyLen = 64

// Flag is one row from map_flags. Value is decoded into a typed Go
// value (bool / int32) at read time so callers don't see JSON.
type Flag struct {
	MapID     int64
	Key       string
	Kind      Kind
	Bool      bool
	Int       int32
	UpdatedAt time.Time
}

// Errors returned by the service. Stable for handler mapping.
var (
	ErrNotFound     = errors.New("flags: not found")
	ErrInvalidKey   = errors.New("flags: invalid key")
	ErrUnknownKind  = errors.New("flags: unknown kind")
	ErrKindMismatch = errors.New("flags: kind mismatch")
)

// Service holds the pgx pool. Construct one per process and share.
type Service struct {
	Pool *pgxpool.Pool
}

// New builds a Service. The cache is not constructed here -- the sim
// owns its own Cache and seeds it via LoadAll on map instance boot.
func New(pool *pgxpool.Pool) *Service {
	return &Service{Pool: pool}
}

// LoadAll returns every flag for one map, in lexical key order.
// Single query; no N+1.
func (s *Service) LoadAll(ctx context.Context, mapID int64) ([]Flag, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT map_id, key, kind, value_json, updated_at
		   FROM map_flags
		  WHERE map_id = $1
		  ORDER BY key ASC`,
		mapID,
	)
	if err != nil {
		return nil, fmt.Errorf("flags load: %w", err)
	}
	defer rows.Close()
	var out []Flag
	for rows.Next() {
		f, err := scanFlag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Get returns one flag by (map_id, key). ErrNotFound if absent.
func (s *Service) Get(ctx context.Context, mapID int64, key string) (Flag, error) {
	if err := validateKey(key); err != nil {
		return Flag{}, err
	}
	row := s.Pool.QueryRow(ctx,
		`SELECT map_id, key, kind, value_json, updated_at
		   FROM map_flags
		  WHERE map_id = $1 AND key = $2`,
		mapID, key,
	)
	f, err := scanFlag(row)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return Flag{}, ErrNotFound
		}
		return Flag{}, err
	}
	return f, nil
}

// SetBool upserts a bool flag.
func (s *Service) SetBool(ctx context.Context, mapID int64, key string, v bool) error {
	if err := validateKey(key); err != nil {
		return err
	}
	raw, _ := json.Marshal(v)
	return s.upsert(ctx, mapID, key, KindBool, raw)
}

// SetInt upserts an int flag.
func (s *Service) SetInt(ctx context.Context, mapID int64, key string, v int32) error {
	if err := validateKey(key); err != nil {
		return err
	}
	raw, _ := json.Marshal(v)
	return s.upsert(ctx, mapID, key, KindInt, raw)
}

// Add atomically increments an int flag by `delta` (negative ok).
// If the row doesn't exist it is created with value = delta. Bool
// flags return ErrKindMismatch.
//
// One round-trip; uses INSERT ... ON CONFLICT DO UPDATE so concurrent
// Add calls from sibling triggers don't lose increments.
func (s *Service) Add(ctx context.Context, mapID int64, key string, delta int32) (int32, error) {
	if err := validateKey(key); err != nil {
		return 0, err
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO map_flags (map_id, key, kind, value_json)
		VALUES ($1, $2, 'int', to_jsonb($3::int))
		ON CONFLICT (map_id, key) DO UPDATE
		   SET value_json = to_jsonb(((map_flags.value_json)::int) + EXCLUDED.value_json::int),
		       updated_at = now()
		 WHERE map_flags.kind = 'int'
		RETURNING value_json::int
	`, mapID, key, delta)
	var newVal int32
	if err := row.Scan(&newVal); err != nil {
		if strings.Contains(err.Error(), "no rows") {
			// Existing row is the wrong kind (the WHERE bailed out).
			return 0, ErrKindMismatch
		}
		return 0, fmt.Errorf("flags add: %w", err)
	}
	return newVal, nil
}

// Delete removes one flag.
func (s *Service) Delete(ctx context.Context, mapID int64, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx,
		`DELETE FROM map_flags WHERE map_id = $1 AND key = $2`,
		mapID, key,
	)
	if err != nil {
		return fmt.Errorf("flags delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// upsert writes the (kind, value) pair. If a row exists with a
// different kind, returns ErrKindMismatch (we never silently coerce
// a bool flag to int or vice versa -- silent coercion would let a
// publish-time rename break trigger reads).
func (s *Service) upsert(ctx context.Context, mapID int64, key string, kind Kind, raw json.RawMessage) error {
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO map_flags (map_id, key, kind, value_json)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (map_id, key) DO UPDATE
		   SET value_json = EXCLUDED.value_json,
		       updated_at = now()
		 WHERE map_flags.kind = EXCLUDED.kind
	`, mapID, key, string(kind), raw)
	if err != nil {
		return fmt.Errorf("flags upsert: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrKindMismatch
	}
	return nil
}

// scanFlag decodes one row into a typed Flag. Accepts either pgx.Row
// or pgx.Rows (both expose Scan).
func scanFlag(r interface {
	Scan(...any) error
}) (Flag, error) {
	var (
		f       Flag
		kindStr string
		raw     []byte
	)
	if err := r.Scan(&f.MapID, &f.Key, &kindStr, &raw, &f.UpdatedAt); err != nil {
		return Flag{}, err
	}
	f.Kind = Kind(kindStr)
	switch f.Kind {
	case KindBool:
		if err := json.Unmarshal(raw, &f.Bool); err != nil {
			return Flag{}, fmt.Errorf("decode bool flag %q: %w", f.Key, err)
		}
	case KindInt:
		if err := json.Unmarshal(raw, &f.Int); err != nil {
			return Flag{}, fmt.Errorf("decode int flag %q: %w", f.Key, err)
		}
	default:
		return Flag{}, fmt.Errorf("%w: %q", ErrUnknownKind, kindStr)
	}
	return f, nil
}

// validateKey enforces the same constraints as the column CHECK so
// callers get a typed error instead of a SQL violation.
func validateKey(key string) error {
	if len(key) == 0 || len(key) > MaxKeyLen {
		return fmt.Errorf("%w: length must be 1..%d", ErrInvalidKey, MaxKeyLen)
	}
	return nil
}
