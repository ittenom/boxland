package automations_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/automations"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/persistence/testdb"
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

func newEntityType(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	authS := authdesigner.New(pool)
	d, err := authS.CreateDesigner(context.Background(), name+"@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	ents := entities.New(pool, components.Default())
	et, err := ents.Create(context.Background(), entities.CreateInput{Name: name, CreatedBy: d.ID})
	if err != nil {
		t.Fatal(err)
	}
	return et.ID
}

func TestService_GetEmpty(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := automations.New(pool, automations.DefaultTriggers(), automations.DefaultActions())
	id := newEntityType(t, pool, "empty-auto")
	got, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Automations) != 0 {
		t.Errorf("expected empty, got %d automations", len(got.Automations))
	}
}

func TestService_SaveValidatesBeforePersist(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := automations.New(pool, automations.DefaultTriggers(), automations.DefaultActions())
	id := newEntityType(t, pool, "validates")

	bad := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name:    "bad",
			Trigger: automations.TriggerNode{Kind: "nope"},
			Actions: []automations.ActionNode{{Kind: "spawn"}},
		}},
	}
	if err := svc.Save(context.Background(), id, bad); err == nil {
		t.Error("expected validation error before persist")
	}
}

func TestService_RoundTrip(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := automations.New(pool, automations.DefaultTriggers(), automations.DefaultActions())
	id := newEntityType(t, pool, "roundtrip")

	cfg, _ := json.Marshal(map[string]any{"type_id": 1})
	set := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name:    "spawn-on-spawn",
			Trigger: automations.TriggerNode{Kind: "on_spawn"},
			Actions: []automations.ActionNode{{Kind: "spawn", Config: cfg}},
		}},
	}
	if err := svc.Save(context.Background(), id, set); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Automations) != 1 || got.Automations[0].Name != "spawn-on-spawn" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestService_DeleteIdempotent(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := automations.New(pool, automations.DefaultTriggers(), automations.DefaultActions())
	id := newEntityType(t, pool, "del")
	if err := svc.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), id); err != nil {
		t.Errorf("delete should be idempotent: %v", err)
	}
}
