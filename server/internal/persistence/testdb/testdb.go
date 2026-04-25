// Package testdb is a tiny test helper that wipes every project-managed
// table in dependency order so integration tests start from a clean slate.
//
// Add new tables to TruncateOrder when their migrations land. The package
// is internal/persistence/* not internal/* so it stays scoped to test
// code without polluting the production import graph.
package testdb

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TruncateOrder lists every project-owned table in FK-cascade-safe order
// (children first, then parents). Maintained centrally so adding a new
// migration is one edit, not a spread across every _test.go file.
//
// New tables go HERE first; tests inherit the change automatically.
var TruncateOrder = []string{
	// Procedural-map / authored-map dependents land here when their
	// migrations exist. Keep the order: children -> parents.
	"map_state",
	"tile_edge_assignments",
	"tile_groups",
	"edge_socket_types",
	"entity_components",
	"entity_types",
	"asset_variants",
	"palette_variants",
	"asset_animations",
	"assets",
	"designer_ws_tickets",
	"designer_sessions",
	"publish_diffs",
	"drafts",
	"designers",
	"player_email_verifications",
	"player_sessions",
	"player_oauth_links",
	"players",
}

// Reset deletes every row from every table in TruncateOrder. Errors per
// table are logged via t.Logf so a benign "table doesn't exist yet"
// (during a partial migration test) doesn't fail the whole reset.
//
// Safe to call BOTH at the start of a test and inside t.Cleanup; the
// before-test wipe makes sure leaks from a prior test (which crashed
// before its t.Cleanup ran, etc.) don't poison the new run.
func Reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	wipe := func() {
		ctx := context.Background()
		for _, table := range TruncateOrder {
			if _, err := pool.Exec(ctx, "DELETE FROM "+table); err != nil {
				t.Logf("testdb.Reset DELETE FROM %s: %v", table, err)
			}
		}
	}
	wipe()
	t.Cleanup(wipe)
}
