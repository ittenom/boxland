package ws_test

import (
	"context"
	"encoding/json"
	"testing"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/ws"
)

// TestBuildEditorTheme_BindsKnownRoles seeds a couple of ClassUI
// entity_types whose names match canonical role aliases and asserts
// the builder binds them with the right URL + insets + dims.
func TestBuildEditorTheme_BindsKnownRoles(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	ctx := context.Background()

	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(ctx, "theme-test@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	as := assets.New(pool)
	es := entities.New(pool, components.Default())

	// Seed a minimal theme: one frame asset + matching entity_type
	// + nine_slice component, with the canonical name the role
	// table expects.
	md, _ := json.Marshal(map[string]any{"width": 24, "height": 24, "source": "crusenho-gradient"})
	a, err := as.Create(ctx, assets.CreateInput{
		Kind:                 assets.KindUIPanel,
		Name:                 "ui_gradient_frame_standard",
		ContentAddressedPath: "ui-pack/aa/bb/standard",
		OriginalFormat:       "png",
		MetadataJSON:         md,
		CreatedBy:            d.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := a.ID
	et, err := es.Create(ctx, entities.CreateInput{
		Name:          "ui_gradient_frame_standard",
		EntityClass:   entities.ClassUI,
		SpriteAssetID: &id,
		CreatedBy:     d.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(components.NineSlice{Left: 8, Top: 8, Right: 8, Bottom: 8})
	if err := es.SetComponents(ctx, nil, et.ID, map[components.Kind]json.RawMessage{
		components.KindNineSlice: cfg,
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := ws.BuildEditorTheme(ctx, es, as)
	if err != nil {
		t.Fatalf("BuildEditorTheme: %v", err)
	}
	var found *ws.EditorThemeEntry
	for i := range entries {
		if entries[i].Role == "frame_standard" {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("frame_standard role not bound; got %+v", entries)
	}
	if found.EntityTypeID != et.ID {
		t.Errorf("entity_type_id mismatch: got %d want %d", found.EntityTypeID, et.ID)
	}
	if found.AssetURL == "" || found.AssetURL[0] != '/' {
		t.Errorf("asset_url malformed: %q", found.AssetURL)
	}
	if found.NineSlice.Left != 8 || found.NineSlice.Top != 8 ||
		found.NineSlice.Right != 8 || found.NineSlice.Bottom != 8 {
		t.Errorf("insets wrong: %+v", found.NineSlice)
	}
	if found.Width != 24 || found.Height != 24 {
		t.Errorf("dims wrong: %dx%d", found.Width, found.Height)
	}
}

// TestBuildEditorTheme_OmitsMissingRoles verifies the builder
// returns an entry only for roles whose canonical name actually
// exists in the DB. Unbound roles silently disappear from the
// output (the editor degrades gracefully via the placeholder fill).
func TestBuildEditorTheme_OmitsMissingRoles(t *testing.T) {
	pool := openPool(t)
	defer pool.Close()
	ctx := context.Background()
	auth := authdesigner.New(pool)
	_, err := auth.CreateDesigner(ctx, "theme-omit@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	as := assets.New(pool)
	es := entities.New(pool, components.Default())
	// No UI entities seeded.
	entries, err := ws.BuildEditorTheme(ctx, es, as)
	if err != nil {
		t.Fatalf("BuildEditorTheme: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty theme, got %d entries", len(entries))
	}
}
