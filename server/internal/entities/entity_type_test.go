package entities_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/publishing/artifact"
)

// openTestPool returns an isolated, freshly-migrated DB. testdb.New wires its own t.Cleanup that drops the database when the test ends.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// resetDB creates per-test fixtures (a designer, etc.). The pool is already
// empty because testdb.New(t) returns a fresh database for every test.
func resetDB(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "entity-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	return d.ID
}

func TestCreate_AppliesDefaults(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())

	et, err := svc.Create(context.Background(), entities.CreateInput{
		Name:      "boss",
		CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if et.ColliderW != 16 || et.ColliderH != 16 {
		t.Errorf("expected default collider 16x16, got %dx%d", et.ColliderW, et.ColliderH)
	}
	if et.DefaultCollisionMask != 1 {
		t.Errorf("expected default mask 1, got %d", et.DefaultCollisionMask)
	}
	if et.Tags == nil {
		t.Errorf("Tags should be empty slice, not nil (postgres TEXT[] NOT NULL DEFAULT '{}')")
	}
}

func TestCreate_DuplicateNameRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	if _, err := svc.Create(ctx, entities.CreateInput{Name: "shared", CreatedBy: designerID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, entities.CreateInput{Name: "shared", CreatedBy: designerID}); !errors.Is(err, entities.ErrNameInUse) {
		t.Errorf("got %v, want ErrNameInUse", err)
	}
}

func TestSetComponents_RoundtripAndReplace(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	et, _ := svc.Create(ctx, entities.CreateInput{Name: "mob", CreatedBy: designerID})

	// First set: Position + Velocity.
	if err := svc.SetComponents(ctx, nil, et.ID, map[components.Kind]json.RawMessage{
		components.KindPosition: []byte(`{"x":256,"y":-128}`),
		components.KindVelocity: []byte(`{"vx":0,"vy":0,"max_speed":1024}`),
	}); err != nil {
		t.Fatalf("SetComponents: %v", err)
	}

	got, err := svc.Components(ctx, et.ID)
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 components, got %d", len(got))
	}

	// Second set should REPLACE not append.
	if err := svc.SetComponents(ctx, nil, et.ID, map[components.Kind]json.RawMessage{
		components.KindCollider: []byte(`{"w":12,"h":12,"anchor_x":6,"anchor_y":6,"mask":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Components(ctx, et.ID)
	if len(got) != 1 || got[0].Kind != components.KindCollider {
		t.Errorf("replace didn't take; got %+v", got)
	}
}

func TestSetComponents_ValidatesEachKind(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	et, _ := svc.Create(ctx, entities.CreateInput{Name: "v", CreatedBy: designerID})

	bad := map[components.Kind]json.RawMessage{
		components.KindCollider: []byte(`{"w":4,"h":4,"anchor_x":99,"anchor_y":99}`),
	}
	if err := svc.SetComponents(ctx, nil, et.ID, bad); err == nil {
		t.Fatal("expected validation error from collider")
	}
	got, _ := svc.Components(ctx, et.ID)
	if len(got) != 0 {
		t.Errorf("validation failure should not have written any components, got %d", len(got))
	}
}

func TestEntityTypeHandler_PublishUpdatesRowAndComponents(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := entities.New(pool, components.Default())
	ctx := context.Background()

	live, _ := svc.Create(ctx, entities.CreateInput{
		Name:      "old",
		ColliderW: 16, ColliderH: 16, ColliderAnchorX: 8, ColliderAnchorY: 16,
		CreatedBy: designerID,
	})

	draft := entities.EntityTypeDraft{
		Name:                 "new",
		ColliderW:            32, ColliderH: 32,
		ColliderAnchorX:      16, ColliderAnchorY: 32,
		DefaultCollisionMask: 5,
		Tags:                 []string{"shiny"},
		Components: map[components.Kind]json.RawMessage{
			components.KindPosition: []byte(`{"x":1024,"y":2048}`),
		},
	}
	body, _ := json.Marshal(draft)
	if _, err := pool.Exec(ctx,
		`INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by) VALUES ($1, $2, $3, $4)`,
		string(artifact.KindEntityType), live.ID, body, designerID,
	); err != nil {
		t.Fatal(err)
	}

	registry := artifact.NewRegistry()
	registry.Register(entities.NewHandler(svc))
	pipe := artifact.NewPipeline(pool, registry)

	outcomes, err := pipe.Run(ctx, designerID)
	if err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("expected one outcome, got %d", len(outcomes))
	}

	got, _ := svc.FindByID(ctx, live.ID)
	if got.Name != "new" || got.ColliderW != 32 || got.DefaultCollisionMask != 5 {
		t.Errorf("row not updated: %+v", got)
	}
	comps, _ := svc.Components(ctx, live.ID)
	if len(comps) != 1 || comps[0].Kind != components.KindPosition {
		t.Errorf("components not applied: %+v", comps)
	}
}

func TestEntityTypeHandler_RejectsBadCollider(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := entities.New(pool, components.Default())

	h := entities.NewHandler(svc)
	body, _ := json.Marshal(entities.EntityTypeDraft{
		Name:           "bad",
		ColliderW:      16,
		ColliderH:      16,
		ColliderAnchorX: 99, // anchor outside w/h
	})
	if err := h.Validate(context.Background(), artifact.DraftRow{
		ArtifactKind: artifact.KindEntityType,
		DraftJSON:    body,
	}); err == nil {
		t.Fatal("expected validation error from collider anchor check")
	}
}
