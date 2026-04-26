// Boxland — designer/characters_handlers smoke tests.
//
// Confirms the dashboard renders, the create endpoints land rows
// through the service, and the draft endpoint upserts into `drafts`.
// These mirror the existing designer handler test patterns; the
// authedReq helper lives in assets_handlers_test.go.

package designer_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/characters"
	designerhandlers "boxland/server/internal/designer"
)

// charactersDeps wires only what the character handlers need so the
// other Deps fields stay nil — keeps the test surface tight.
func charactersDeps(t *testing.T) (designerhandlers.Deps, *characters.Service, int64, string) {
	t.Helper()
	pool := openTestPool(t)
	t.Cleanup(pool.Close)

	auth := authdesigner.New(pool)
	d, err := auth.CreateDesigner(context.Background(), "char-handler@x.com", "password-12", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	tok, err := auth.OpenSession(context.Background(), d.ID, "ua", nil)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	svc := characters.New(pool)
	deps := designerhandlers.Deps{
		Auth:       auth,
		Characters: svc,
	}
	return deps, svc, d.ID, tok
}

// formBody renders form values as an io.Reader for use with the shared
// authedReq helper.
func formBody(form url.Values) io.Reader {
	return strings.NewReader(form.Encode())
}

// authedFormReq is the form-bearing variant of the shared authedReq;
// adds the Content-Type header that ParseForm requires.
func authedFormReq(method, path, tok string, form url.Values) *http.Request {
	req := authedReq(method, path, tok, formBody(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestCharactersDashboard_RendersWhenEmpty(t *testing.T) {
	deps, _, _, tok := charactersDeps(t)
	srv := buildHandler(deps)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq("GET", "/design/characters", tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, trim(rr.Body.String(), 400))
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Characters") {
		t.Errorf("body missing 'Characters' heading; got: %q", trim(body, 200))
	}
	if !strings.Contains(body, "NPC templates") {
		t.Errorf("body missing 'NPC templates' card; got: %q", trim(body, 200))
	}
}

func TestCharactersDashboard_ShowsRecentNpcTemplates(t *testing.T) {
	deps, svc, designerID, tok := charactersDeps(t)
	srv := buildHandler(deps)

	if _, err := svc.CreateNpcTemplate(context.Background(), characters.CreateNpcTemplateInput{
		Name: "Goblin", CreatedBy: designerID,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq("GET", "/design/characters", tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Goblin") {
		t.Errorf("dashboard missing recent NPC; body excerpt: %q", trim(rr.Body.String(), 400))
	}
}

func TestPostNpcTemplate_CreatesRow(t *testing.T) {
	deps, svc, _, tok := charactersDeps(t)
	srv := buildHandler(deps)

	form := url.Values{}
	form.Set("name", "Wolf")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedFormReq("POST", "/design/characters/npc-templates", tok, form))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, trim(rr.Body.String(), 400))
	}

	got, err := svc.ListNpcTemplates(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Wolf" {
		t.Errorf("got %+v", got)
	}
}

func TestPostNpcTemplate_RejectsDuplicateName(t *testing.T) {
	deps, svc, designerID, tok := charactersDeps(t)
	srv := buildHandler(deps)

	if _, err := svc.CreateNpcTemplate(context.Background(), characters.CreateNpcTemplateInput{
		Name: "Goblin", CreatedBy: designerID,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	form := url.Values{}
	form.Set("name", "Goblin")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedFormReq("POST", "/design/characters/npc-templates", tok, form))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, trim(rr.Body.String(), 200))
	}
}

// authedJSONReq builds a JSON-bearing POST/GET — used by the recipe
// endpoints which take JSON bodies (vs the form-bearing draft endpoints).
func authedJSONReq(method, path, tok string, body []byte) *http.Request {
	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	}
	req := authedReq(method, path, tok, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestGetCharacterCatalog_ReturnsSlotsAndParts(t *testing.T) {
	deps, svc, designerID, tok := charactersDeps(t)
	srv := buildHandler(deps)
	ctx := context.Background()

	// Seed a sprite asset + a part on the body slot so the catalog has
	// at least one part to return.
	assetSvc := assets.New(svc.Pool)
	a, err := assetSvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "src",
		ContentAddressedPath: "tests/sprite", OriginalFormat: "png",
		CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	slots, _ := svc.ListSlots(ctx)
	if _, err := svc.CreatePart(ctx, characters.CreatePartInput{
		SlotID: slots[0].ID, AssetID: a.ID, Name: "Test body",
		CreatedBy: designerID,
	}); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq("GET", "/design/characters/catalog", tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, trim(rr.Body.String(), 400))
	}
	var resp struct {
		Slots []struct {
			Key   string `json:"key"`
			Parts []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"parts"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, trim(rr.Body.String(), 400))
	}
	if len(resp.Slots) != 24 {
		t.Errorf("slots = %d, want 24 (the seeded vocabulary)", len(resp.Slots))
	}
	// body is the first slot in OrderIndex; it should carry the seeded part.
	if resp.Slots[0].Key != "body" {
		t.Errorf("first slot = %q, want body", resp.Slots[0].Key)
	}
	if len(resp.Slots[0].Parts) != 1 || resp.Slots[0].Parts[0].Name != "Test body" {
		t.Errorf("body parts = %+v", resp.Slots[0].Parts)
	}
}

func TestPostCharacterRecipe_CreatesAndRoundtrips(t *testing.T) {
	deps, svc, designerID, tok := charactersDeps(t)
	srv := buildHandler(deps)
	ctx := context.Background()

	// Seed a part so the recipe references a real id.
	assetSvc := assets.New(svc.Pool)
	a, _ := assetSvc.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "x", ContentAddressedPath: "x", OriginalFormat: "png",
		CreatedBy: designerID,
	})
	slots, _ := svc.ListSlots(ctx)
	part, _ := svc.CreatePart(ctx, characters.CreatePartInput{
		SlotID: slots[0].ID, AssetID: a.ID, Name: "test", CreatedBy: designerID,
	})

	body, _ := json.Marshal(map[string]any{
		"name": "My recipe",
		"appearance": map[string]any{
			"slots": []map[string]any{
				{"slot_key": "body", "part_id": part.ID},
			},
		},
	})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedJSONReq("POST", "/design/characters/recipes", tok, body))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", rr.Code, trim(rr.Body.String(), 400))
	}
	var created struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.ID == 0 || created.Name != "My recipe" {
		t.Errorf("response = %+v", created)
	}

	// GET the same recipe — designer-mode endpoint returns the payload.
	rr2 := httptest.NewRecorder()
	srv.ServeHTTP(rr2, authedReq("GET", "/design/characters/recipes/"+intStr(created.ID), tok, nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", rr2.Code, trim(rr2.Body.String(), 400))
	}
	if !strings.Contains(rr2.Body.String(), "My recipe") {
		t.Errorf("get body missing name; got %q", trim(rr2.Body.String(), 400))
	}
}

