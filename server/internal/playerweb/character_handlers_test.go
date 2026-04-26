// Boxland — playerweb character endpoint tests.
//
// Focus: tenant isolation. Every save / load endpoint MUST scope by
// the authenticated player_id, never by request-body fields. These
// tests pin that invariant by trying to read / write across players
// and asserting 404 (we map cross-player to 404 to avoid leaking
// existence).

package playerweb_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	"boxland/server/internal/auth/csrf"
	authdesigner "boxland/server/internal/auth/designer"
	authplayer "boxland/server/internal/auth/player"
	"boxland/server/internal/characters"
	"boxland/server/internal/persistence"
	"boxland/server/internal/playerweb"
)

// charactersFixture wires the playerweb mux with a Characters service
// hooked up to MinIO + Postgres. Each test gets its own DB.
type charactersFixture struct {
	t          *testing.T
	pool       *pgxpool.Pool
	srv        *httptest.Server
	authP      *authplayer.Service
	store      *persistence.ObjectStore
	chars      *characters.Service
	designerID int64
}

// newCharactersFixture builds a player-mode fixture with the Characters
// service wired in. Skips when MinIO is unreachable.
func newCharactersFixture(t *testing.T) *charactersFixture {
	t.Helper()
	pool := openPool(t)
	t.Cleanup(pool.Close)

	cfg := persistence.ObjectStoreConfig{
		Endpoint:        "http://localhost:9000",
		Region:          "us-east-1",
		Bucket:          "boxland-assets",
		AccessKeyID:     "boxland",
		SecretAccessKey: "boxland_dev_secret",
		UsePathStyle:    true,
		PublicBaseURL:   "http://localhost:9000/boxland-assets",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := persistence.NewObjectStore(ctx, cfg)
	if err != nil {
		t.Skipf("minio unavailable: %v", err)
	}

	authP := authplayer.New(pool, []byte("test-jwt-secret-32-bytes-padded__"))
	assetSvc := assets.New(pool)
	charSvc := characters.New(pool)
	charSvc.SetBakeDeps(store, assetSvc)

	authD := authdesigner.New(pool)
	d, err := authD.CreateDesigner(context.Background(), "system@x.com", "password-12", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	charSvc.SetSystemDesignerID(d.ID)

	deps := playerweb.Deps{
		Auth:          authP,
		Assets:        assetSvc,
		ObjectStore:   store,
		Characters:    charSvc,
		SecureCookies: false,
	}
	csrfMW := csrf.Middleware(csrf.Config{Secure: false, SameSite: http.SameSiteStrictMode})
	loadMW := playerweb.LoadSession(deps)
	srv := httptest.NewServer(csrfMW(loadMW(playerweb.New(deps))))
	t.Cleanup(srv.Close)

	return &charactersFixture{
		t: t, pool: pool, srv: srv, authP: authP,
		store: store, chars: charSvc, designerID: d.ID,
	}
}

// makePlayer creates an authenticated player and returns its id + a
// cookie jar / CSRF token primed for subsequent requests against
// f.srv. Uses noRedirectClient so the post-login 303 doesn't follow
// to /play/maps and trip on missing services.
func (f *charactersFixture) makePlayer(t *testing.T, email string) (playerID int64, client *http.Client, csrfTok string) {
	t.Helper()
	p, err := f.authP.CreatePlayer(context.Background(), email, "pw-secret-1234")
	if err != nil {
		t.Fatalf("create player: %v", err)
	}
	// Bypass email verification — the test environment doesn't run
	// the verification flow. Real flows go through IssueEmailVerification
	// + the verification handler.
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE players SET email_verified = true WHERE id = $1`, p.ID,
	); err != nil {
		t.Fatalf("verify email: %v", err)
	}

	jar := newJar()
	client = noRedirectClient(jar)
	csrfTok = primeCSRF(t, client, f.srv.URL)
	// Log in so subsequent requests carry the player session cookie.
	resp := postForm(t, client, f.srv.URL+"/play/login", csrfTok, url.Values{
		"email":    {email},
		"password": {"pw-secret-1234"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: status = %d, want 303", resp.StatusCode)
	}
	return p.ID, client, csrfTok
}

// uploadAndRegisterPart uploads a tiny PNG and creates a character_part
// row referencing it. Returns the new part.
func (f *charactersFixture) uploadAndRegisterPart(t *testing.T, slotID int64, name string, c color.NRGBA) *characters.Part {
	t.Helper()
	// Build a 32x32 single-color PNG.
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	body := buf.Bytes()
	key := persistence.ContentAddressedKey("assets", body)
	if err := f.store.Put(context.Background(), key, "image/png", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("put: %v", err)
	}

	asvc := assets.New(f.pool)
	asset, err := asvc.Create(context.Background(), assets.CreateInput{
		Kind:                 assets.KindSprite,
		Name:                 "src-" + name,
		ContentAddressedPath: key,
		OriginalFormat:       "png",
		MetadataJSON:         []byte(`{"grid_w":32,"grid_h":32,"cols":1,"rows":1,"frame_count":1}`),
		CreatedBy:            f.designerID,
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	part, err := f.chars.CreatePart(context.Background(), characters.CreatePartInput{
		SlotID: slotID, AssetID: asset.ID, Name: name,
		FrameMapJSON: []byte(`{"idle":[0,0]}`),
		CreatedBy:    f.designerID,
	})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	return part
}

// ---------------------------------------------------------------------------
// Catalog endpoint
// ---------------------------------------------------------------------------

func TestPlayCharacterCatalog_RequiresAuth(t *testing.T) {
	f := newCharactersFixture(t)
	c := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.Get(f.srv.URL + "/play/character-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// RequirePlayer redirects unauthenticated browsers to /play/login.
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Errorf("unauthed: status = %d, want 302/303", resp.StatusCode)
	}
}

func TestPlayCharacterCatalog_ReturnsSlotsAndParts(t *testing.T) {
	f := newCharactersFixture(t)
	_, client, _ := f.makePlayer(t, "p1@x.com")

	slots, _ := f.chars.ListSlots(context.Background())
	body := f.uploadAndRegisterPart(t, slots[0].ID, "body-a", color.NRGBA{255, 0, 0, 255})

	resp, err := client.Get(f.srv.URL + "/play/character-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, raw)
	}
	var got struct {
		Slots []struct {
			Key   string `json:"key"`
			Parts []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"parts"`
		} `json:"slots"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Slots) != 24 {
		t.Errorf("slots = %d, want 24", len(got.Slots))
	}
	// body slot should carry the seeded part.
	if got.Slots[0].Key != "body" {
		t.Errorf("first slot = %q, want body", got.Slots[0].Key)
	}
	if len(got.Slots[0].Parts) != 1 || got.Slots[0].Parts[0].ID != body.ID {
		t.Errorf("body parts: %+v", got.Slots[0].Parts)
	}
}

// ---------------------------------------------------------------------------
// Cross-player isolation
// ---------------------------------------------------------------------------

func TestPlayerCharacter_CrossPlayerGet_404(t *testing.T) {
	f := newCharactersFixture(t)
	playerA, _, _ := f.makePlayer(t, "playera@x.com")

	// Insert a character belonging to A directly via SQL — keeps the
	// test focused on the cross-player guard.
	var charID int64
	if err := f.pool.QueryRow(context.Background(), `
		INSERT INTO player_characters (player_id, name) VALUES ($1, 'Aria') RETURNING id
	`, playerA).Scan(&charID); err != nil {
		t.Fatal(err)
	}

	// Player B logs in and tries to fetch A's character.
	_, clientB, _ := f.makePlayer(t, "playerb@x.com")
	resp, err := clientB.Get(f.srv.URL + "/play/characters/" + intToStr(charID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Errorf("cross-player GET: status = %d (want 404), body=%s", resp.StatusCode, raw)
	}
}

func TestPlayerCharacter_CrossPlayerUpdate_404(t *testing.T) {
	f := newCharactersFixture(t)
	playerA, _, _ := f.makePlayer(t, "playera@x.com")

	var charID int64
	if err := f.pool.QueryRow(context.Background(), `
		INSERT INTO player_characters (player_id, name) VALUES ($1, 'Aria') RETURNING id
	`, playerA).Scan(&charID); err != nil {
		t.Fatal(err)
	}

	_, clientB, csrfB := f.makePlayer(t, "playerb@x.com")
	body := strings.NewReader(`{"name":"Stolen","appearance":{"slots":[]},"stats":{"set_id":0,"allocations":{}},"talents":{"picks":{}}}`)
	req, _ := http.NewRequest("POST", f.srv.URL+"/play/characters/"+intToStr(charID), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrf.HeaderName, csrfB)
	resp, err := clientB.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Errorf("cross-player POST: status = %d (want 404), body=%s", resp.StatusCode, raw)
	}

	// The character row is untouched.
	var nm string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT name FROM player_characters WHERE id = $1`, charID,
	).Scan(&nm); err != nil {
		t.Fatal(err)
	}
	if nm != "Aria" {
		t.Errorf("character name leaked through: got %q, want Aria", nm)
	}
}

