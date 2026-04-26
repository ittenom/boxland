// Package testdb provides per-test isolated PostgreSQL databases backed
// by template-database cloning. Each call to New(t) returns a freshly
// migrated, empty database that is dropped automatically when the test
// ends — no shared state, no DELETE-FROM gymnastics, and safe across
// concurrent test packages (the canonical race that forced `go test
// -p 1` historically).
//
// Implementation: github.com/peterldowns/pgtestdb. It hashes the
// embedded migration set, runs golang-migrate against a `template_<sha>`
// database exactly once per (binary, migration content), then clones
// it via `CREATE DATABASE … TEMPLATE …` for every New(t) call. Cloning
// runs in ~10–20ms regardless of schema size, so the per-test cost is
// dominated by whatever the test itself does.
//
// Concurrency: pgtestdb uses Postgres advisory locks AND in-process
// Go locks to coordinate template creation, so every running test
// binary in `go test ./...` cooperates correctly without -p 1.
//
// On failure, pgtestdb leaves the test database alive and prints a
// connection string in the test log so you can `psql` into the
// post-mortem state.
package testdb

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	// Register the pgx stdlib driver under the name "pgx" so pgtestdb
	// can sql.Open() through it for template/db management.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/golangmigrator"

	"boxland/server/migrations"
)

// DefaultDSN is used when TEST_DATABASE_URL isn't set. Matches the
// docker-compose dev stack credentials.
const DefaultDSN = "postgres://boxland:boxland_dev@localhost:5433/boxland?sslmode=disable"

// migrator is the singleton golang-migrate adapter pgtestdb uses to
// fingerprint + materialize the template database. Built once at
// package init from the embedded migration FS so the hash is stable
// across goroutines and processes.
var migrator = golangmigrator.New(".", golangmigrator.WithFS(migrations.FS))

// New returns a freshly-migrated, isolated PostgreSQL pool plus
// cleanup wired through t.Cleanup. The returned pool is safe to use
// concurrently from within the test (standard pgxpool semantics).
//
// Skips the test (with t.Skip) if Postgres isn't reachable — matches
// the pre-existing convention so `go test ./...` on a developer
// machine without docker still does something sensible.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	cfg := loadConfig(t)
	if cfg == nil {
		return nil // t.Skip already called
	}
	tdbCfg := pgtestdb.Custom(t, *cfg, migrator)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, tdbCfg.URL())
	if err != nil {
		t.Fatalf("testdb: open isolated pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("testdb: ping isolated pool: %v", err)
	}
	// pgtestdb's own t.Cleanup drops the database; we register pool.Close
	// FIRST so the close runs BEFORE the drop (LIFO cleanup order). If
	// we close after the drop, pgxpool's idle-connection reaper logs a
	// noisy "context canceled" — harmless but confusing in test output.
	t.Cleanup(pool.Close)
	return pool
}

// loadConfig builds the pgtestdb.Config from TEST_DATABASE_URL (or the
// default dev DSN). Returns nil after t.Skip when Postgres is missing.
func loadConfig(t testing.TB) *pgtestdb.Config {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = DefaultDSN
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Skipf("testdb: bad TEST_DATABASE_URL: %v", err)
		return nil
	}
	pw, _ := u.User.Password()
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	dbname := strings.TrimPrefix(u.Path, "/")
	if dbname == "" {
		dbname = "postgres"
	}
	options := u.RawQuery
	if options == "" {
		// pgtestdb requires *some* options string; sslmode=disable is
		// our dev default and matches the docker-compose stack.
		options = "sslmode=disable"
	}

	cfg := &pgtestdb.Config{
		DriverName: "pgx",
		User:       u.User.Username(),
		Password:   pw,
		Host:       host,
		Port:       port,
		Database:   dbname,
		Options:    options,
		// Force-terminate any lingering connections at drop time. Some
		// of our integration tests (websocket gateway, sandbox runtime)
		// hold onto connections through goroutines that don't always
		// release before Cleanup fires; without this the drop would
		// fail with "database is being accessed by other users".
		ForceTerminateConnections: true,
	}

	// Probe reachability up front so we can t.Skip cleanly. pgtestdb
	// itself would call t.Fatal on failure, which surfaces as a noisy
	// red CI when a developer simply forgot `just up`.
	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	probe, err := pgxpool.New(probeCtx, dsn)
	if err != nil {
		t.Skipf("testdb: postgres unavailable: %v", err)
		return nil
	}
	if err := probe.Ping(probeCtx); err != nil {
		probe.Close()
		t.Skipf("testdb: postgres unavailable: %v", err)
		return nil
	}
	probe.Close()
	return cfg
}
