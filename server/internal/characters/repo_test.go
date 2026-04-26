// Boxland — characters: Service / repo CRUD tests against an isolated
// PostgreSQL via testdb.New(t). Mirrors the pattern in
// server/internal/entities/entity_type_test.go.

package characters_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	authplayer "boxland/server/internal/auth/player"
	"boxland/server/internal/characters"
	"boxland/server/internal/persistence/testdb"
)

// fixture bundles the per-test dependencies a CRUD test needs.
type fixture struct {
	pool       *pgxpool.Pool
	svc        *characters.Service
	designerID int64
}

// setup opens a fresh DB, creates a designer for FK satisfaction, and
// returns a cleanup-safe fixture.
func setup(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.New(t)
	if pool == nil {
		t.Skip("postgres unavailable")
	}
	t.Cleanup(pool.Close)

	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "characters-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	return &fixture{
		pool:       pool,
		svc:        characters.New(pool),
		designerID: d.ID,
	}
}

// makePlayer is a tiny helper for player_characters tests.
func makePlayer(t *testing.T, pool *pgxpool.Pool, email string) int64 {
	t.Helper()
	playerSvc := authplayer.New(pool, []byte("test-jwt-secret-change-me-please"))
	p, err := playerSvc.CreatePlayer(context.Background(), email, "pw-secret-1234")
	if err != nil {
		t.Fatalf("create player %s: %v", email, err)
	}
	return p.ID
}