func TestPlayerCharacter_BodyPlayerIDIgnored(t *testing.T) {
	// Defense-in-depth: even if the body says player_id=<other>, the
	// server must scope by the authenticated session's player id.
	f := newCharactersFixture(t)
	playerA, clientA, csrfA := f.makePlayer(t, "playera@x.com")
	playerB, _, _ := f.makePlayer(t, "playerb@x.com")
	_ = playerB // existence proves the spoofed id is real but shouldn't matter

	// Seed a registered part so the recipe validates + bakes cleanly.
	slots, _ := f.chars.ListSlots(context.Background())
	body := f.uploadAndRegisterPart(t, slots[0].ID, "body-spoof", color.NRGBA{255, 0, 0, 255})

	payload := map[string]any{
		"player_id":  playerB,    // spoofed; should be ignored
		"name":       "AriaA",
		"appearance": map[string]any{"slots": []map[string]any{{"slot_key": "body", "part_id": body.ID}}},
		"stats":      map[string]any{"set_id": 0, "allocations": map[string]int{}},
		"talents":    map[string]any{"picks": map[string]int{}},
	}
	js, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", f.srv.URL+"/play/characters", bytes.NewReader(js))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrf.HeaderName, csrfA)
	resp, err := clientA.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, raw)
	}

	// The new character belongs to A, not the spoofed body id.
	var ownerID int64
	if err := f.pool.QueryRow(context.Background(),
		`SELECT player_id FROM player_characters WHERE name = 'AriaA'`,
	).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if ownerID != playerA {
		t.Errorf("character owner = %d, want %d (body player_id was ignored)", ownerID, playerA)
	}
	// And the recipe is owned by playerA, not playerB.
	var recipeOwner int64
	if err := f.pool.QueryRow(context.Background(),
		`SELECT owner_id FROM character_recipes WHERE name = 'AriaA' AND owner_kind = 'player'`,
	).Scan(&recipeOwner); err != nil {
		t.Fatal(err)
	}
	if recipeOwner != playerA {
		t.Errorf("recipe owner = %d, want %d", recipeOwner, playerA)
	}
}

