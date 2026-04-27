package folders_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/folders"
	"boxland/server/internal/persistence/testdb"
)

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// fixture spins up a designer + service. The pool is already empty
// because testdb.New(t) gives every test a fresh database.
func fixture(t *testing.T, pool *pgxpool.Pool) (*folders.Service, int64) {
	t.Helper()
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "folders-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	return folders.New(pool), d.ID
}

func TestCreate_HappyPath(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)

	f, err := svc.Create(context.Background(), folders.CreateInput{
		Name:      "Forest",
		KindRoot:  folders.KindTile,
		CreatedBy: dID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if f.KindRoot != folders.KindTile || f.Name != "Forest" || f.SortMode != folders.SortAlpha {
		t.Errorf("got %+v", f)
	}
	if f.ParentID != nil {
		t.Errorf("expected nil parent, got %v", *f.ParentID)
	}
}

func TestCreate_DuplicateNameRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	_, err := svc.Create(ctx, folders.CreateInput{Name: "Forest", KindRoot: folders.KindTile, CreatedBy: dID})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = svc.Create(ctx, folders.CreateInput{Name: "Forest", KindRoot: folders.KindTile, CreatedBy: dID})
	if !errors.Is(err, folders.ErrNameInUse) {
		t.Fatalf("want ErrNameInUse, got %v", err)
	}
}

func TestCreate_SameNameDifferentKindRoot(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	if _, err := svc.Create(ctx, folders.CreateInput{Name: "Town", KindRoot: folders.KindTile, CreatedBy: dID}); err != nil {
		t.Fatalf("create tile: %v", err)
	}
	if _, err := svc.Create(ctx, folders.CreateInput{Name: "Town", KindRoot: folders.KindSprite, CreatedBy: dID}); err != nil {
		t.Fatalf("create sprite: %v", err)
	}
}

func TestCreate_InvalidKindRoot(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)

	_, err := svc.Create(context.Background(), folders.CreateInput{
		Name: "Whatever", KindRoot: folders.KindRoot("bogus"), CreatedBy: dID,
	})
	if !errors.Is(err, folders.ErrInvalidKindRoot) {
		t.Fatalf("want ErrInvalidKindRoot, got %v", err)
	}
}

func TestCreate_ParentMustShareKindRoot(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	tileFolder, _ := svc.Create(ctx, folders.CreateInput{Name: "Tiles", KindRoot: folders.KindTile, CreatedBy: dID})

	_, err := svc.Create(ctx, folders.CreateInput{
		Name:      "Sprites",
		KindRoot:  folders.KindSprite,
		ParentID:  &tileFolder.ID,
		CreatedBy: dID,
	})
	if !errors.Is(err, folders.ErrCrossKindMove) {
		t.Fatalf("want ErrCrossKindMove, got %v", err)
	}
}

func TestRename_Happy(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	f, _ := svc.Create(ctx, folders.CreateInput{Name: "Forest", KindRoot: folders.KindTile, CreatedBy: dID})
	if err := svc.Rename(ctx, f.ID, "Woodland"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, _ := svc.FindByID(ctx, f.ID)
	if got.Name != "Woodland" {
		t.Errorf("want Woodland, got %q", got.Name)
	}
}

func TestRename_DuplicateRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	a, _ := svc.Create(ctx, folders.CreateInput{Name: "Forest", KindRoot: folders.KindTile, CreatedBy: dID})
	_, _ = svc.Create(ctx, folders.CreateInput{Name: "Town", KindRoot: folders.KindTile, CreatedBy: dID})

	if err := svc.Rename(ctx, a.ID, "Town"); !errors.Is(err, folders.ErrNameInUse) {
		t.Fatalf("want ErrNameInUse, got %v", err)
	}
}

func TestMove_PreventsCycle(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	root, _ := svc.Create(ctx, folders.CreateInput{Name: "Root", KindRoot: folders.KindTile, CreatedBy: dID})
	mid, _ := svc.Create(ctx, folders.CreateInput{Name: "Mid", KindRoot: folders.KindTile, ParentID: &root.ID, CreatedBy: dID})
	leaf, _ := svc.Create(ctx, folders.CreateInput{Name: "Leaf", KindRoot: folders.KindTile, ParentID: &mid.ID, CreatedBy: dID})

	// Try moving Root under Leaf — would create a cycle.
	if err := svc.Move(ctx, root.ID, &leaf.ID); !errors.Is(err, folders.ErrCycle) {
		t.Fatalf("want ErrCycle, got %v", err)
	}
	// Self-move is also a cycle.
	if err := svc.Move(ctx, mid.ID, &mid.ID); !errors.Is(err, folders.ErrCycle) {
		t.Fatalf("want ErrCycle, got %v", err)
	}
}

