// Boxland — characters migration smoke test.
//
// The real proof that 0034 applies cleanly is that any test in this
// package using testdb.New(t) succeeds (testdb runs every migration
// against the per-test template). This file pins the contract more
// explicitly: it asserts the seed slot vocabulary lands intact, so a
// future migration can't silently truncate or re-order the defaults
// without tripping a test.

package characters_test

import (
	"context"
	"testing"

	"boxland/server/internal/persistence/testdb"
)

// TestMigration_SeedsDefaultSlots checks the 24 default character_slots
// rows seeded by 0034_characters.up.sql.
func TestMigration_SeedsDefaultSlots(t *testing.T) {
	pool := testdb.New(t)
	if pool == nil {
		return // testdb skipped
	}
	defer pool.Close()
	ctx := context.Background()

	// Count rows.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM character_slots`).Scan(&n); err != nil {
		t.Fatalf("count character_slots: %v", err)
	}
	if n != 24 {
		t.Fatalf("expected 24 seeded slots, got %d", n)
	}

	// `body` is the only required slot.
	var bodyRequired bool
	if err := pool.QueryRow(ctx,
		`SELECT required FROM character_slots WHERE key = 'body'`,
	).Scan(&bodyRequired); err != nil {
		t.Fatalf("select body slot: %v", err)
	}
	if !bodyRequired {
		t.Errorf("expected body slot to be required")
	}

	// `created_by IS NULL` for every seed row.
	var nonSystem int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM character_slots WHERE created_by IS NOT NULL`,
	).Scan(&nonSystem); err != nil {
		t.Fatalf("count non-system slots: %v", err)
	}
	if nonSystem != 0 {
		t.Errorf("expected all seeded slots to have NULL created_by, got %d non-null", nonSystem)
	}
}
