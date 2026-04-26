package persistence_test

import (
	"context"
	"testing"
	"time"

	"boxland/server/internal/persistence"
	"boxland/server/internal/persistence/testdb"
)

func TestMeta_GetMissingReturnsEmpty(t *testing.T) {
	pool := testdb.New(t)
	if pool == nil {
		return
	}
	got, err := persistence.MetaGet(context.Background(), pool, "no-such-key")
	if err != nil {
		t.Fatalf("MetaGet on missing key: %v", err)
	}
	if got != "" {
		t.Errorf("MetaGet missing = %q, want empty", got)
	}
}

func TestMeta_SetThenGet(t *testing.T) {
	pool := testdb.New(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	if err := persistence.MetaSet(ctx, pool, "hello", "world"); err != nil {
		t.Fatalf("MetaSet: %v", err)
	}
	got, err := persistence.MetaGet(ctx, pool, "hello")
	if err != nil {
		t.Fatalf("MetaGet: %v", err)
	}
	if got != "world" {
		t.Errorf("MetaGet = %q, want world", got)
	}
}

func TestMeta_SetUpserts(t *testing.T) {
	pool := testdb.New(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	if err := persistence.MetaSet(ctx, pool, "k", "v1"); err != nil {
		t.Fatalf("first set: %v", err)
	}
	if err := persistence.MetaSet(ctx, pool, "k", "v2"); err != nil {
		t.Fatalf("second set: %v", err)
	}
	got, _ := persistence.MetaGet(ctx, pool, "k")
	if got != "v2" {
		t.Errorf("upsert: got %q, want v2", got)
	}
}

func TestRecordStartupRoundTrip(t *testing.T) {
	pool := testdb.New(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	when := time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)
	if err := persistence.RecordStartup(ctx, pool, "0.1.0", when); err != nil {
		t.Fatalf("RecordStartup: %v", err)
	}
	v, ts, ok, err := persistence.LastStartedVersion(ctx, pool)
	if err != nil {
		t.Fatalf("LastStartedVersion: %v", err)
	}
	if !ok {
		t.Fatalf("LastStartedVersion ok=false after RecordStartup")
	}
	if v != "0.1.0" {
		t.Errorf("version = %q, want 0.1.0", v)
	}
	if !ts.Equal(when) {
		t.Errorf("ts = %v, want %v", ts, when)
	}
}

func TestLastStartedVersion_FreshDB(t *testing.T) {
	pool := testdb.New(t)
	if pool == nil {
		return
	}
	v, _, ok, err := persistence.LastStartedVersion(context.Background(), pool)
	if err != nil {
		t.Fatalf("LastStartedVersion on fresh: %v", err)
	}
	if ok || v != "" {
		t.Errorf("fresh DB returned ok=%v v=%q, want false/empty", ok, v)
	}
}
