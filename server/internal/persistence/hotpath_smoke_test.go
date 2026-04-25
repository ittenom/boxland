package persistence_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/persistence/hotpath"
)

// TestHotpathSmokeOne is the sqlc tracer bullet: if this passes, the codegen
// pipeline (queries -> generated Go -> pgx) is working end-to-end. Skipped
// when Postgres isn't available.
func TestHotpathSmokeOne(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://boxland:boxland_dev@localhost:5433/boxland?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}

	q := hotpath.New(pool)
	got, err := q.SmokeOne(ctx)
	if err != nil {
		t.Fatalf("SmokeOne: %v", err)
	}
	if got != 1 {
		t.Errorf("SmokeOne: got %d, want 1", got)
	}
}
