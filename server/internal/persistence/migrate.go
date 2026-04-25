package persistence

import (
	"errors"
	"fmt"
	"log/slog"

	"boxland/server/migrations"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // register pgx5 scheme
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// MigrateUp applies all pending migrations against the given DSN.
// Migrations are embedded into the binary at build time so the production
// image is self-contained.
//
// The DSN must be a Postgres URL (postgres://...). The migrate library
// requires the `pgx5://` URL scheme; we rewrite the user's standard
// `postgres://` DSN transparently so config.DatabaseURL works for both
// the runtime pgx pool and the migrator.
func MigrateUp(databaseURL string) error {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer closeMigrator(m)

	switch err := m.Up(); {
	case errors.Is(err, migrate.ErrNoChange):
		slog.Info("migrations: already at latest version")
		return nil
	case err != nil:
		return fmt.Errorf("migrate up: %w", err)
	}
	v, dirty, _ := m.Version()
	slog.Info("migrations: applied", "version", v, "dirty", dirty)
	return nil
}

// MigrateDown rolls back one migration step.
func MigrateDown(databaseURL string) error {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer closeMigrator(m)

	if err := m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate down: %w", err)
	}
	v, dirty, _ := m.Version()
	slog.Info("migrations: rolled back one step", "version", v, "dirty", dirty)
	return nil
}

// MigrateVersion returns the current applied migration version (or zero if
// the database is fresh) and whether a previous run left it dirty.
func MigrateVersion(databaseURL string) (uint, bool, error) {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return 0, false, err
	}
	defer closeMigrator(m)

	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, dirty, nil
}

func newMigrator(databaseURL string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	url := normalizeMigrateURL(databaseURL)
	m, err := migrate.NewWithSourceInstance("iofs", src, url)
	if err != nil {
		return nil, fmt.Errorf("init migrator: %w", err)
	}
	return m, nil
}

// normalizeMigrateURL rewrites postgres:// or postgresql:// to pgx5://
// because the migrate library uses driver schemes to pick its database
// driver, and we want pgx (matching our runtime pool) rather than the
// stdlib lib/pq driver.
func normalizeMigrateURL(databaseURL string) string {
	const (
		old1 = "postgres://"
		old2 = "postgresql://"
		newp = "pgx5://"
	)
	if has(databaseURL, old1) {
		return newp + databaseURL[len(old1):]
	}
	if has(databaseURL, old2) {
		return newp + databaseURL[len(old2):]
	}
	return databaseURL
}

func has(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func closeMigrator(m *migrate.Migrate) {
	if m == nil {
		return
	}
	srcErr, dbErr := m.Close()
	if srcErr != nil {
		slog.Warn("migrate close source", "err", srcErr)
	}
	if dbErr != nil {
		slog.Warn("migrate close db", "err", dbErr)
	}
}
