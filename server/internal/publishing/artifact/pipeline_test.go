package artifact_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/configurable"
	"boxland/server/internal/publishing/artifact"
)

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("postgres unavailable: %v", err)
	}
	return pool
}

func resetTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, q := range []string{
		`DELETE FROM publish_diffs`,
		`DELETE FROM drafts`,
	} {
		if _, err := pool.Exec(context.Background(), q); err != nil {
			t.Fatalf("reset %s: %v", q, err)
		}
	}
}

// recordingHandler captures every Publish call.
type recordingHandler struct {
	kind  artifact.Kind
	calls *[]artifact.DraftRow
}

func (h recordingHandler) Kind() artifact.Kind { return h.kind }
func (h recordingHandler) Validate(context.Context, artifact.DraftRow) error { return nil }
func (h recordingHandler) Publish(_ context.Context, _ pgx.Tx, d artifact.DraftRow) (artifact.PublishResult, error) {
	*h.calls = append(*h.calls, d)
	return artifact.PublishResult{
		Op:   artifact.OpUpdated,
		Diff: configurable.StructuredDiff{SummaryLine: "test ok"},
	}, nil
}

func TestPipeline_RunHappyPath(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetTables(t, pool)

	ctx := context.Background()

	// Seed two drafts of two different kinds.
	for _, d := range []struct {
		kind artifact.Kind
		id   int64
	}{
		{artifact.KindAsset, 100},
		{artifact.KindMap, 200},
	} {
		_, err := pool.Exec(ctx,
			`INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by) VALUES ($1, $2, $3, $4)`,
			string(d.kind), d.id, json.RawMessage(`{"hello":"world"}`), int64(7),
		)
		if err != nil {
			t.Fatalf("seed draft: %v", err)
		}
	}

	var calls []artifact.DraftRow
	reg := artifact.NewRegistry()
	reg.Register(recordingHandler{kind: artifact.KindAsset, calls: &calls})
	reg.Register(recordingHandler{kind: artifact.KindMap, calls: &calls})

	pipe := artifact.NewPipeline(pool, reg)
	outcomes, err := pipe.Run(ctx, 7)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %d", len(outcomes))
	}
	if len(calls) != 2 {
		t.Errorf("expected 2 handler calls, got %d", len(calls))
	}

	// All drafts were deleted.
	var draftCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM drafts`).Scan(&draftCount); err != nil {
		t.Fatalf("count drafts: %v", err)
	}
	if draftCount != 0 {
		t.Errorf("expected drafts to be deleted, got %d remaining", draftCount)
	}

	// publish_diffs got two rows for the same changeset.
	var diffCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM publish_diffs WHERE changeset_id = $1`,
		outcomes[0].ChangesetID,
	).Scan(&diffCount); err != nil {
		t.Fatalf("count diffs: %v", err)
	}
	if diffCount != 2 {
		t.Errorf("expected 2 publish_diffs rows, got %d", diffCount)
	}

	// Both outcomes share the same changeset id.
	if outcomes[0].ChangesetID != outcomes[1].ChangesetID {
		t.Errorf("changeset ids should match: %d vs %d",
			outcomes[0].ChangesetID, outcomes[1].ChangesetID)
	}
}

func TestPipeline_UnknownKindAborts(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetTables(t, pool)

	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by) VALUES ($1, $2, $3, $4)`,
		"unknown_kind", int64(1), json.RawMessage(`{}`), int64(7),
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	reg := artifact.NewRegistry() // no handlers
	pipe := artifact.NewPipeline(pool, reg)
	if _, err := pipe.Run(ctx, 7); err == nil {
		t.Fatal("expected error for unknown kind")
	}

	// Draft remains because the publish never started.
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM drafts`).Scan(&n)
	if n != 1 {
		t.Errorf("draft should be untouched, got %d remaining", n)
	}
}

func TestPipeline_NoDraftsIsNoOp(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetTables(t, pool)

	pipe := artifact.NewPipeline(pool, artifact.NewRegistry())
	outcomes, err := pipe.Run(context.Background(), 7)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(outcomes) != 0 {
		t.Errorf("expected no outcomes, got %d", len(outcomes))
	}
}