func TestMove_AcrossKindRootRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	tileF, _ := svc.Create(ctx, folders.CreateInput{Name: "Forest", KindRoot: folders.KindTile, CreatedBy: dID})
	spriteF, _ := svc.Create(ctx, folders.CreateInput{Name: "NPCs", KindRoot: folders.KindSprite, CreatedBy: dID})

	if err := svc.Move(ctx, tileF.ID, &spriteF.ID); !errors.Is(err, folders.ErrCrossKindMove) {
		t.Fatalf("want ErrCrossKindMove, got %v", err)
	}
}

func TestDelete_CascadesChildrenSparesAssets(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	parent, _ := svc.Create(ctx, folders.CreateInput{Name: "Forest", KindRoot: folders.KindTile, CreatedBy: dID})
	child, _ := svc.Create(ctx, folders.CreateInput{Name: "Trees", KindRoot: folders.KindTile, ParentID: &parent.ID, CreatedBy: dID})

	// Add an asset into child and verify it bubbles back to root after delete.
	asvc := assets.New(pool)
	a, err := asvc.Create(ctx, assets.CreateInput{
		Kind:                 assets.KindTile,
		Name:                 "tree-a",
		ContentAddressedPath: "test/tree-a",
		OriginalFormat:       "png",
		FolderID:             &child.ID,
		CreatedBy:            dID,
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}

	if err := svc.Delete(ctx, parent.ID); err != nil {
		t.Fatalf("delete parent: %v", err)
	}

	// Asset should still exist, with folder_id NULL'd via SET NULL.
	got, err := asvc.FindByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("asset after parent delete: %v", err)
	}
	if got.FolderID != nil {
		t.Errorf("expected folder_id NULL after delete, got %v", *got.FolderID)
	}
	// Child folder should be gone.
	if _, err := svc.FindByID(ctx, child.ID); !errors.Is(err, folders.ErrNotFound) {
		t.Errorf("expected child gone, got %v", err)
	}
}

func TestEnsurePath_CreatesAndIsIdempotent(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	id1, err := svc.EnsurePath(ctx, folders.KindTile, "forest/trees/oaks", dID)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if id1 == 0 {
		t.Fatal("expected non-zero leaf id")
	}
	// Run again: should hit the existing rows, not create dupes.
	id2, err := svc.EnsurePath(ctx, folders.KindTile, "forest/trees/oaks", dID)
	if err != nil {
		t.Fatalf("ensure idempotent: %v", err)
	}
	if id1 != id2 {
		t.Errorf("idempotency broken: %d vs %d", id1, id2)
	}

	// Path round-trip.
	p, err := svc.Path(ctx, id1)
	if err != nil {
		t.Fatal(err)
	}
	if p != "forest/trees/oaks" {
		t.Errorf("path = %q", p)
	}
}

func TestEnsurePath_CaseInsensitiveMatch(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	id1, _ := svc.EnsurePath(ctx, folders.KindTile, "Forest", dID)
	id2, _ := svc.EnsurePath(ctx, folders.KindTile, "forest", dID)
	if id1 != id2 {
		t.Errorf("case-insensitive match failed: %d vs %d", id1, id2)
	}
}

func TestEnsurePath_EmptyReturnsZero(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)

	id, err := svc.EnsurePath(context.Background(), folders.KindTile, "", dID)
	if err != nil {
		t.Fatalf("ensure empty: %v", err)
	}
	if id != 0 {
		t.Errorf("expected 0 for empty path, got %d", id)
	}
}

func TestPathsByID_BulkResolve(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	a, _ := svc.EnsurePath(ctx, folders.KindTile, "forest/trees", dID)
	b, _ := svc.EnsurePath(ctx, folders.KindTile, "town/walls", dID)

	got, err := svc.PathsByID(ctx, []int64{a, b})
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	if got[a] != "forest/trees" || got[b] != "town/walls" {
		t.Errorf("got %v", got)
	}
}

