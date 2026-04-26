package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MetaKeyLastStartedVersion is the boxland_meta.key under which we
// stash the version string of the last `boxland serve` to start
// successfully against this database. The updater reads it on the
// next boot to detect cross-version jumps and surface the relevant
// MIGRATION_NOTES.md sections.
const MetaKeyLastStartedVersion = "last_started_version"

// MetaKeyLastStartedAt complements MetaKeyLastStartedVersion with the
// timestamp of that successful start. Useful for "you haven't booted
// this DB in 3 months — consider a backup before updating" hints.
const MetaKeyLastStartedAt = "last_started_at"

// MetaGet returns the value for key, or "" with no error when the
// key has never been written. Callers should treat empty as "first
// time we're seeing this database" and act accordingly.
func MetaGet(ctx context.Context, pool *pgxpool.Pool, key string) (string, error) {
	if pool == nil {
		return "", errors.New("persistence: nil pool")
	}
	var v string
	err := pool.QueryRow(ctx, `SELECT value FROM boxland_meta WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("meta get %q: %w", key, err)
	}
	return v, nil
}

// MetaSet upserts key=value with updated_at = now(). Idempotent.
func MetaSet(ctx context.Context, pool *pgxpool.Pool, key, value string) error {
	if pool == nil {
		return errors.New("persistence: nil pool")
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO boxland_meta (key, value)
		 VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE
		   SET value = EXCLUDED.value,
		       updated_at = now()`,
		key, value)
	if err != nil {
		return fmt.Errorf("meta set %q: %w", key, err)
	}
	return nil
}

// MetaSetMany applies several key/value updates in a single
// transaction. Useful when we want "version + timestamp" to land or
// fail together, never half-applied.
func MetaSetMany(ctx context.Context, pool *pgxpool.Pool, kv map[string]string) error {
	if pool == nil {
		return errors.New("persistence: nil pool")
	}
	if len(kv) == 0 {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for k, v := range kv {
		if _, err := tx.Exec(ctx,
			`INSERT INTO boxland_meta (key, value)
			 VALUES ($1, $2)
			 ON CONFLICT (key) DO UPDATE
			   SET value = EXCLUDED.value,
			       updated_at = now()`, k, v); err != nil {
			return fmt.Errorf("meta set %q: %w", k, err)
		}
	}
	return tx.Commit(ctx)
}

// RecordStartup writes the version and current time as the "last
// successful boot" pair. Called from runServe once all dependencies
// have connected, so we never record a version that didn't actually
// finish booting.
func RecordStartup(ctx context.Context, pool *pgxpool.Pool, version string, now time.Time) error {
	return MetaSetMany(ctx, pool, map[string]string{
		MetaKeyLastStartedVersion: version,
		MetaKeyLastStartedAt:      now.UTC().Format(time.RFC3339),
	})
}

// LastStartedVersion returns (version, when, ok). ok=false when no
// startup has ever been recorded — equivalent to a fresh database.
func LastStartedVersion(ctx context.Context, pool *pgxpool.Pool) (string, time.Time, bool, error) {
	v, err := MetaGet(ctx, pool, MetaKeyLastStartedVersion)
	if err != nil {
		return "", time.Time{}, false, err
	}
	if v == "" {
		return "", time.Time{}, false, nil
	}
	tsStr, err := MetaGet(ctx, pool, MetaKeyLastStartedAt)
	if err != nil {
		return v, time.Time{}, true, err
	}
	if tsStr == "" {
		return v, time.Time{}, true, nil
	}
	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		// Don't fail the caller over a corrupt timestamp; the
		// version string is the actionable bit.
		return v, time.Time{}, true, nil
	}
	return v, ts, true, nil
}
