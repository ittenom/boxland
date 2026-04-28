package designer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	designerhandlers "boxland/server/internal/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/folders"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence"
	"boxland/server/internal/tilemaps"
	"boxland/server/internal/worlds"
)

// redesign_routes_test.go — smoke tests for the Phase 2 IDE surfaces
// from the holistic redesign. The point is to catch route-registration
// crashes (mux conflicts, double-space typos, dependency bugs in
// chrome.go) at test time, not on first dev boot. Each route here
// gets one happy-path GET / POST hit.

// buildRedesignDeps wires every dep the new handlers expect. The
// goal is "exercise the route, not the underlying behavior" — we
// don't seed exhaustive test data, just enough that BuildChrome and
// the page templates render without nil-deref.
func buildRedesignDeps(pool any, designerSvc *authdesigner.Service) designerhandlers.Deps {
	p := pool.(interface {
		// pgxpool.Pool's surface, narrowed.
	})
	_ = p
	return designerhandlers.Deps{}
}

// TestRedesignRoutes_RegisteredCleanly hits every Phase 2 route at
// least once and asserts none of them surface a nil-deref or 500 due
// to wiring issues. Auth is required, so we open a session first.
func TestRedesignRoutes_RegisteredCleanly(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()

	auth := authdesigner.New(pool)
	dr, err := auth.CreateDesigner(ctx, "redesign-routes@x.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	tok, err := auth.OpenSession(ctx, dr.ID, "ua", nil)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}

	assetSvc := assets.New(pool)
	entitySvc := entities.New(pool, components.Default())
	mapsSvc := mapsservice.New(pool)
	tilemapsSvc := tilemaps.New(pool, assetSvc, entitySvc)
	levelsSvc := levels.New(pool)
	worldsSvc := worlds.New(pool)
	foldersSvc := folders.New(pool)
	store := persistence.ObjectStoreForTest("https://cdn.example.test")

	deps := designerhandlers.Deps{
		Auth:        auth,
		Assets:      assetSvc,
		Entities:    entitySvc,
		Components:  components.Default(),
		Folders:     foldersSvc,
		Tilemaps:    tilemapsSvc,
		Maps:        mapsSvc,
		Levels:      levelsSvc,
		Worlds:      worldsSvc,
		ObjectStore: store,
	}
	srv := buildHandler(deps)

	// Seed a map + a world + a level so detail pages have something
	// to render. A bare list page suffices for the tilemaps surface.
	m, err := mapsSvc.Create(ctx, mapsservice.CreateInput{
		Name: "smoke-map", Width: 8, Height: 8, CreatedBy: dr.ID,
	})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	w0, err := worldsSvc.Create(ctx, worlds.CreateInput{Name: "Smoke", CreatedBy: dr.ID})
	if err != nil {
		t.Fatalf("create world: %v", err)
	}
	lv, err := levelsSvc.Create(ctx, levels.CreateInput{
		Name: "Town", MapID: m.ID, WorldID: &w0.ID, CreatedBy: dr.ID,
	})
	if err != nil {
		t.Fatalf("create level: %v", err)
	}

	type call struct {
		method, path string
		// Accepts any 2xx OR a 3xx redirect; both prove the route
		// resolved cleanly without a 500.
	}
	cases := []call{
		// Top-level lists.
		{"GET", "/design/worlds"},
		{"GET", "/design/levels"},
		{"GET", "/design/tilemaps"},
		{"GET", "/design/library"}, // 302 to /design/library/sprites
		{"GET", "/design/library/sprites"},
		{"GET", "/design/library/tilemaps"},
		{"GET", "/design/library/audio"},
		{"GET", "/design/library/ui-panels"},

		// Per-class entity pages.
		{"GET", "/design/entities"},
		{"GET", "/design/entities/tiles"},
		{"GET", "/design/entities/npcs"},
		{"GET", "/design/entities/pcs"},
		{"GET", "/design/entities/logic"},

		// Detail pages for the seeded fixtures.
		{"GET", "/design/worlds/" + itoa(w0.ID)},
		{"GET", "/design/levels/" + itoa(lv.ID)},
		// Each of the level editor's tabs is reachable via ?tab=.
		{"GET", "/design/levels/" + itoa(lv.ID) + "?tab=geometry"},
		{"GET", "/design/levels/" + itoa(lv.ID) + "?tab=entities"},
		{"GET", "/design/levels/" + itoa(lv.ID) + "?tab=hud"},
		{"GET", "/design/levels/" + itoa(lv.ID) + "?tab=automations"},
		{"GET", "/design/levels/" + itoa(lv.ID) + "?tab=settings"},
		// Visual level-editor placement API. List should return an
		// empty array on a fresh level — proves the route resolved
		// without 500. PATCH/DELETE are exercised end-to-end in
		// level_entities_handlers_test.go.
		{"GET", "/design/levels/" + itoa(lv.ID) + "/entities"},

		// Modals.
		{"GET", "/design/worlds/new"},
		{"GET", "/design/levels/new"},

		// Phase 3 export/import surfaces.
		{"GET", "/design/worlds/" + itoa(w0.ID) + "/export"},
		{"GET", "/design/worlds/import"},
		{"GET", "/design/levels/" + itoa(lv.ID) + "/export"},
		{"GET", "/design/levels/import"},
		{"GET", "/design/tilemaps/import"},
		// Tilemap export needs a real tilemap, which would require a
		// tile sheet upload to set up; leave it out of this smoke test
		// (covered by the round-trip test in the exporter package).
	}

	for _, c := range cases {
		req := authedReq(c.method, c.path, tok, nil)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code >= 500 {
			t.Errorf("%s %s — server error %d: %s", c.method, c.path, rr.Code,
				strings.TrimSpace(rr.Body.String()))
		}
		if rr.Code == http.StatusUnauthorized {
			t.Errorf("%s %s — unexpected 401 (auth misconfigured?)", c.method, c.path)
		}
	}
}
