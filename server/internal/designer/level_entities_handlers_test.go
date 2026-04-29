package designer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	designerhandlers "boxland/server/internal/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
)

// levelEntitiesFixture creates a designer + a 10x10 map + a level
// referencing that map + one logic-class entity_type ("spawn") that
// can be placed. Returns the full Deps + auth token + level + entity
// type ids — the harness every level-entities handler test needs.
func levelEntitiesFixture(t *testing.T, pool *pgxpool.Pool) (deps designerhandlers.Deps, token string, levelID, entityTypeID, otherLevelID int64) {
	t.Helper()
	deps, designerID := fullDepsWithEntities(t, pool)
	deps.Levels = levels.New(pool)
	deps.Maps = mapsservice.New(pool)
	ctx := context.Background()

	m, err := deps.Maps.Create(ctx, mapsservice.CreateInput{Name: "lvl-map", Width: 10, Height: 10, CreatedBy: designerID})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	lv, err := deps.Levels.Create(ctx, levels.CreateInput{Name: "Town", MapID: m.ID, CreatedBy: designerID})
	if err != nil {
		t.Fatalf("create level: %v", err)
	}
	lv2, err := deps.Levels.Create(ctx, levels.CreateInput{Name: "Castle", MapID: m.ID, CreatedBy: designerID})
	if err != nil {
		t.Fatalf("create other level: %v", err)
	}
	et, err := deps.Entities.Create(ctx, entities.CreateInput{Name: "spawn", EntityClass: entities.ClassLogic, CreatedBy: designerID})
	if err != nil {
		t.Fatalf("create entity type: %v", err)
	}
	tok, err := deps.Auth.OpenSession(ctx, designerID, "ua", nil)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	return deps, tok, lv.ID, et.ID, lv2.ID
}

// resetDesignerEmail keeps fullDeps's hard-coded "asset-handler@x.com"
// from colliding when both this test file and the asset tests run in
// the same package. testdb gives every test a fresh DB so this isn't
// strictly required, but the helper lives here in case we want to
// pivot to a shared DB in the future.
func levelHandlerOK(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("status: got %d want %d, body=%s", rr.Code, want, rr.Body.String())
	}
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewReader(b)
}