// makeAsset is a tiny helper to insert a sprite asset for FK satisfaction.
func makeAsset(t *testing.T, svc *characters.Service, designerID int64) int64 {
	t.Helper()
	assetSvc := assets.New(svc.Pool)
	a, err := assetSvc.Create(context.Background(), assets.CreateInput{
		Kind:                 assets.KindSprite,
		Name:                 "test-sprite",
		ContentAddressedPath: "tests/sprite",
		OriginalFormat:       "png",
		MetadataJSON:         []byte(`{"grid_w":32,"grid_h":32,"cols":1,"rows":1,"frame_count":1}`),
		CreatedBy:            designerID,
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	return a.ID
}

// ---------------------------------------------------------------------------
// Slots
// ---------------------------------------------------------------------------

func TestCreateSlot_Roundtrip(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	got, err := f.svc.CreateSlot(ctx, characters.CreateSlotInput{
		Key: "extra_slot", Label: "Extra", OrderIndex: 999, DefaultLayerOrder: 1000,
		CreatedBy: f.designerID,
	})
	if err != nil {
		t.Fatalf("CreateSlot: %v", err)
	}
	if got.ID == 0 {
		t.Errorf("expected refresh of ID")
	}
	if got.CreatedBy == nil || *got.CreatedBy != f.designerID {
		t.Errorf("CreatedBy not persisted: %+v", got.CreatedBy)
	}

	again, err := f.svc.FindSlotByID(ctx, got.ID)
	if err != nil {
		t.Fatalf("FindSlotByID: %v", err)
	}
	if again.Key != "extra_slot" {
		t.Errorf("got %q", again.Key)
	}
}

func TestCreateSlot_DuplicateKeyRejected(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	if _, err := f.svc.CreateSlot(ctx, characters.CreateSlotInput{
		Key: "extra", Label: "Extra", CreatedBy: f.designerID,
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := f.svc.CreateSlot(ctx, characters.CreateSlotInput{
		Key: "extra", Label: "Extra2", CreatedBy: f.designerID,
	})
	if !errors.Is(err, characters.ErrKeyInUse) {
		t.Errorf("got %v, want ErrKeyInUse", err)
	}
}

func TestListSlots_IncludesSeeded(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	got, err := f.svc.ListSlots(ctx)
	if err != nil {
		t.Fatalf("ListSlots: %v", err)
	}
	// Migration seeds 24; no designer-authored rows yet.
	if len(got) != 24 {
		t.Errorf("expected 24 seeded slots, got %d", len(got))
	}
	// Ordered: body (10) is first.
	if got[0].Key != "body" {
		t.Errorf("expected body first, got %q", got[0].Key)
	}
}

func TestFindSlot_NotFound(t *testing.T) {
	f := setup(t)
	_, err := f.svc.FindSlotByID(context.Background(), 999_999)
	if !errors.Is(err, characters.ErrSlotNotFound) {
		t.Errorf("got %v, want ErrSlotNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Parts
// ---------------------------------------------------------------------------

func TestCreatePart_Roundtrip(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	// Use a seeded slot.
	slots, _ := f.svc.ListSlots(ctx)
	bodySlot := slots[0]
	assetID := makeAsset(t, f.svc, f.designerID)

	p, err := f.svc.CreatePart(ctx, characters.CreatePartInput{
		SlotID: bodySlot.ID, AssetID: assetID,
		Name: "Plain body", Tags: []string{"npc"},
		CreatedBy: f.designerID,
	})
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if p.ID == 0 {
		t.Errorf("expected refresh of ID")
	}
	// Default-empty FrameMapJSON should land as `{}`.
	if string(p.FrameMapJSON) != "{}" {
		t.Errorf("FrameMapJSON: got %q", p.FrameMapJSON)
	}
	if len(p.Tags) != 1 || p.Tags[0] != "npc" {
		t.Errorf("Tags: %v", p.Tags)
	}
}

func TestCreatePart_DuplicateSlotAssetRejected(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	slots, _ := f.svc.ListSlots(ctx)
	bodySlot := slots[0]
	assetID := makeAsset(t, f.svc, f.designerID)

	if _, err := f.svc.CreatePart(ctx, characters.CreatePartInput{
		SlotID: bodySlot.ID, AssetID: assetID, Name: "first",
		CreatedBy: f.designerID,
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := f.svc.CreatePart(ctx, characters.CreatePartInput{
		SlotID: bodySlot.ID, AssetID: assetID, Name: "second",
		CreatedBy: f.designerID,
	})
	if !errors.Is(err, characters.ErrKeyInUse) {
		t.Errorf("got %v, want ErrKeyInUse", err)
	}
}

func TestListParts_FilterBySlot(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	slots, _ := f.svc.ListSlots(ctx)
	a1 := makeAsset(t, f.svc, f.designerID)

	// Make a second asset for slot 2.
	assetSvc := assets.New(f.pool)
	a2row, _ := assetSvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "second", ContentAddressedPath: "tests/sprite2",
		OriginalFormat: "png", CreatedBy: f.designerID,
	})

	if _, err := f.svc.CreatePart(ctx, characters.CreatePartInput{SlotID: slots[0].ID, AssetID: a1, Name: "in slot 1", CreatedBy: f.designerID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreatePart(ctx, characters.CreatePartInput{SlotID: slots[1].ID, AssetID: a2row.ID, Name: "in slot 2", CreatedBy: f.designerID}); err != nil {
		t.Fatal(err)
	}

	got, err := f.svc.ListParts(ctx, characters.ListPartsOpts{SlotID: slots[0].ID})
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 part in slot 1, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// NPC templates
// ---------------------------------------------------------------------------

func TestCreateNpcTemplate_DuplicateNameRejected(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	if _, err := f.svc.CreateNpcTemplate(ctx, characters.CreateNpcTemplateInput{Name: "Goblin", CreatedBy: f.designerID}); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := f.svc.CreateNpcTemplate(ctx, characters.CreateNpcTemplateInput{Name: "Goblin", CreatedBy: f.designerID})
	if !errors.Is(err, characters.ErrNameInUse) {
		t.Errorf("got %v, want ErrNameInUse", err)
	}
}

// ---------------------------------------------------------------------------
// Player characters — owner scoping
// ---------------------------------------------------------------------------

func TestPlayerCharacter_CrossPlayerAccessForbidden(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	playerA := makePlayer(t, f.pool, "playera@x.com")
	playerB := makePlayer(t, f.pool, "playerb@x.com")

	// Insert one player_character belonging to A directly via SQL —
	// the create endpoint comes in Phase 4. The repo path is what we
	// want to exercise here: it must reject cross-player gets.
	var charID int64
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO player_characters (player_id, name) VALUES ($1, $2) RETURNING id
	`, playerA, "Aria").Scan(&charID); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A can read A's character.
	if _, err := f.svc.FindPlayerCharacter(ctx, playerA, charID); err != nil {
		t.Errorf("A reading A: %v", err)
	}
	// B cannot.
	_, err := f.svc.FindPlayerCharacter(ctx, playerB, charID)
	if !errors.Is(err, characters.ErrForbidden) {
		t.Errorf("B reading A: got %v, want ErrForbidden", err)
	}

	// ListPlayerCharacters never returns another player's rows.
	bChars, err := f.svc.ListPlayerCharacters(ctx, playerB)
	if err != nil {
		t.Fatalf("List B: %v", err)
	}
	if len(bChars) != 0 {
		t.Errorf("ListPlayerCharacters(B) returned %d rows, want 0", len(bChars))
	}
}
