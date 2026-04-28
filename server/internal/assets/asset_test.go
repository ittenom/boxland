package assets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/persistence/testdb"
)

// openTestPool returns an isolated, freshly-migrated DB. testdb.New
// wires its own t.Cleanup that drops the database when the test ends.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// resetDB pre-creates a designer so the FK constraint on assets.created_by
// passes. The pool is already empty because testdb.New(t) returns a fresh
// database for every test.
func resetDB(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
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
		Kind: assets.KindSpriteAnimated, Name: "wall", ContentAddressedPath: "p2",
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
		Kind: assets.KindSpriteAnimated, Name: "x", ContentAddressedPath: "shared-path",
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

	for i, kind := range []assets.Kind{assets.KindSprite, assets.KindSprite, assets.KindSpriteAnimated, assets.KindAudio} {
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

func TestRename_Happy(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	a, _ := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "old", ContentAddressedPath: "p",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	if err := svc.Rename(ctx, a.ID, "new"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	got, _ := svc.FindByID(ctx, a.ID)
	if got.Name != "new" {
		t.Errorf("got %q", got.Name)
	}
}

func TestRename_DuplicateRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	_, _ = svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "alpha", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	b, _ := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "beta", ContentAddressedPath: "p2",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	if err := svc.Rename(ctx, b.ID, "alpha"); !errors.Is(err, assets.ErrNameInUse) {
		t.Fatalf("want ErrNameInUse, got %v", err)
	}
}

func TestListByFolder_KindRootHonored(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	_, _ = svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "s1", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	_, _ = svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSpriteAnimated, Name: "t1", ContentAddressedPath: "p2",
		OriginalFormat: "png", CreatedBy: designerID,
	})

	got, err := svc.ListByFolder(ctx, nil, "sprite", "alpha")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Kind != assets.KindSprite {
		t.Errorf("got %+v", got)
	}
}