func TestLevelEntities_RoundTrip(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, levelID, etID, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	// 1) List on empty level: 200 with empty array.
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/levels/"+itoa(levelID)+"/entities", tok, nil))
	levelHandlerOK(t, rr, http.StatusOK)
	var listResp struct {
		LevelID  int64             `json:"level_id"`
		Entities []json.RawMessage `json:"entities"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v body=%s", err, rr.Body.String())
	}
	if listResp.LevelID != levelID {
		t.Errorf("level_id: got %d want %d", listResp.LevelID, levelID)
	}
	if len(listResp.Entities) != 0 {
		t.Errorf("expected empty entities, got %d", len(listResp.Entities))
	}

	// 2) Place one.
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodPost, "/design/levels/"+itoa(levelID)+"/entities", tok,
		jsonBody(t, map[string]any{
			"entity_type_id":   etID,
			"x":                3,
			"y":                4,
			"rotation_degrees": 90,
		})))
	levelHandlerOK(t, rr, http.StatusCreated)
	var placed struct {
		Entity struct {
			ID              int64 `json:"id"`
			EntityTypeID    int64 `json:"entity_type_id"`
			X               int32 `json:"x"`
			Y               int32 `json:"y"`
			RotationDegrees int16 `json:"rotation_degrees"`
		} `json:"entity"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &placed); err != nil {
		t.Fatalf("decode place: %v body=%s", err, rr.Body.String())
	}
	if placed.Entity.ID == 0 {
		t.Fatalf("expected non-zero id, got %+v", placed.Entity)
	}
	if placed.Entity.X != 3 || placed.Entity.Y != 4 || placed.Entity.RotationDegrees != 90 {
		t.Errorf("placed wrong: %+v", placed.Entity)
	}
	eid := placed.Entity.ID

	// 3) PATCH (move).
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodPatch, "/design/levels/"+itoa(levelID)+"/entities/"+itoa(eid), tok,
		jsonBody(t, map[string]any{"x": 7, "y": 7})))
	levelHandlerOK(t, rr, http.StatusOK)
	var moved struct {
		Entity struct {
			X int32 `json:"x"`
			Y int32 `json:"y"`
			RotationDegrees int16 `json:"rotation_degrees"`
		} `json:"entity"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &moved); err != nil {
		t.Fatalf("decode move: %v", err)
	}
	if moved.Entity.X != 7 || moved.Entity.Y != 7 {
		t.Errorf("move: got (%d,%d), want (7,7)", moved.Entity.X, moved.Entity.Y)
	}
	if moved.Entity.RotationDegrees != 90 {
		t.Errorf("rotation should be preserved on partial PATCH, got %d", moved.Entity.RotationDegrees)
	}

	// 4) PATCH (overrides).
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodPatch, "/design/levels/"+itoa(levelID)+"/entities/"+itoa(eid), tok,
		jsonBody(t, map[string]any{"instance_overrides": map[string]any{"hp": 50}})))
	levelHandlerOK(t, rr, http.StatusOK)
	if !strings.Contains(rr.Body.String(), `"hp":50`) {
		t.Errorf("overrides not echoed: %s", rr.Body.String())
	}

	// 5) DELETE.
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodDelete, "/design/levels/"+itoa(levelID)+"/entities/"+itoa(eid), tok, nil))
	levelHandlerOK(t, rr, http.StatusNoContent)

	// 6) List again — empty.
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/levels/"+itoa(levelID)+"/entities", tok, nil))
	levelHandlerOK(t, rr, http.StatusOK)
	if !strings.Contains(rr.Body.String(), `"entities":[]`) {
		t.Errorf("expected empty entities after delete, got %s", rr.Body.String())
	}
}

func TestLevelEntities_CrossLevelGuard(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, levelID, etID, otherLevelID := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	// Place an entity on `levelID`.
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodPost, "/design/levels/"+itoa(levelID)+"/entities", tok,
		jsonBody(t, map[string]any{"entity_type_id": etID, "x": 1, "y": 1})))
	levelHandlerOK(t, rr, http.StatusCreated)
	var placed struct {
		Entity struct {
			ID int64 `json:"id"`
		} `json:"entity"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &placed)

	// Try to PATCH it through the OTHER level's URL — must 404.
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodPatch,
		"/design/levels/"+itoa(otherLevelID)+"/entities/"+itoa(placed.Entity.ID), tok,
		jsonBody(t, map[string]any{"x": 9})))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-level PATCH should 404, got %d body=%s", rr.Code, rr.Body.String())
	}
	// And DELETE — same.
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodDelete,
		"/design/levels/"+itoa(otherLevelID)+"/entities/"+itoa(placed.Entity.ID), tok, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-level DELETE should 404, got %d", rr.Code)
	}

	// Confirm the original placement is still there on its real level.
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/levels/"+itoa(levelID)+"/entities", tok, nil))
	levelHandlerOK(t, rr, http.StatusOK)
	if !strings.Contains(rr.Body.String(), `"id":`+strconv.FormatInt(placed.Entity.ID, 10)) {
		t.Errorf("placement unexpectedly missing: %s", rr.Body.String())
	}
}

func TestLevelEntities_RejectsBadRotation(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, levelID, etID, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodPost, "/design/levels/"+itoa(levelID)+"/entities", tok,
		jsonBody(t, map[string]any{"entity_type_id": etID, "x": 0, "y": 0, "rotation_degrees": 45})))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("rotation 45 should 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestLevelEntities_RejectsMissingEntityType(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, levelID, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodPost, "/design/levels/"+itoa(levelID)+"/entities", tok,
		jsonBody(t, map[string]any{"x": 1, "y": 1})))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing entity_type_id should 400, got %d", rr.Code)
	}
}

