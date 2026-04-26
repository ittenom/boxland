// Boxland — characters: artifact-handler tests.
//
// Drives each handler's Validate + Publish path against an isolated
// PostgreSQL via testdb.New(t). Mirrors the pattern used by entities/
// (which keeps its handler tests in entity_type_test.go but follows the
// same shape — Validate negative cases, Publish updates the live row,
// diff is non-empty when the draft differs).

package characters_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/characters"
	"boxland/server/internal/publishing/artifact"
)

// withTx runs fn inside a transaction that is always rolled back, so
// each test case starts from the migrated baseline. Mirrors how
// publish/Pipeline.Preview behaves.
func withTx(t *testing.T, f *fixture, fn func(tx pgx.Tx)) {
	t.Helper()
	tx, err := f.pool.BeginTx(context.Background(), pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	fn(tx)
}

// ---------------------------------------------------------------------------
// SlotHandler
// ---------------------------------------------------------------------------

func TestSlotHandler_KindAndValidate(t *testing.T) {
	h := characters.NewSlotHandler(nil)
	if h.Kind() != artifact.KindCharacterSlot {
		t.Errorf("Kind() = %q", h.Kind())
	}

	// Bad JSON.
	if err := h.Validate(context.Background(), artifact.DraftRow{
		ArtifactKind: artifact.KindCharacterSlot, ArtifactID: 1,
		DraftJSON: []byte(`{not json`),
	}); err == nil {
		t.Errorf("expected json error")
	}

	// Bad slot key.
	body, _ := json.Marshal(characters.SlotDraft{Key: "Bad Key", Label: "x"})
	if err := h.Validate(context.Background(), artifact.DraftRow{
		ArtifactKind: artifact.KindCharacterSlot, DraftJSON: body,
	}); err == nil {
		t.Errorf("expected validate error")
	}

	// Good draft passes.
	body, _ = json.Marshal(characters.SlotDraft{Key: "extra", Label: "Extra"})
	if err := h.Validate(context.Background(), artifact.DraftRow{
		ArtifactKind: artifact.KindCharacterSlot, DraftJSON: body,
	}); err != nil {
		t.Errorf("good draft: %v", err)
	}
}

func TestSlotHandler_PublishUpdatesLiveRow(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	h := characters.NewSlotHandler(f.svc)

	// Pick the seeded `body` slot and re-label it via a draft.
	slots, _ := f.svc.ListSlots(ctx)
	body := slots[0]
	if body.Key != "body" {
		t.Fatalf("expected body first; got %q", body.Key)
	}

	draft := characters.SlotDraft{
		Key: "body", Label: "Body (renamed)", Required: true,
		OrderIndex: body.OrderIndex, DefaultLayerOrder: body.DefaultLayerOrder,
		AllowsPalette: body.AllowsPalette,
	}

	withTx(t, f, func(tx pgx.Tx) {
		js, _ := json.Marshal(draft)
		res, err := h.Publish(ctx, tx, artifact.DraftRow{
			ArtifactKind: artifact.KindCharacterSlot, ArtifactID: slots[0].ID,
			DraftJSON: js, CreatedBy: f.designerID,
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if res.Op != artifact.OpUpdated {
			t.Errorf("Op = %q, want updated", res.Op)
		}
		// Diff should mention the label change.
		var sawLabel bool
		for _, c := range res.Diff.Changes {
			if c.Path == "label" {
				sawLabel = true
			}
		}
		if !sawLabel {
			t.Errorf("expected diff to include label change; got %+v", res.Diff.Changes)
		}

		// Read inside the same tx to confirm the row updated.
		var lbl string
		if err := tx.QueryRow(ctx, `SELECT label FROM character_slots WHERE id = $1`, slots[0].ID).Scan(&lbl); err != nil {
			t.Fatalf("readback: %v", err)
		}
		if lbl != "Body (renamed)" {
			t.Errorf("label after publish = %q", lbl)
		}
	})
}

// ---------------------------------------------------------------------------
// PartHandler
// ---------------------------------------------------------------------------

func TestPartHandler_PublishUpdatesLiveRow(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	// Seed a part via the service so the row exists for the handler to update.
	slots, _ := f.svc.ListSlots(ctx)
	assetID := makeAsset(t, f.svc, f.designerID)
	row, err := f.svc.CreatePart(ctx, characters.CreatePartInput{
		SlotID: slots[0].ID, AssetID: assetID, Name: "Original",
		CreatedBy: f.designerID,
	})
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}

	draft := characters.PartDraft{
		SlotID: slots[0].ID, AssetID: assetID,
		Name: "Renamed", Tags: []string{"npc"},
		FrameMapJSON: json.RawMessage(`{"idle":[0,0]}`),
	}
	js, _ := json.Marshal(draft)
	h := characters.NewPartHandler(f.svc)

	withTx(t, f, func(tx pgx.Tx) {
		res, err := h.Publish(ctx, tx, artifact.DraftRow{
			ArtifactKind: artifact.KindCharacterPart, ArtifactID: row.ID,
			DraftJSON: js, CreatedBy: f.designerID,
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if res.Op != artifact.OpUpdated {
			t.Errorf("Op = %q", res.Op)
		}
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM character_parts WHERE id = $1`, row.ID).Scan(&name); err != nil {
			t.Fatalf("readback: %v", err)
		}
		if name != "Renamed" {
			t.Errorf("name after publish = %q", name)
		}
	})
}

// ---------------------------------------------------------------------------
// StatSetHandler
// ---------------------------------------------------------------------------

func TestStatSetHandler_PublishUpdatesLiveRow(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	// Insert directly because the service has no Create helper for stat
	// sets yet (Phase 3 territory) — mirrors how a designer's first
	// "create" lands a row before drafts can ever update it.
	var rowID int64
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO character_stat_sets (key, name, created_by) VALUES ($1, $2, $3) RETURNING id
	`, "default", "Default", f.designerID).Scan(&rowID); err != nil {
		t.Fatalf("seed: %v", err)
	}

	draft := characters.StatSetDraft{Key: "default", Name: "Default (renamed)"}
	js, _ := json.Marshal(draft)
	h := characters.NewStatSetHandler(f.svc)

	withTx(t, f, func(tx pgx.Tx) {
		_, err := h.Publish(ctx, tx, artifact.DraftRow{
			ArtifactKind: artifact.KindCharacterStatSet, ArtifactID: rowID, DraftJSON: js,
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM character_stat_sets WHERE id = $1`, rowID).Scan(&name); err != nil {
			t.Fatalf("readback: %v", err)
		}
		if name != "Default (renamed)" {
			t.Errorf("name after publish = %q", name)
		}
	})
}

// ---------------------------------------------------------------------------
// TalentTreeHandler — node replacement
// ---------------------------------------------------------------------------

func TestTalentTreeHandler_PublishReplacesNodes(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	// Seed tree + 1 stale node directly.
	var treeID int64
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO character_talent_trees (key, name, currency_key, layout_mode, created_by)
		VALUES ($1, $2, $3, $4, $5) RETURNING id
	`, "warrior", "Warrior", "talent_points", "tree", f.designerID).Scan(&treeID); err != nil {
		t.Fatalf("seed tree: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO character_talent_nodes (tree_id, key, name, max_rank) VALUES ($1, $2, $3, $4)
	`, treeID, "stale", "Stale", 1); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	draft := characters.TalentTreeDraft{
		Key: "warrior", Name: "Warrior (v2)", CurrencyKey: "talent_points",
		LayoutMode: characters.LayoutTree,
		Nodes: []characters.TalentNodeDraft{
			{Key: "cleave", Name: "Cleave", MaxRank: 1},
			{Key: "shield_bash", Name: "Shield Bash", MaxRank: 3, MutexGroup: "weapon"},
		},
	}
	js, _ := json.Marshal(draft)
	h := characters.NewTalentTreeHandler(f.svc)

	withTx(t, f, func(tx pgx.Tx) {
		_, err := h.Publish(ctx, tx, artifact.DraftRow{
			ArtifactKind: artifact.KindCharacterTalentTree, ArtifactID: treeID, DraftJSON: js,
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		// Stale node gone, two new nodes present.
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM character_talent_nodes WHERE tree_id = $1`, treeID).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 2 {
			t.Errorf("node count after publish = %d, want 2", n)
		}
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM character_talent_trees WHERE id = $1`, treeID).Scan(&name); err != nil {
			t.Fatalf("readback: %v", err)
		}
		if name != "Warrior (v2)" {
			t.Errorf("name after publish = %q", name)
		}
	})
}

func TestTalentTreeHandler_RejectsDuplicateNodeKeys(t *testing.T) {
	h := characters.NewTalentTreeHandler(nil)
	draft := characters.TalentTreeDraft{
		Key: "k", Name: "n", CurrencyKey: "tp", LayoutMode: characters.LayoutTree,
		Nodes: []characters.TalentNodeDraft{
			{Key: "dup", Name: "A", MaxRank: 1},
			{Key: "dup", Name: "B", MaxRank: 1},
		},
	}
	js, _ := json.Marshal(draft)
	err := h.Validate(context.Background(), artifact.DraftRow{
		ArtifactKind: artifact.KindCharacterTalentTree, DraftJSON: js,
	})
	if err == nil {
		t.Errorf("expected duplicate-key error, got nil")
	}
}

// ---------------------------------------------------------------------------
// NpcTemplateHandler
// ---------------------------------------------------------------------------

func TestNpcTemplateHandler_PublishUpdatesLiveRow(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	row, err := f.svc.CreateNpcTemplate(ctx, characters.CreateNpcTemplateInput{
		Name: "Goblin", CreatedBy: f.designerID,
	})
	if err != nil {
		t.Fatalf("CreateNpcTemplate: %v", err)
	}

	draft := characters.NpcTemplateDraft{Name: "Goblin (chief)", Tags: []string{"npc", "boss"}}
	js, _ := json.Marshal(draft)
	h := characters.NewNpcTemplateHandler(f.svc)

	withTx(t, f, func(tx pgx.Tx) {
		_, err := h.Publish(ctx, tx, artifact.DraftRow{
			ArtifactKind: artifact.KindNpcTemplate, ArtifactID: row.ID, DraftJSON: js,
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		var name string
		var tags []string
		if err := tx.QueryRow(ctx, `SELECT name, tags FROM npc_templates WHERE id = $1`, row.ID).Scan(&name, &tags); err != nil {
			t.Fatalf("readback: %v", err)
		}
		if name != "Goblin (chief)" {
			t.Errorf("name = %q", name)
		}
		if len(tags) != 2 || tags[0] != "npc" || tags[1] != "boss" {
			t.Errorf("tags = %v", tags)
		}
	})
}

// ---------------------------------------------------------------------------
// Pipeline integration: register all handlers and run a multi-kind preview.
// ---------------------------------------------------------------------------

func TestPipeline_AcceptsAllCharacterKinds(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	registry := artifact.NewRegistry()
	registry.Register(characters.NewSlotHandler(f.svc))
	registry.Register(characters.NewPartHandler(f.svc))
	registry.Register(characters.NewStatSetHandler(f.svc))
	registry.Register(characters.NewTalentTreeHandler(f.svc))
	registry.Register(characters.NewNpcTemplateHandler(f.svc))

	for _, k := range []artifact.Kind{
		artifact.KindCharacterSlot, artifact.KindCharacterPart,
		artifact.KindCharacterStatSet, artifact.KindCharacterTalentTree,
		artifact.KindNpcTemplate,
	} {
		if _, ok := registry.HandlerFor(k); !ok {
			t.Errorf("registry missing handler for %q", k)
		}
	}

	// Now drive a single slot draft through Pipeline.Preview to confirm
	// end-to-end wiring (validation + publish + rollback).
	pipe := artifact.NewPipeline(f.pool, registry)
	slots, _ := f.svc.ListSlots(ctx)
	body := slots[0]
	draft := characters.SlotDraft{
		Key: body.Key, Label: "Body (preview)", Required: body.Required,
		OrderIndex: body.OrderIndex, DefaultLayerOrder: body.DefaultLayerOrder,
		AllowsPalette: body.AllowsPalette,
	}
	js, _ := json.Marshal(draft)
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
		VALUES ($1, $2, $3, $4)
	`, "character_slot", body.ID, js, f.designerID); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	outs, err := pipe.Preview(ctx)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(outs))
	}
	if outs[0].Kind != artifact.KindCharacterSlot {
		t.Errorf("kind = %q", outs[0].Kind)
	}
	if outs[0].Op != artifact.OpUpdated {
		t.Errorf("op = %q", outs[0].Op)
	}
}
