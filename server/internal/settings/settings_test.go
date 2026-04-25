package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/settings"
)

func openPool(t *testing.T) *pgxpool.Pool {
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

func TestService_GetEmptyReturnsObjectLiteral(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := settings.New(pool)
	got, err := svc.Get(context.Background(), settings.RealmDesigner, 123)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Errorf("empty get: got %s, want {}", got)
	}
}

func TestService_SaveThenGetRoundTrip(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := settings.New(pool)
	ctx := context.Background()

	payload := []byte(`{"v":1,"font":"Kubasta","audio":{"master":50,"music":40,"sfx":80}}`)
	if err := svc.Save(ctx, settings.RealmPlayer, 7, payload); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, settings.RealmPlayer, 7)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["font"] != "Kubasta" {
		t.Errorf("font: got %v", decoded["font"])
	}
}

func TestService_RealmIsolation(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := settings.New(pool)
	ctx := context.Background()
	if err := svc.Save(ctx, settings.RealmDesigner, 5, []byte(`{"font":"AtariGames"}`)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(ctx, settings.RealmPlayer, 5, []byte(`{"font":"Kubasta"}`)); err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Get(ctx, settings.RealmDesigner, 5)
	p, _ := svc.Get(ctx, settings.RealmPlayer, 5)
	if string(d) == string(p) {
		t.Errorf("realms collided: designer=%s player=%s", d, p)
	}
}

func TestService_SaveUpsertOverwrites(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := settings.New(pool)
	ctx := context.Background()
	if err := svc.Save(ctx, settings.RealmPlayer, 9, []byte(`{"font":"a"}`)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(ctx, settings.RealmPlayer, 9, []byte(`{"font":"b"}`)); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(ctx, settings.RealmPlayer, 9)
	if string(got) == "" || !contains(string(got), `"b"`) {
		t.Errorf("upsert did not overwrite: %s", got)
	}
}

func TestService_SaveRejectsNonObject(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := settings.New(pool)
	ctx := context.Background()
	if err := svc.Save(ctx, settings.RealmPlayer, 1, []byte(`[1,2,3]`)); err == nil {
		t.Errorf("expected error for array payload")
	}
	if err := svc.Save(ctx, settings.RealmPlayer, 1, []byte(`"oops"`)); err == nil {
		t.Errorf("expected error for string payload")
	}
}

func TestService_InvalidRealm(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := settings.New(pool)
	_, err := svc.Get(context.Background(), settings.Realm("bogus"), 1)
	if !errors.Is(err, settings.ErrInvalidRealm) {
		t.Errorf("got %v, want ErrInvalidRealm", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