func TestLevelEntities_AuthRequired(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, _, levelID, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	rr := httptest.NewRecorder()
	// No cookie. Browser request → redirect to /design/login (302).
	req := httptest.NewRequest(http.MethodGet, "/design/levels/"+itoa(levelID)+"/entities", nil)
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound && rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauth should 302/401, got %d", rr.Code)
	}
}

func TestLevelEntities_NotFoundLevel(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, _, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/levels/9999999/entities", tok, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing level should 404, got %d", rr.Code)
	}
}

// TestLevelEntities_StackedPlacements verifies multiple entities can
// occupy the same cell — the editor lets the user place a region
// trigger on top of an NPC, etc.
func TestLevelEntities_StackedPlacements(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, levelID, etID, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	for i := 0; i < 3; i++ {
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, authedReq(http.MethodPost, "/design/levels/"+itoa(levelID)+"/entities", tok,
			jsonBody(t, map[string]any{"entity_type_id": etID, "x": 5, "y": 5})))
		levelHandlerOK(t, rr, http.StatusCreated)
	}

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/levels/"+itoa(levelID)+"/entities", tok, nil))
	levelHandlerOK(t, rr, http.StatusOK)
	var listResp struct {
		Entities []json.RawMessage `json:"entities"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &listResp)
	if len(listResp.Entities) != 3 {
		t.Fatalf("expected 3 stacked placements, got %d body=%s", len(listResp.Entities), rr.Body.String())
	}
}

func TestLevelSettings_UpdatesModes(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, levelID, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	body := strings.NewReader("instancing_mode=per_user&persistence_mode=transient&spectator_policy=invite")
	rr := httptest.NewRecorder()
	req := authedReq(http.MethodPost, "/design/levels/"+itoa(levelID)+"/settings", tok, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rr, req)
	levelHandlerOK(t, rr, http.StatusOK)

	lv, err := deps.Levels.FindByID(context.Background(), levelID)
	if err != nil {
		t.Fatalf("re-find: %v", err)
	}
	if lv.InstancingMode != "per_user" || lv.PersistenceMode != "transient" || lv.SpectatorPolicy != "invite" {
		t.Errorf("modes not saved: %+v", lv)
	}
}

// TestLevelEditor_EntitiesTab_RendersPixiHost verifies the new
// Entities tab returns the minimal Pixi-host shell: the JS entry
// script, the host element with level/map dims, and the WS url +
// ticket the gateway minted. The Pixi-rendered editor reads its
// palette + chrome from the WS snapshot at boot, so the templ no
// longer carries palette data attributes — we just confirm the
// boot config is present.
func TestLevelEditor_EntitiesTab_RendersPixiHost(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, levelID, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/levels/"+itoa(levelID)+"?tab=entities", tok, nil))
	levelHandlerOK(t, rr, http.StatusOK)
	body := rr.Body.String()

	for _, want := range []string{
		`data-bx-level-editor`,
		`data-level-id="` + itoa(levelID) + `"`,
		`data-map-w="`,
		`data-map-h="`,
		`/static/web/level-editor.js`,
		`data-surface="level-editor-entities"`,
		`data-bx-ws-url="`,
		`data-bx-ws-ticket="`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body", want)
		}
	}
}

func TestLevelSettings_RejectsInvalidMode(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	deps, tok, levelID, _, _ := levelEntitiesFixture(t, pool)
	srv := buildHandler(deps)

	body := strings.NewReader("instancing_mode=bogus")
	rr := httptest.NewRecorder()
	req := authedReq(http.MethodPost, "/design/levels/"+itoa(levelID)+"/settings", tok, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode should 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// Compile-time assertion that the auth service is wired the way our
// helpers assume — keeps a future Deps refactor from silently breaking
// the fixture.
var _ = authdesigner.RoleEditor
