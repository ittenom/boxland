package repo_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/persistence/repo"
)

// Widget is a tiny test-only row type used to exercise Repo[T].
type Widget struct {
	ID        int64     `db:"id"        pk:"auto"`
	Name      string    `db:"name"`
	Score     int       `db:"score"`
	CreatedAt time.Time `db:"created_at" repo:"readonly"`
}

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://boxland:boxland_dev@localhost:5433/boxland?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Skipf("postgres unavailable, skipping integration test: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("postgres unavailable, skipping integration test: %v", err)
	}
	return pool
}

// setupTable creates a fresh _repo_test_widgets table; t.Cleanup drops it.
func setupTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`DROP TABLE IF EXISTS _repo_test_widgets`,
		`CREATE TABLE _repo_test_widgets (
			id         BIGSERIAL PRIMARY KEY,
			name       TEXT NOT NULL,
			score      INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("setup %s: %v", s, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS _repo_test_widgets`)
	})
}

func TestRepo_InsertGetUpdateDelete(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	setupTable(t, pool)
	ctx := context.Background()

	r := repo.New[Widget](pool, "_repo_test_widgets")

	w := &Widget{Name: "alpha", Score: 7}
	if err := r.Insert(ctx, w); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if w.ID == 0 {
		t.Fatal("Insert should set the auto-generated ID")
	}
	if w.CreatedAt.IsZero() {
		t.Error("Insert should populate readonly fields like created_at")
	}

	got, err := r.Get(ctx, w.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "alpha" || got.Score != 7 {
		t.Errorf("Get: got %+v", got)
	}

	got.Score = 42
	if err := r.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, _ := r.Get(ctx, w.ID)
	if got2.Score != 42 {
		t.Errorf("Update did not persist: %+v", got2)
	}

	if err := r.Delete(ctx, w.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = r.Get(ctx, w.ID)
	if err == nil {
		t.Fatal("Get after Delete should return an error")
	}
}

func TestRepo_List_PaginationAndFilter(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	setupTable(t, pool)
	ctx := context.Background()
	r := repo.New[Widget](pool, "_repo_test_widgets")

	for i := 1; i <= 5; i++ {
		if err := r.Insert(ctx, &Widget{Name: "w", Score: i * 10}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := r.List(ctx, repo.ListOpts{})
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 5 {
		t.Errorf("expected 5, got %d", len(all))
	}

	// filter: score > 20 → 30, 40, 50
	highs, err := r.List(ctx, repo.ListOpts{
		Where: squirrel.Gt{"score": 20},
		Order: "score ASC",
	})
	if err != nil {
		t.Fatalf("List(filter): %v", err)
	}
	if len(highs) != 3 {
		t.Fatalf("expected 3 high scores, got %d", len(highs))
	}
	if highs[0].Score != 30 || highs[2].Score != 50 {
		t.Errorf("ordering wrong: %+v", highs)
	}

	// pagination
	page, err := r.List(ctx, repo.ListOpts{Order: "id ASC", Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("List(page): %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("expected page size 2, got %d", len(page))
	}
}

func TestRepo_GetMissing(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	setupTable(t, pool)
	ctx := context.Background()
	r := repo.New[Widget](pool, "_repo_test_widgets")

	_, err := r.Get(ctx, 9999)
	if err == nil {
		t.Fatal("Get(missing) should fail")
	}
	if !repo.IsNotFound(err) {
		t.Errorf("expected not-found error, got %v", err)
	}
}
