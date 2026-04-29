package setup_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/persistence"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/setup"
)

// makeStore wires a real S3-style ObjectStore against the dev MinIO.
// Mirrors the helper in assets/upload_test.go: skips the test when
// MinIO isn't reachable so CI / local devs without docker still pass
// the rest of the suite.
func makeStore(t *testing.T) *persistence.ObjectStore {
	t.Helper()
	cfg := persistence.ObjectStoreConfig{
		Endpoint:        "http://localhost:9000",
		Region:          "us-east-1",
		Bucket:          "boxland-assets",
		AccessKeyID:     "boxland",
		SecretAccessKey: "boxland_dev_secret",
		UsePathStyle:    true,
		PublicBaseURL:   "http://localhost:9000/boxland-assets",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := persistence.NewObjectStore(ctx, cfg)
	if err != nil {
		t.Skipf("minio unavailable: %v", err)
	}
	return store
}

func openPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// fixture returns the deps the seeder wants, plus a designer id we
// attribute the seeded rows to.
func fixture(t *testing.T) (setup.UIKitDeps, int64) {
	t.Helper()
	pool := openPool(t)
	t.Cleanup(pool.Close)

	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "uipack-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	return setup.UIKitDeps{
		Assets:   assets.New(pool),
		Entities: entities.New(pool, components.Default()),
		Store:    makeStore(t),
	}, d.ID
}

func TestImportUIKitGradient_PopulatesAssetsAndEntities(t *testing.T) {
	deps, designerID := fixture(t)
	res, err := setup.ImportUIKitGradient(context.Background(), deps, designerID)
	if err != nil {
		t.Fatalf("ImportUIKitGradient: %v", err)
	}
	if res.Total == 0 {
		t.Fatal("expected at least one sprite in the embedded UI pack; got zero")
	}
	if res.Failed > 0 {
		t.Errorf("expected 0 failures, got %d", res.Failed)
	}
	if res.Created == 0 {
		t.Errorf("expected at least one freshly-created row")
	}

	// Confirm the assets show up under KindUIPanel.
	uiAssets, err := deps.Assets.List(context.Background(), assets.ListOpts{
		Kind:  assets.KindUIPanel,
		Limit: 1024,
	})
	if err != nil {
		t.Fatalf("list ui_panel assets: %v", err)
	}
	if len(uiAssets) != res.Created {
		t.Errorf("expected %d ui_panel assets to match Created count, got %d", res.Created, len(uiAssets))
	}

	// Every asset row must be tagged so future seeders/migrations
	// can identify the pack origin without filename heuristics.
	for _, a := range uiAssets {
		if !containsTag(a.Tags, "ui-pack") || !containsTag(a.Tags, "crusenho-gradient") {
			t.Errorf("asset %q missing pack tags: %v", a.Name, a.Tags)
		}
	}

	// And the corresponding ClassUI entity_types exist with a
	// nine_slice component populated.
	uiEntities, err := deps.Entities.ListByClass(context.Background(), entities.ClassUI, entities.ListOpts{Limit: 1024})
	if err != nil {
		t.Fatalf("list ui entities: %v", err)
	}
	if len(uiEntities) != res.Created {
		t.Errorf("expected %d ClassUI entity_types to match Created, got %d", res.Created, len(uiEntities))
	}
	for _, et := range uiEntities {
		rows, err := deps.Entities.Components(context.Background(), et.ID)
		if err != nil {
			t.Fatalf("load components for %q: %v", et.Name, err)
		}
		var found bool
		for _, r := range rows {
			if r.Kind != components.KindNineSlice {
				continue
			}
			found = true
			// Decode + validate to catch any seeder bug that
			// stuffs a degenerate config into the table.
			reg := components.Default()
			def, _ := reg.Get(components.KindNineSlice)
			if err := def.Validate(r.ConfigJSON); err != nil {
				t.Errorf("entity %q has invalid nine_slice: %v", et.Name, err)
			}
			break
		}
		if !found {
			t.Errorf("entity %q missing nine_slice component", et.Name)
		}
	}
}

func TestImportUIKitGradient_IsIdempotent(t *testing.T) {
	deps, designerID := fixture(t)
	first, err := setup.ImportUIKitGradient(context.Background(), deps, designerID)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Failed > 0 {
		t.Fatalf("first run had failures: %+v", first)
	}
	second, err := setup.ImportUIKitGradient(context.Background(), deps, designerID)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Created != 0 {
		t.Errorf("second run created %d new rows; expected 0", second.Created)
	}
	if second.Skipped != first.Total {
		t.Errorf("second run skipped %d, expected %d (every sprite already imported)", second.Skipped, first.Total)
	}
	if second.Failed > 0 {
		t.Errorf("second run had failures: %+v", second)
	}
}

func TestImportUIKitGradient_RecreatesEntityIfDeleted(t *testing.T) {
	deps, designerID := fixture(t)
	first, err := setup.ImportUIKitGradient(context.Background(), deps, designerID)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Created == 0 {
		t.Skip("nothing to delete; pack appears empty")
	}

	// Delete one ClassUI entity_type so the seeder has a row to
	// repair on the second run.
	uiEntities, err := deps.Entities.ListByClass(context.Background(), entities.ClassUI, entities.ListOpts{Limit: 1})
	if err != nil || len(uiEntities) == 0 {
		t.Fatalf("expected at least one ClassUI entity, got err=%v len=%d", err, len(uiEntities))
	}
	target := uiEntities[0]
	if err := deps.Entities.Delete(context.Background(), target.ID); err != nil {
		t.Fatalf("delete %q: %v", target.Name, err)
	}

	// Re-run the seeder; the orphaned asset should regrow its
	// entity_type. Skipped accounts for the asset (still present);
	// Created should be at least 1 for the recreated entity. Note:
	// our Result struct only counts the high-level "Created" branch
	// when both rows are fresh. The repair branch lives under
	// Skipped — what matters is the entity comes back.
	if _, err := setup.ImportUIKitGradient(context.Background(), deps, designerID); err != nil {
		t.Fatalf("repair run: %v", err)
	}
	if _, err := deps.Entities.FindBySpriteAtlas(context.Background(), *target.SpriteAssetID); err != nil {
		// Allow either ErrEntityTypeNotFound (regression) or a
		// found row (repaired). Anything else is unexpected.
		if !errors.Is(err, entities.ErrEntityTypeNotFound) {
			t.Errorf("unexpected error: %v", err)
		} else {
			t.Errorf("entity_type was not recreated for asset %d", *target.SpriteAssetID)
		}
	}
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}