// ---------------------------------------------------------------------------
// Save + bake end-to-end
// ---------------------------------------------------------------------------

func TestPlayerCharacter_CreateSavesAndBakes(t *testing.T) {
	f := newCharactersFixture(t)
	_, client, csrfTok := f.makePlayer(t, "p@x.com")

	slots, _ := f.chars.ListSlots(context.Background())
	body := f.uploadAndRegisterPart(t, slots[0].ID, "body-a", color.NRGBA{255, 0, 0, 255})

	payload := map[string]any{
		"name": "Aria",
		"appearance": map[string]any{
			"slots": []map[string]any{{"slot_key": "body", "part_id": body.ID}},
		},
		"stats":   map[string]any{"set_id": 0, "allocations": map[string]int{}},
		"talents": map[string]any{"picks": map[string]int{}},
	}
	js, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", f.srv.URL+"/play/characters", bytes.NewReader(js))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrf.HeaderName, csrfTok)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, raw)
	}

	var saved struct {
		ID          int64 `json:"id"`
		BakeID      int64 `json:"bake_id"`
		BakeAssetID int64 `json:"bake_asset_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID == 0 || saved.BakeID == 0 || saved.BakeAssetID == 0 {
		t.Errorf("saved response missing ids: %+v", saved)
	}

	// The new player_character row is linked to the bake.
	var bakeID int64
	if err := f.pool.QueryRow(context.Background(),
		`SELECT active_bake_id FROM player_characters WHERE id = $1`, saved.ID,
	).Scan(&bakeID); err != nil {
		t.Fatal(err)
	}
	if bakeID != saved.BakeID {
		t.Errorf("active_bake_id mismatch: got %d, want %d", bakeID, saved.BakeID)
	}

	// The bake row is in 'baked' status with the right asset_id.
	var status string
	var assetID int64
	if err := f.pool.QueryRow(context.Background(),
		`SELECT status, asset_id FROM character_bakes WHERE id = $1`, bakeID,
	).Scan(&status, &assetID); err != nil {
		t.Fatal(err)
	}
	if status != "baked" || assetID != saved.BakeAssetID {
		t.Errorf("bake row: status=%q asset_id=%d (want baked, %d)", status, assetID, saved.BakeAssetID)
	}
}

func TestPlayerCharacter_BakeFailureRollsBackShell(t *testing.T) {
	// Regression test for Phase 5.0 risk #4: a save whose bake errors
	// out must NOT leave an orphan player_characters row. Trigger by
	// posting a recipe with a part that doesn't exist — RunBake will
	// reject "source asset not found", and the shell + recipe should
	// roll back together.
	f := newCharactersFixture(t)
	playerID, client, csrfTok := f.makePlayer(t, "p@x.com")

	// Reference part_id 999999 which doesn't exist; the bake's
	// LoadBakeRecipe step will fail with "recipe references unknown
	// part id".
	payload := map[string]any{
		"name": "Ghost",
		"appearance": map[string]any{
			"slots": []map[string]any{{"slot_key": "body", "part_id": 999999}},
		},
		"stats":   map[string]any{"set_id": 0, "allocations": map[string]int{}},
		"talents": map[string]any{"picks": map[string]int{}},
	}
	js, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", f.srv.URL+"/play/characters", bytes.NewReader(js))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrf.HeaderName, csrfTok)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, raw)
	}

	// Critical assertion: NO player_characters row was created for
	// the player as a side-effect of the failed save.
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM player_characters WHERE player_id = $1`, playerID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 player_characters after failed save, got %d (orphan row leaked!)", n)
	}

	// And no recipe row leaked either.
	var r int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM character_recipes WHERE owner_kind = 'player' AND owner_id = $1`, playerID,
	).Scan(&r); err != nil {
		t.Fatal(err)
	}
	if r != 0 {
		t.Errorf("expected 0 recipes after failed save, got %d (orphan recipe leaked!)", r)
	}
}

func TestPlayerCharacter_PostWithoutCSRF_403(t *testing.T) {
	// Defense in depth: CSRF protection is global on the player mux,
	// but it's worth pinning the contract — JSON POSTs without the
	// X-CSRF-Token header MUST be rejected, even when the cookie is
	// present.
	f := newCharactersFixture(t)
	_, client, _ := f.makePlayer(t, "p@x.com")

	body := `{"name":"X","appearance":{"slots":[]},"stats":{"set_id":0,"allocations":{}},"talents":{"picks":{}}}`
	req, _ := http.NewRequest("POST", f.srv.URL+"/play/characters", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// NOTE: deliberately no X-CSRF-Token header.
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		raw, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 403; body=%s", resp.StatusCode, raw)
	}
}

func TestPlayerCharacter_EditLoad_RoundTripsAppearance(t *testing.T) {
	// Regression test for Phase 5.0 risk #24: player saves a character,
	// reloads the editor, sees their selections preserved.
	f := newCharactersFixture(t)
	_, client, csrfTok := f.makePlayer(t, "p@x.com")

	slots, _ := f.chars.ListSlots(context.Background())
	body := f.uploadAndRegisterPart(t, slots[0].ID, "body-rt", color.NRGBA{200, 100, 50, 255})

	// Save a character.
	payload := map[string]any{
		"name": "Bree",
		"appearance": map[string]any{
			"slots": []map[string]any{{"slot_key": "body", "part_id": body.ID}},
		},
		"stats":   map[string]any{"set_id": 0, "allocations": map[string]int{}},
		"talents": map[string]any{"picks": map[string]int{}},
	}
	js, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", f.srv.URL+"/play/characters", bytes.NewReader(js))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrf.HeaderName, csrfTok)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&saved)
	resp.Body.Close()
	if saved.ID == 0 {
		t.Fatalf("save returned id 0")
	}

	// GET the character — appearance must round-trip.
	resp2, err := client.Get(f.srv.URL + "/play/characters/" + intToStr(saved.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp2.Body)
		t.Fatalf("get: status = %d, body=%s", resp2.StatusCode, raw)
	}
	var got struct {
		Name       string `json:"name"`
		Appearance struct {
			Slots []struct {
				SlotKey string `json:"slot_key"`
				PartID  int64  `json:"part_id"`
			} `json:"slots"`
		} `json:"appearance"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Bree" {
		t.Errorf("name = %q, want Bree", got.Name)
	}
	if len(got.Appearance.Slots) != 1 || got.Appearance.Slots[0].SlotKey != "body" || got.Appearance.Slots[0].PartID != body.ID {
		t.Errorf("appearance not round-tripped: %+v", got.Appearance)
	}
}

