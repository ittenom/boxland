// Boxland — characters: end-to-end publish-with-bake integration.
//
// This is the spec's central acceptance scenario:
//
//   designer composes a recipe -> drafts an NPC template referencing
//   the recipe -> publishes -> a baked sprite asset exists, a
//   character_bakes row in 'baked' status exists, the NPC template's
//   active_bake_id and entity_type_id are populated, and the
//   auto-minted entity_type row exists with the bake asset attached.
//
// Driven through artifact.Pipeline.Run so we exercise the same
// transaction boundary the production publish surface uses.

package characters_test

import (
	"context"
	"encoding/json"
	"image/color"
	"testing"

	"boxland/server/internal/assets"
	"boxland/server/internal/characters"
	"boxland/server/internal/publishing/artifact"
)

func TestPublish_WithRecipe_BakesAndAutoMintsEntityType(t *testing.T) {
	f := setup(t)
	store := makeBakeStore(t)
	ctx := context.Background()

	// Wire bake deps onto the service so the handler can find them.
	f.svc.SetBakeDeps(store, assets.New(f.pool))

	// Seed: two parts (body + hair) and a recipe selecting them.
	slots, _ := f.svc.ListSlots(ctx)
	bodySlot := slots[0]
	var hairSlot characters.Slot
	for _, s := range slots {
		if s.Key == "hair_front" {
			hairSlot = s
		}
	}
	body := uploadPart(t, ctx, f, store, bodySlot.ID, "body", color.NRGBA{200, 80, 60, 255}, `{"idle":[0,0]}`)
	hair := uploadPart(t, ctx, f, store, hairSlot.ID, "hair", color.NRGBA{40, 30, 20, 255}, `{"idle":[0,0]}`)
	recipeID, _ := seedRecipe(t, ctx, f, []*characters.Part{body, hair})

	// Create the NPC template shell, then write a draft for it that
	// pins the recipe (this is what the Generator UI POSTs).
	tmpl, err := f.svc.CreateNpcTemplate(ctx, characters.CreateNpcTemplateInput{
		Name: "Goblin chief", CreatedBy: f.designerID,
	})
	if err != nil {
		t.Fatalf("create npc template: %v", err)
	}
	draft := characters.NpcTemplateDraft{
		Name:     "Goblin chief",
		Tags:     []string{"npc", "boss"},
		RecipeID: &recipeID,
	}
	draftJSON, _ := json.Marshal(draft)
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
		VALUES ('npc_template', $1, $2::jsonb, $3)
	`, tmpl.ID, draftJSON, f.designerID); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	// Wire a publish pipeline with just the npc_template handler.
	registry := artifact.NewRegistry()
	registry.Register(characters.NewNpcTemplateHandler(f.svc))
	pipe := artifact.NewPipeline(f.pool, registry)

	outs, err := pipe.Run(ctx, f.designerID)
	if err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(outs))
	}
	if outs[0].Op != artifact.OpUpdated {
		t.Errorf("op = %q", outs[0].Op)
	}

	// Verify the NPC template row now has active_bake_id + entity_type_id.
	var activeBakeID, entityTypeID *int64
	if err := f.pool.QueryRow(ctx, `
		SELECT active_bake_id, entity_type_id FROM npc_templates WHERE id = $1
	`, tmpl.ID).Scan(&activeBakeID, &entityTypeID); err != nil {
		t.Fatalf("read npc template: %v", err)
	}
	if activeBakeID == nil {
		t.Errorf("active_bake_id is null after publish")
	}
	if entityTypeID == nil {
		t.Errorf("entity_type_id is null after publish (auto-mint failed)")
	}

	// The bake row exists and is 'baked'.
	var bakeStatus string
	var bakeAssetID int64
	if err := f.pool.QueryRow(ctx, `
		SELECT status, asset_id FROM character_bakes WHERE id = $1
	`, *activeBakeID).Scan(&bakeStatus, &bakeAssetID); err != nil {
		t.Fatalf("read bake: %v", err)
	}
	if bakeStatus != "baked" {
		t.Errorf("bake status = %q, want baked", bakeStatus)
	}

	// The auto-minted entity_type points at the bake's asset.
	var entitySpriteAssetID *int64
	var entityName string
	var entityTags []string
	if err := f.pool.QueryRow(ctx, `
		SELECT name, sprite_asset_id, tags FROM entity_types WHERE id = $1
	`, *entityTypeID).Scan(&entityName, &entitySpriteAssetID, &entityTags); err != nil {
		t.Fatalf("read entity_type: %v", err)
	}
	if entitySpriteAssetID == nil || *entitySpriteAssetID != bakeAssetID {
		t.Errorf("entity_type.sprite_asset_id = %v, want %d", entitySpriteAssetID, bakeAssetID)
	}
	if entityName != "Goblin chief" {
		t.Errorf("entity_type.name = %q", entityName)
	}
	hasNpcTag := false
	for _, tg := range entityTags {
		if tg == "npc" {
			hasNpcTag = true
		}
	}
	if !hasNpcTag {
		t.Errorf("auto-minted entity_type missing 'npc' tag; got %v", entityTags)
	}

	// The drafts row was consumed by the publish.
	var leftover int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM drafts WHERE artifact_kind = 'npc_template' AND artifact_id = $1
	`, tmpl.ID).Scan(&leftover); err != nil {
		t.Fatalf("count remaining drafts: %v", err)
	}
	if leftover != 0 {
		t.Errorf("draft row still present after publish (%d rows)", leftover)
	}
}