func TestUpdateCharacterRecipe_RejectsCrossDesigner(t *testing.T) {
	deps, svc, designerID, tok := charactersDeps(t)
	srv := buildHandler(deps)
	ctx := context.Background()

	// Seed a recipe owned by a *different* designer (id designerID+999).
	// Direct DB insert keeps the test focused on the cross-owner check.
	hash, _ := characters.ComputeRecipeHash("Other", nil, nil, nil)
	otherDesignerID := designerID + 999

	// Create the other designer first so the foreign key holds.
	auth := authdesigner.New(svc.Pool)
	other, err := auth.CreateDesigner(ctx, "other@x.com", "password-12", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create other designer: %v", err)
	}
	otherDesignerID = other.ID

	var recipeID int64
	if err := svc.Pool.QueryRow(ctx, `
		INSERT INTO character_recipes (owner_kind, owner_id, name, recipe_hash, created_by)
		VALUES ('designer', $1, 'Other', $2, $1) RETURNING id
	`, otherDesignerID, hash).Scan(&recipeID); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"name":       "Stolen",
		"appearance": map[string]any{"slots": []any{}},
	})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedJSONReq("POST", "/design/characters/recipes/"+intStr(recipeID), tok, body))
	// Mapped to 404 to avoid leaking existence.
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", rr.Code, trim(rr.Body.String(), 400))
	}
}

func TestPostAttachRecipe_LinksTemplate(t *testing.T) {
	deps, svc, designerID, tok := charactersDeps(t)
	srv := buildHandler(deps)
	ctx := context.Background()

	tmpl, err := svc.CreateNpcTemplate(ctx, characters.CreateNpcTemplateInput{
		Name: "Goblin", CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	recipe, err := svc.CreateRecipe(ctx, characters.CreateRecipeInput{
		OwnerKind: characters.OwnerKindDesigner, OwnerID: designerID,
		Name: "R", CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("create recipe: %v", err)
	}

	body, _ := json.Marshal(map[string]any{"recipe_id": recipe.ID})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedJSONReq("POST",
		"/design/characters/npc-templates/"+intStr(tmpl.ID)+"/attach-recipe", tok, body))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rr.Code, trim(rr.Body.String(), 400))
	}

	got, _ := svc.FindNpcTemplateByID(ctx, tmpl.ID)
	if got.RecipeID == nil || *got.RecipeID != recipe.ID {
		t.Errorf("recipe_id not linked: got %v", got.RecipeID)
	}
}

func TestPostCharacterSlotDraft_UpsertsIntoDrafts(t *testing.T) {
	deps, svc, _, tok := charactersDeps(t)
	srv := buildHandler(deps)
	ctx := context.Background()

	// Pick the seeded `body` slot to draft against.
	slots, _ := svc.ListSlots(ctx)
	body := slots[0]

	form := url.Values{}
	form.Set("key", body.Key)
	form.Set("label", "Body (renamed)")
	form.Set("required", "on")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedFormReq("POST", "/design/characters/slots/"+intStr(body.ID)+"/draft", tok, form))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, trim(rr.Body.String(), 400))
	}

	// Verify the draft row exists and is keyed by character_slot.
	var n int
	if err := svc.Pool.QueryRow(ctx, `
		SELECT count(*) FROM drafts
		WHERE artifact_kind = 'character_slot' AND artifact_id = $1
	`, body.ID).Scan(&n); err != nil {
		t.Fatalf("count drafts: %v", err)
	}
	if n != 1 {
		t.Errorf("draft count = %d, want 1", n)
	}
}

// trim returns the first n chars of s, with an ellipsis if truncated.
func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// intStr formats an int64 as decimal — local helper so the test file
// doesn't depend on strconv.
func intStr(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	out := ""
	for n > 0 {
		d := byte(n % 10)
		out = string(rune('0'+d)) + out
		n /= 10
	}
	if neg {
		out = "-" + out
	}
	return out
}
