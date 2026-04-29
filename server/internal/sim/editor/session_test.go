package editor_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence/testdb"
	"boxland/server/internal/sim/editor"
)

// fixture seeds a designer + map + level + entity_type so every
// test starts with a runnable level placement environment.
func fixture(t *testing.T) (pool *pgxpool.Pool, deps editor.Deps, designerID, levelID, entityTypeID int64) {
	t.Helper()
	pool = testdb.New(t)
	t.Cleanup(pool.Close)

	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "edit-session@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	mp := mapsservice.New(pool)
	m, err := mp.Create(context.Background(), mapsservice.CreateInput{Name: "m", Width: 8, Height: 8, CreatedBy: d.ID})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	lvSvc := levels.New(pool)
	lv, err := lvSvc.Create(context.Background(), levels.CreateInput{Name: "Level", MapID: m.ID, CreatedBy: d.ID})
	if err != nil {
		t.Fatalf("create level: %v", err)
	}
	es := entities.New(pool, components.Default())
	et, err := es.Create(context.Background(), entities.CreateInput{Name: "spawn", EntityClass: entities.ClassLogic, CreatedBy: d.ID})
	if err != nil {
		t.Fatalf("create entity_type: %v", err)
	}
	return pool, editor.Deps{Levels: lvSvc, Maps: mp}, d.ID, lv.ID, et.ID
}

func TestSession_PlaceLevelEntity_Roundtrip(t *testing.T) {
	_, deps, _, levelID, etID := fixture(t)
	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindLevelEditor, TargetID: levelID})

	op := &editor.PlaceLevelEntityOp{LevelID: levelID, EntityTypeID: etID, X: 3, Y: 4}
	diff, err := ses.Apply(context.Background(), deps, op)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if diff.Kind != editor.DiffPlacementAdded {
		t.Errorf("kind: got %d want PlacementAdded", diff.Kind)
	}
	undo, redo := ses.HistoryDepths()
	if undo != 1 || redo != 0 {
		t.Errorf("depths: got (%d,%d) want (1,0)", undo, redo)
	}

	// One entity in the level after Apply.
	all, err := deps.Levels.ListEntities(context.Background(), levelID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 placement, got %d", len(all))
	}
}

func TestSession_PlaceUndoRedo_RoundTrip(t *testing.T) {
	_, deps, _, levelID, etID := fixture(t)
	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindLevelEditor, TargetID: levelID})
	ctx := context.Background()

	// Place.
	if _, err := ses.Apply(ctx, deps, &editor.PlaceLevelEntityOp{LevelID: levelID, EntityTypeID: etID, X: 1, Y: 1}); err != nil {
		t.Fatal(err)
	}
	all, _ := deps.Levels.ListEntities(ctx, levelID)
	if len(all) != 1 {
		t.Fatalf("after place: %d", len(all))
	}

	// Undo => row deleted.
	if _, err := ses.Undo(ctx, deps); err != nil {
		t.Fatal(err)
	}
	all, _ = deps.Levels.ListEntities(ctx, levelID)
	if len(all) != 0 {
		t.Fatalf("after undo: %d, want 0", len(all))
	}
	undo, redo := ses.HistoryDepths()
	if undo != 0 || redo != 1 {
		t.Errorf("after undo, depths: got (%d,%d) want (0,1)", undo, redo)
	}

	// Redo => row recreated (with a fresh id).
	if _, err := ses.Redo(ctx, deps); err != nil {
		t.Fatal(err)
	}
	all, _ = deps.Levels.ListEntities(ctx, levelID)
	if len(all) != 1 {
		t.Fatalf("after redo: %d, want 1", len(all))
	}
	undo, redo = ses.HistoryDepths()
	if undo != 1 || redo != 0 {
		t.Errorf("after redo, depths: got (%d,%d) want (1,0)", undo, redo)
	}
}

func TestSession_MoveLevelEntity_RecordsPriorPosition(t *testing.T) {
	_, deps, _, levelID, etID := fixture(t)
	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindLevelEditor, TargetID: levelID})
	ctx := context.Background()

	placeOp := &editor.PlaceLevelEntityOp{LevelID: levelID, EntityTypeID: etID, X: 0, Y: 0}
	if _, err := ses.Apply(ctx, deps, placeOp); err != nil {
		t.Fatal(err)
	}
	all, _ := deps.Levels.ListEntities(ctx, levelID)
	pid := all[0].ID

	moveOp := &editor.MoveLevelEntityOp{LevelID: levelID, PlacementID: pid, X: 5, Y: 6, RotationDegrees: 90}
	if _, err := ses.Apply(ctx, deps, moveOp); err != nil {
		t.Fatal(err)
	}
	all, _ = deps.Levels.ListEntities(ctx, levelID)
	if all[0].X != 5 || all[0].Y != 6 || all[0].RotationDegrees != 90 {
		t.Errorf("after move: %+v", all[0])
	}

	// Undo move -> back to original.
	if _, err := ses.Undo(ctx, deps); err != nil {
		t.Fatal(err)
	}
	all, _ = deps.Levels.ListEntities(ctx, levelID)
	if all[0].X != 0 || all[0].Y != 0 || all[0].RotationDegrees != 0 {
		t.Errorf("after undo move: %+v want (0,0,0)", all[0])
	}
}

func TestSession_BroadcastsToSubscribers(t *testing.T) {
	_, deps, _, levelID, etID := fixture(t)
	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindLevelEditor, TargetID: levelID})
	ctx := context.Background()

	a := make(chan editor.Diff, 8)
	b := make(chan editor.Diff, 8)
	unsubA := ses.Subscribe(1, a)
	unsubB := ses.Subscribe(2, b)
	defer unsubA()
	defer unsubB()

	if _, err := ses.Apply(ctx, deps, &editor.PlaceLevelEntityOp{LevelID: levelID, EntityTypeID: etID, X: 1, Y: 2}); err != nil {
		t.Fatal(err)
	}
	if got := <-a; got.Kind != editor.DiffPlacementAdded {
		t.Errorf("subscriber a: got kind %d", got.Kind)
	}
	if got := <-b; got.Kind != editor.DiffPlacementAdded {
		t.Errorf("subscriber b: got kind %d", got.Kind)
	}
}

func TestManager_GetOrCreateAndClose(t *testing.T) {
	m := editor.NewManager()
	key := editor.SessionKey{Kind: editor.KindLevelEditor, TargetID: 42}

	a := m.GetOrCreate(key)
	b := m.GetOrCreate(key)
	if a != b {
		t.Errorf("GetOrCreate not idempotent")
	}
	if m.Find(key) != a {
		t.Errorf("Find returned different instance")
	}
	if got := len(m.Sessions()); got != 1 {
		t.Errorf("Sessions count: got %d want 1", got)
	}
	m.CloseSession(key)
	if m.Find(key) != nil {
		t.Errorf("session still present after close")
	}
}

func TestSession_UndoOnEmptyStackEmitsHistoryChanged(t *testing.T) {
	_, deps, _, levelID, _ := fixture(t)
	ses := editor.NewSession(editor.SessionKey{Kind: editor.KindLevelEditor, TargetID: levelID})
	sub := make(chan editor.Diff, 4)
	defer ses.Subscribe(1, sub)()
	d, err := ses.Undo(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != editor.DiffHistoryChanged {
		t.Errorf("kind: got %d want HistoryChanged", d.Kind)
	}
}
