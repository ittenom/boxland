package flags_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/flags"
	"boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
)

// openTestPool returns an isolated, freshly-migrated DB. testdb.New wires its own t.Cleanup that drops the database when the test ends.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// seedMap creates a designer + map so flag rows have FK targets.
// The pool is already empty because testdb.New(t) returns a fresh database for every test.
func seedMap(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "flags-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	mapsSvc := maps.New(pool)
	m, err := mapsSvc.Create(context.Background(), maps.CreateInput{
		Name: "flag-test-map", Width: 32, Height: 32, CreatedBy: d.ID,
	})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	return m.ID
}

func TestSetBool_RoundTrip(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	mapID := seedMap(t, pool)

	svc := flags.New(pool)
	ctx := context.Background()

	if err := svc.SetBool(ctx, mapID, "met_king", true); err != nil {
		t.Fatalf("SetBool: %v", err)
	}
	got, err := svc.Get(ctx, mapID, "met_king")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Kind != flags.KindBool || got.Bool != true {
		t.Errorf("got %+v, want bool=true", got)
	}
}

func TestSetInt_AndAdd_AreAtomic(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	mapID := seedMap(t, pool)

	svc := flags.New(pool)
	ctx := context.Background()

	// Add to a missing key should create it with the delta value.
	v, err := svc.Add(ctx, mapID, "gold", 100)
	if err != nil {
		t.Fatalf("Add (insert): %v", err)
	}
	if v != 100 {
		t.Errorf("first Add: got %d, want 100", v)
	}
	// Subsequent Add should accumulate, not overwrite.
	v, err = svc.Add(ctx, mapID, "gold", -25)
	if err != nil {
		t.Fatalf("Add (accumulate): %v", err)
	}
	if v != 75 {
		t.Errorf("second Add: got %d, want 75", v)
	}
	// SetInt should overwrite.
	if err := svc.SetInt(ctx, mapID, "gold", 1); err != nil {
		t.Fatalf("SetInt: %v", err)
	}
	got, err := svc.Get(ctx, mapID, "gold")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Int != 1 {
		t.Errorf("after SetInt: got %d, want 1", got.Int)
	}
}

func TestKindMismatch_OnTypeChange(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	mapID := seedMap(t, pool)

	svc := flags.New(pool)
	ctx := context.Background()

	if err := svc.SetBool(ctx, mapID, "x", true); err != nil {
		t.Fatal(err)
	}
	// Trying to overwrite a bool flag with an int must fail loudly so
	// designers see the bug at publish, not at runtime via a silent
	// coercion that breaks downstream triggers.
	err := svc.SetInt(ctx, mapID, "x", 42)
	if !errors.Is(err, flags.ErrKindMismatch) {
		t.Errorf("SetInt over bool: want ErrKindMismatch, got %v", err)
	}
	// Add against a bool flag also rejects.
	if _, err := svc.Add(ctx, mapID, "x", 1); !errors.Is(err, flags.ErrKindMismatch) {
		t.Errorf("Add over bool: want ErrKindMismatch, got %v", err)
	}
}

func TestLoadAll_IsLexical_AndScopedToMap(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	mapID := seedMap(t, pool)

	svc := flags.New(pool)
	ctx := context.Background()
	for _, k := range []string{"zebra", "apple", "mango"} {
		if err := svc.SetInt(ctx, mapID, k, 1); err != nil {
			t.Fatal(err)
		}
	}
	out, err := svc.LoadAll(ctx, mapID)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("LoadAll len: got %d, want 3", len(out))
	}
	for i, want := range []string{"apple", "mango", "zebra"} {
		if out[i].Key != want {
			t.Errorf("[%d] got %q, want %q", i, out[i].Key, want)
		}
	}

	// A different map_id must see zero rows -- tenant isolation.
	otherID := seedMapForIsolation(t, pool)
	other, err := svc.LoadAll(ctx, otherID)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("cross-map leak: got %d rows on a fresh map", len(other))
	}
}

// seedMapForIsolation makes a SECOND map without resetting the DB so we
// can prove flags don't leak across maps in the same tenant.
func seedMapForIsolation(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "flags-iso@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	mapsSvc := maps.New(pool)
	m, err := mapsSvc.Create(context.Background(), maps.CreateInput{
		Name: "iso-map", Width: 16, Height: 16, CreatedBy: d.ID,
	})
	if err != nil {
		t.Fatalf("create iso map: %v", err)
	}
	return m.ID
}

func TestDelete_RemovesRow(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	mapID := seedMap(t, pool)

	svc := flags.New(pool)
	ctx := context.Background()
	if err := svc.SetBool(ctx, mapID, "tmp", true); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, mapID, "tmp"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, mapID, "tmp"); !errors.Is(err, flags.ErrNotFound) {
		t.Errorf("after delete: want ErrNotFound, got %v", err)
	}
	if err := svc.Delete(ctx, mapID, "tmp"); !errors.Is(err, flags.ErrNotFound) {
		t.Errorf("double delete: want ErrNotFound, got %v", err)
	}
}

func TestValidateKey_RejectsEmptyAndOversize(t *testing.T) {
	svc := flags.New(nil) // doesn't need DB for the validation path
	ctx := context.Background()
	if err := svc.SetBool(ctx, 1, "", true); !errors.Is(err, flags.ErrInvalidKey) {
		t.Errorf("empty key: want ErrInvalidKey, got %v", err)
	}
	long := make([]byte, flags.MaxKeyLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := svc.SetBool(ctx, 1, string(long), true); !errors.Is(err, flags.ErrInvalidKey) {
		t.Errorf("oversize key: want ErrInvalidKey, got %v", err)
	}
}
