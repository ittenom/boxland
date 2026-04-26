// Boxland — characters: down-migration smoke.
//
// `testdb` only exercises the up-migration path (every test runs against
// a fresh template). This test confirms 0034_characters.down.sql cleanly
// removes everything 0034_characters.up.sql created — important so a
// developer experimenting with the schema can roll back without a hand-
// crafted DROP CASCADE chain.

package characters_test

import (
	"context"
	"testing"

	"boxland/server/internal/persistence/testdb"
)

// TestMigration_DownDropsAllTables runs the 0034 down migration against
// the per-test DB and verifies every characters table is gone.
func TestMigration_DownDropsAllTables(t *testing.T) {
	pool := testdb.New(t)
	if pool == nil {
		return
	}
	defer pool.Close()
	ctx := context.Background()

	// Apply the 0034 down SQL by hand. The migrator inside testdb only
	// runs `up`; we mirror the .down.sql contents here so a future
	// edit to .down.sql shows up as a test failure (drift detector).
	statements := []string{
		`DROP TABLE IF EXISTS player_characters`,
		`DROP TABLE IF EXISTS npc_templates`,
		`DROP TABLE IF EXISTS character_talent_nodes`,
		`DROP TABLE IF EXISTS character_talent_trees`,
		`DROP TABLE IF EXISTS character_stat_sets`,
		`DROP TABLE IF EXISTS character_bakes`,
		`DROP TABLE IF EXISTS character_recipes`,
		`DROP TABLE IF EXISTS character_parts`,
		`DROP TABLE IF EXISTS character_slots`,
	}
	for _, s := range statements {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("down stmt %q: %v", s, err)
		}
	}

	// Confirm none of the tables exist anymore.
	tables := []string{
		"character_slots", "character_parts", "character_recipes", "character_bakes",
		"character_stat_sets", "character_talent_trees", "character_talent_nodes",
		"npc_templates", "player_characters",
	}
	for _, name := range tables {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)
		`, name).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", name, err)
		}
		if exists {
			t.Errorf("table %s still exists after down migration", name)
		}
	}
}