func TestMoveAssets_KindMismatchRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	tileFolder, _ := svc.Create(ctx, folders.CreateInput{Name: "Forest", KindRoot: folders.KindTile, CreatedBy: dID})

	asvc := assets.New(pool)
	spriteAsset, err := asvc.Create(ctx, assets.CreateInput{
		Kind:                 assets.KindSprite,
		Name:                 "hero",
		ContentAddressedPath: "test/hero",
		OriginalFormat:       "png",
		CreatedBy:            dID,
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}

	_, err = svc.MoveAssets(ctx, []int64{spriteAsset.ID}, &tileFolder.ID)
	if !errors.Is(err, folders.ErrCrossKindMove) {
		t.Fatalf("want ErrCrossKindMove, got %v", err)
	}
}

func TestMoveAssets_HappyPath(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	tileFolder, _ := svc.Create(ctx, folders.CreateInput{Name: "Forest", KindRoot: folders.KindTile, CreatedBy: dID})

	asvc := assets.New(pool)
	t1, _ := asvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindTile, Name: "tree-a", ContentAddressedPath: "test/a",
		OriginalFormat: "png", CreatedBy: dID,
	})
	t2, _ := asvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindTile, Name: "tree-b", ContentAddressedPath: "test/b",
		OriginalFormat: "png", CreatedBy: dID,
	})

	n, err := svc.MoveAssets(ctx, []int64{t1.ID, t2.ID}, &tileFolder.ID)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 moved, got %d", n)
	}

	got, _ := asvc.FindByID(ctx, t1.ID)
	if got.FolderID == nil || *got.FolderID != tileFolder.ID {
		t.Errorf("asset 1 folder = %v, want %d", got.FolderID, tileFolder.ID)
	}
}

func TestSetSortMode(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	f, _ := svc.Create(ctx, folders.CreateInput{Name: "Forest", KindRoot: folders.KindTile, CreatedBy: dID})

	if err := svc.SetSortMode(ctx, f.ID, folders.SortColor); err != nil {
		t.Fatalf("set sort: %v", err)
	}
	got, _ := svc.FindByID(ctx, f.ID)
	if got.SortMode != folders.SortColor {
		t.Errorf("got %s", got.SortMode)
	}

	if err := svc.SetSortMode(ctx, f.ID, folders.SortMode("nope")); !errors.Is(err, folders.ErrInvalidSortMode) {
		t.Fatalf("want ErrInvalidSortMode, got %v", err)
	}
}

func TestListByKindRoot_FlatOrdered(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc, dID := fixture(t, pool)
	ctx := context.Background()

	a, _ := svc.Create(ctx, folders.CreateInput{Name: "alpha", KindRoot: folders.KindTile, CreatedBy: dID})
	_, _ = svc.Create(ctx, folders.CreateInput{Name: "child-a", KindRoot: folders.KindTile, ParentID: &a.ID, CreatedBy: dID})
	_, _ = svc.Create(ctx, folders.CreateInput{Name: "beta", KindRoot: folders.KindTile, CreatedBy: dID})
	// Folder under a different kind_root must NOT appear.
	_, _ = svc.Create(ctx, folders.CreateInput{Name: "shouldnotappear", KindRoot: folders.KindSprite, CreatedBy: dID})

	got, err := svc.ListByKindRoot(ctx, folders.KindTile)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d (%+v)", len(got), got)
	}
	for _, f := range got {
		if f.KindRoot != folders.KindTile {
			t.Errorf("leaked %+v", f)
		}
	}
}

func TestAvailableSortModes_PerKind(t *testing.T) {
	contains := func(modes []folders.SortMode, want folders.SortMode) bool {
		for _, m := range modes {
			if m == want {
				return true
			}
		}
		return false
	}
	if contains(folders.AvailableSortModes(folders.KindAudio), folders.SortColor) {
		t.Error("audio should not offer sort by color")
	}
	if !contains(folders.AvailableSortModes(folders.KindAudio), folders.SortLength) {
		t.Error("audio should offer sort by length")
	}
	if contains(folders.AvailableSortModes(folders.KindTile), folders.SortLength) {
		t.Error("tile should not offer sort by length")
	}
	if !contains(folders.AvailableSortModes(folders.KindSprite), folders.SortColor) {
		t.Error("sprite should offer sort by color")
	}
}