// seedStatSet inserts a stat set with the canonical might/wit/grit
// shape used elsewhere in the test suite. Returns the new id.
func seedStatSet(t *testing.T, ctx context.Context, f *fixture, pool int) int64 {
	t.Helper()
	stats := []map[string]any{
		{"key": "might", "label": "Might", "kind": "core", "default": 1, "min": 1, "max": 10, "creation_cost": 1, "display_order": 1},
		{"key": "wit", "label": "Wit", "kind": "core", "default": 1, "min": 1, "max": 10, "creation_cost": 1, "display_order": 2},
		{"key": "grit", "label": "Grit", "kind": "core", "default": 1, "min": 1, "max": 10, "creation_cost": 1, "display_order": 3},
		{"key": "talent_points", "label": "Talent points", "kind": "resource", "default": 5, "display_order": 4},
	}
	statsJSON, _ := json.Marshal(stats)
	rules, _ := json.Marshal(map[string]any{"method": "point_buy", "pool": pool})
	var id int64
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO character_stat_sets (key, name, stats_json, creation_rules_json, created_by)
		VALUES ('default', 'Default', $1::jsonb, $2::jsonb, $3) RETURNING id
	`, statsJSON, rules, f.designerID).Scan(&id); err != nil {
		t.Fatalf("seed stat set: %v", err)
	}
	return id
}

func TestPublish_RejectsRecipeWithBadStatAllocation(t *testing.T) {
	f := setup(t)
	store := makeBakeStore(t)
	ctx := context.Background()
	f.svc.SetBakeDeps(store, assets.New(f.pool))

	statSetID := seedStatSet(t, ctx, f, 6) // pool = 6

	slots, _ := f.svc.ListSlots(ctx)
	body := uploadPart(t, ctx, f, store, slots[0].ID, "body", anyColor(), `{"idle":[0,0]}`)

	// Recipe spends 5 points (against pool of 6) -> validation should reject.
	appearance, _ := json.Marshal(characters.AppearanceSelection{
		Slots: []characters.AppearanceSlot{{SlotKey: "body", PartID: body.ID}},
	})
	stats, _ := json.Marshal(characters.StatSelection{
		SetID:       statSetID,
		Allocations: map[string]int{"might": 3, "wit": 2}, // 5 != 6
	})
	hash, _ := characters.ComputeRecipeHash("Bad alloc", appearance, stats, nil)
	var recipeID int64
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO character_recipes
			(owner_kind, owner_id, name, appearance_json, stats_json, recipe_hash, created_by)
		VALUES ('designer', $1, 'Bad alloc', $2::jsonb, $3::jsonb, $4, $1)
		RETURNING id
	`, f.designerID, appearance, stats, hash).Scan(&recipeID); err != nil {
		t.Fatalf("seed recipe: %v", err)
	}

	tmpl, _ := f.svc.CreateNpcTemplate(ctx, characters.CreateNpcTemplateInput{
		Name: "Bad", CreatedBy: f.designerID,
	})
	draft := characters.NpcTemplateDraft{Name: "Bad", RecipeID: &recipeID}
	js, _ := json.Marshal(draft)
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
		VALUES ('npc_template', $1, $2::jsonb, $3)
	`, tmpl.ID, js, f.designerID); err != nil {
		t.Fatal(err)
	}

	registry := artifact.NewRegistry()
	registry.Register(characters.NewNpcTemplateHandler(f.svc))
	pipe := artifact.NewPipeline(f.pool, registry)

	_, err := pipe.Run(ctx, f.designerID)
	if err == nil {
		t.Fatal("expected publish to fail on bad allocation")
	}
	// The validator's error should mention the pool.
	if !contains(err.Error(), "spent 5 points") {
		t.Errorf("error %q didn't mention pool spend", err.Error())
	}
}

func anyColor() color.NRGBA { return color.NRGBA{200, 80, 60, 255} }
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestPublish_RepublishSameRecipe_ReusesBake(t *testing.T) {
	f := setup(t)
	store := makeBakeStore(t)
	ctx := context.Background()
	f.svc.SetBakeDeps(store, assets.New(f.pool))

	slots, _ := f.svc.ListSlots(ctx)
	body := uploadPart(t, ctx, f, store, slots[0].ID, "body", color.NRGBA{255, 0, 0, 255}, `{"idle":[0,0]}`)
	recipeID, _ := seedRecipe(t, ctx, f, []*characters.Part{body})

	tmpl, _ := f.svc.CreateNpcTemplate(ctx, characters.CreateNpcTemplateInput{Name: "Goblin", CreatedBy: f.designerID})
	draft := characters.NpcTemplateDraft{Name: "Goblin", RecipeID: &recipeID}
	js, _ := json.Marshal(draft)

	registry := artifact.NewRegistry()
	registry.Register(characters.NewNpcTemplateHandler(f.svc))
	pipe := artifact.NewPipeline(f.pool, registry)

	// Publish once to land a bake.
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
		VALUES ('npc_template', $1, $2::jsonb, $3)
	`, tmpl.ID, js, f.designerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pipe.Run(ctx, f.designerID); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	var firstBakeID int64
	_ = f.pool.QueryRow(ctx, `SELECT active_bake_id FROM npc_templates WHERE id = $1`, tmpl.ID).Scan(&firstBakeID)
	var bakeRowsBefore int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM character_bakes WHERE status = 'baked'`).Scan(&bakeRowsBefore)

	// Re-publish with an unchanged draft. The bake should be reused
	// (no new character_bakes row).
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
		VALUES ('npc_template', $1, $2::jsonb, $3)
	`, tmpl.ID, js, f.designerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pipe.Run(ctx, f.designerID); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	var bakeRowsAfter int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM character_bakes WHERE status = 'baked'`).Scan(&bakeRowsAfter)
	if bakeRowsAfter != bakeRowsBefore {
		t.Errorf("re-publish created a new baked row (was %d, now %d)", bakeRowsBefore, bakeRowsAfter)
	}
	var secondBakeID int64
	_ = f.pool.QueryRow(ctx, `SELECT active_bake_id FROM npc_templates WHERE id = $1`, tmpl.ID).Scan(&secondBakeID)
	if secondBakeID != firstBakeID {
		t.Errorf("active_bake_id changed after no-op republish (%d -> %d)", firstBakeID, secondBakeID)
	}
}


