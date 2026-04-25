package assets_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/persistence/testdb"
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

// resetDB wipes every project table via the shared testdb helper, then
// pre-creates a designer so the FK constraint on assets.created_by passes.
func resetDB(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	testdb.Reset(t, pool)
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "asset-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	return d.ID
}

func TestCreate_HappyPath(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)

	a, err := svc.Create(context.Background(), assets.CreateInput{
		Kind:                 assets.KindSprite,
		Name:                 "boss",
		ContentAddressedPath: "assets/aa/bb/test-bytes-1",
		OriginalFormat:       "png",
		Tags:                 []string{"enemy", "boss"},
		CreatedBy:            designerID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.ID == 0 {
		t.Errorf("ID should be assigned")
	}
	if a.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be populated by repo:readonly tag")
	}
}

func TestCreate_DuplicateNameRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)

	_, err := svc.Create(context.Background(), assets.CreateInput{
		Kind: assets.KindSprite, Name: "dup", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Create(context.Background(), assets.CreateInput{
		Kind: assets.KindSprite, Name: "dup", ContentAddressedPath: "p2",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	if !errors.Is(err, assets.ErrNameInUse) {
		t.Errorf("got %v, want ErrNameInUse", err)
	}
}

func TestCreate_SameNameDifferentKindAllowed(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	// "wall" is fine as both a sprite and a tile.
	if _, err := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "wall", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindTile, Name: "wall", ContentAddressedPath: "p2",
		OriginalFormat: "png", CreatedBy: designerID,
	}); err != nil {
		t.Errorf("same name across kinds should be allowed: %v", err)
	}
}

func TestFindByContentPath_KindFiltered(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	_, _ = svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "x", ContentAddressedPath: "shared-path",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	_, _ = svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindTile, Name: "x", ContentAddressedPath: "shared-path",
		OriginalFormat: "png", CreatedBy: designerID,
	})

	a, err := svc.FindByContentPath(ctx, assets.KindSprite, "shared-path")
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != assets.KindSprite {
		t.Errorf("got kind %q, want sprite", a.Kind)
	}

	if _, err := svc.FindByContentPath(ctx, assets.KindAudio, "shared-path"); !errors.Is(err, assets.ErrAssetNotFound) {
		t.Errorf("audio lookup should miss, got %v", err)
	}
}

func TestList_FiltersAndPagination(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	for i, kind := range []assets.Kind{assets.KindSprite, assets.KindSprite, assets.KindTile, assets.KindAudio} {
		_, err := svc.Create(ctx, assets.CreateInput{
			Kind:                 kind,
			Name:                 string([]rune{'a', rune('A' + i)}), // "aA", "aB", "aC", "aD"
			ContentAddressedPath: "p" + string(rune('a'+i)),
			OriginalFormat:       "png",
			Tags:                 []string{"t" + string(rune('a'+i))},
			CreatedBy:            designerID,
		})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	all, err := svc.List(ctx, assets.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Errorf("expected 4 rows, got %d", len(all))
	}

	sprites, err := svc.List(ctx, assets.ListOpts{Kind: assets.KindSprite})
	if err != nil {
		t.Fatal(err)
	}
	if len(sprites) != 2 {
		t.Errorf("expected 2 sprites, got %d", len(sprites))
	}

	page, err := svc.List(ctx, assets.ListOpts{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 {
		t.Errorf("expected page of 2, got %d", len(page))
	}
}

func TestDelete(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	a, _ := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "del", ContentAddressedPath: "p",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	if err := svc.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.FindByID(ctx, a.ID); !errors.Is(err, assets.ErrAssetNotFound) {
		t.Errorf("expected ErrAssetNotFound, got %v", err)
	}
}
