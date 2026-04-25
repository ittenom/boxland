package assets_test

import (
	"context"
	"encoding/json"
	"testing"

	"boxland/server/internal/assets"
	"boxland/server/internal/publishing/artifact"
)

func TestAssetHandler_PublishUpdatesNameAndTags(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	svc := assets.New(pool)
	ctx := context.Background()

	// Create the live asset.
	live, err := svc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "old-name",
		ContentAddressedPath: "p", OriginalFormat: "png",
		Tags: []string{"old"}, CreatedBy: designerID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Build a draft, drop it into the drafts table, run the publish pipeline.
	draft := assets.AssetDraft{
		Name: "new-name",
		Tags: []string{"shiny", "boss"},
	}
	body, _ := json.Marshal(draft)
	if _, err := pool.Exec(ctx,
		`INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by) VALUES ($1, $2, $3, $4)`,
		string(artifact.KindAsset), live.ID, body, designerID,
	); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	registry := artifact.NewRegistry()
	registry.Register(assets.NewHandler(svc))
	pipe := artifact.NewPipeline(pool, registry)

	outcomes, err := pipe.Run(ctx, designerID)
	if err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Op != artifact.OpUpdated {
		t.Fatalf("expected one updated outcome, got %+v", outcomes)
	}

	// The asset row should now reflect the draft.
	got, err := svc.FindByID(ctx, live.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "new-name" {
		t.Errorf("name not updated: %q", got.Name)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "shiny" || got.Tags[1] != "boss" {
		t.Errorf("tags not updated: %v", got.Tags)
	}

	// And the publish_diffs table should have one row with a sensible
	// summary line.
	var summary string
	if err := pool.QueryRow(ctx,
		`SELECT summary_line FROM publish_diffs WHERE artifact_id = $1 LIMIT 1`,
		live.ID,
	).Scan(&summary); err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if summary == "" {
		t.Error("expected non-empty summary line")
	}
}

func TestAssetHandler_ValidateRejectsEmptyName(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)

	h := assets.NewHandler(assets.New(pool))
	body, _ := json.Marshal(assets.AssetDraft{Name: "", Tags: []string{"x"}})
	err := h.Validate(context.Background(), artifact.DraftRow{
		ArtifactKind: artifact.KindAsset,
		ArtifactID:   1,
		DraftJSON:    body,
	})
	if err == nil {
		t.Fatal("expected validation error for empty name")
	}
}

func TestAssetHandler_ValidateRejectsEmptyTag(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)

	h := assets.NewHandler(assets.New(pool))
	body, _ := json.Marshal(assets.AssetDraft{Name: "x", Tags: []string{"good", ""}})
	if err := h.Validate(context.Background(), artifact.DraftRow{
		ArtifactKind: artifact.KindAsset,
		DraftJSON:    body,
	}); err == nil {
		t.Fatal("expected validation error for empty tag")
	}
}

func TestAssetHandler_PublishMissingAssetReturnsNotFound(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := assets.New(pool)

	registry := artifact.NewRegistry()
	registry.Register(assets.NewHandler(svc))
	pipe := artifact.NewPipeline(pool, registry)

	body, _ := json.Marshal(assets.AssetDraft{Name: "ghost", Tags: nil})
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by) VALUES ($1, $2, $3, 0)`,
		string(artifact.KindAsset), int64(99999), body,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := pipe.Run(context.Background(), 0); err == nil {
		t.Error("expected error for draft pointing at non-existent asset")
	}
}