func TestPlayerCharacter_EditPage_LoadsRecipeID(t *testing.T) {
	// Regression test: the edit page's data-bx-recipe-id attribute
	// must be the linked recipe id, not 0. The client uses this to
	// know whether to fetch the existing payload on boot.
	f := newCharactersFixture(t)
	_, client, csrfTok := f.makePlayer(t, "p@x.com")

	slots, _ := f.chars.ListSlots(context.Background())
	body := f.uploadAndRegisterPart(t, slots[0].ID, "body-page", color.NRGBA{0, 200, 100, 255})

	payload := map[string]any{
		"name":       "Cara",
		"appearance": map[string]any{"slots": []map[string]any{{"slot_key": "body", "part_id": body.ID}}},
		"stats":      map[string]any{"set_id": 0, "allocations": map[string]int{}},
		"talents":    map[string]any{"picks": map[string]int{}},
	}
	js, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", f.srv.URL+"/play/characters", bytes.NewReader(js))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrf.HeaderName, csrfTok)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		ID       int64 `json:"id"`
		RecipeID int64 `json:"recipe_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&saved)
	resp.Body.Close()
	if saved.ID == 0 || saved.RecipeID == 0 {
		t.Fatalf("save returned saved=%+v", saved)
	}

	// GET the edit page; the data-bx-recipe-id attribute should be
	// the linked recipe id.
	pageResp, err := client.Get(f.srv.URL + "/play/characters/" + intToStr(saved.ID) + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResp.Body.Close()
	if pageResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(pageResp.Body)
		t.Fatalf("edit page: status = %d, body=%s", pageResp.StatusCode, raw)
	}
	bodyBytes, _ := io.ReadAll(pageResp.Body)
	html := string(bodyBytes)
	wantAttr := `data-bx-recipe-id="` + intToStr(saved.RecipeID) + `"`
	if !strings.Contains(html, wantAttr) {
		t.Errorf("edit page missing %q attribute; head=%s", wantAttr, html[:min(400, len(html))])
	}
}

// min is in stdlib as of Go 1.21+.

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// intToStr stringifies an int64 in decimal. Local helper to avoid
// pulling strconv into the test path.
func intToStr(n int64) string {
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
