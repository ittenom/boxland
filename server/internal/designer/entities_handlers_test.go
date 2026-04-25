package designer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	designerhandlers "boxland/server/internal/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
)

// fullDepsWithEntities mirrors fullDeps but also wires the entity service +
// component registry. Lives here to avoid forcing every Asset-Manager test
// to import the entity packages.
func fullDepsWithEntities(t *testing.T, pool *pgxpool.Pool) (designerhandlers.Deps, int64) {
	t.Helper()
	d, designerID := fullDeps(t, pool)
	d.Components = components.Default()
	d.Entities = entities.New(pool, d.Components)
	return d, designerID
}

func TestEntitiesList_Empty(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)

	tok, _ := deps.Auth.OpenSession(context.Background(), designerID, "ua", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/entities", tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `data-surface="entity-manager"`) {
		t.Errorf("expected entity-manager surface; got %s", rr.Body.String())
	}
}

func TestEntityCreateAndDuplicate(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)

	// Create one entity type via the form-style POST.
	form := strings.NewReader("name=goblin")
	req := authedReq(http.MethodPost, "/design/entities", tok, form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "goblin") {
		t.Errorf("created entity not in grid response")
	}

	// Find it by name to grab the id, then duplicate.
	created, err := deps.Entities.FindByName(ctx, "goblin")
	if err != nil {
		t.Fatal(err)
	}
	dupReq := authedReq(http.MethodPost, "/design/entities/"+itoa(created.ID)+"/duplicate", tok, nil)
	dupRR := httptest.NewRecorder()
	srv.ServeHTTP(dupRR, dupReq)
	if dupRR.Code != http.StatusOK {
		t.Fatalf("duplicate status %d, body=%s", dupRR.Code, dupRR.Body.String())
	}
	if !strings.Contains(dupRR.Body.String(), "goblin (copy)") {
		t.Errorf("duplicate name not present; body=%s", dupRR.Body.String())
	}
}

func TestEntityDetail_RendersFormAndComponents(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)

	et, _ := deps.Entities.Create(ctx, entities.CreateInput{Name: "scout", CreatedBy: designerID})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/entities/"+itoa(et.ID), tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status %d, body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{
		`>scout<`,
		`name="name"`,
		`name="collider_w"`,
		`data-bx-collider-overlay`,
		`Add component`,
	} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("missing %q in body", want)
		}
	}
}

func TestEntityComponentLifecycle(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)

	et, _ := deps.Entities.Create(ctx, entities.CreateInput{Name: "blob", CreatedBy: designerID})

	// Add the Position component.
	addReq := authedReq(http.MethodPost, "/design/entities/"+itoa(et.ID)+"/components/add", tok,
		strings.NewReader("kind=position"))
	addReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addRR := httptest.NewRecorder()
	srv.ServeHTTP(addRR, addReq)
	if addRR.Code != http.StatusOK {
		t.Fatalf("add status %d, body=%s", addRR.Code, addRR.Body.String())
	}
	comps, _ := deps.Entities.Components(ctx, et.ID)
	if len(comps) != 1 || comps[0].Kind != components.KindPosition {
		t.Fatalf("expected position component; got %+v", comps)
	}

	// Save with new values via the per-kind endpoint.
	saveReq := authedReq(http.MethodPost,
		"/design/entities/"+itoa(et.ID)+"/components/position", tok,
		strings.NewReader("x=512&y=-256"))
	saveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveRR := httptest.NewRecorder()
	srv.ServeHTTP(saveRR, saveReq)
	if saveRR.Code != http.StatusOK {
		t.Fatalf("save status %d, body=%s", saveRR.Code, saveRR.Body.String())
	}
	comps, _ = deps.Entities.Components(ctx, et.ID)
	if !strings.Contains(string(comps[0].ConfigJSON), `"x": 512`) &&
		!strings.Contains(string(comps[0].ConfigJSON), `"x":512`) {
		t.Errorf("config not persisted: %s", comps[0].ConfigJSON)
	}

	// Delete it.
	delReq := authedReq(http.MethodDelete, "/design/entities/"+itoa(et.ID)+"/components/position", tok, nil)
	delRR := httptest.NewRecorder()
	srv.ServeHTTP(delRR, delReq)
	if delRR.Code != http.StatusOK {
		t.Fatalf("delete status %d", delRR.Code)
	}
	comps, _ = deps.Entities.Components(ctx, et.ID)
	if len(comps) != 0 {
		t.Errorf("expected no components after delete, got %+v", comps)
	}
}

func TestSocketsCRUD_ViaHTTP(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)

	// Empty list page renders.
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/sockets", tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list status %d", rr.Code)
	}

	// Create.
	createReq := authedReq(http.MethodPost, "/design/sockets", tok,
		strings.NewReader("name=stone&color=%23808080"))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createRR := httptest.NewRecorder()
	srv.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusOK {
		t.Fatalf("create status %d, body=%s", createRR.Code, createRR.Body.String())
	}
	if !strings.Contains(createRR.Body.String(), "stone") {
		t.Errorf("created socket should appear in grid; got %s", createRR.Body.String())
	}

	got, _ := deps.Entities.FindSocketByName(ctx, "stone")
	if got == nil {
		t.Fatal("socket not persisted")
	}

	// Delete.
	delReq := authedReq(http.MethodDelete, "/design/sockets/"+itoa(got.ID), tok, nil)
	delRR := httptest.NewRecorder()
	srv.ServeHTTP(delRR, delReq)
	if delRR.Code != http.StatusOK {
		t.Fatalf("delete status %d", delRR.Code)
	}
}

func TestEntityDraft_ValidationRejectsBadCollider(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDepsWithEntities(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()
	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)

	et, _ := deps.Entities.Create(ctx, entities.CreateInput{Name: "foo", CreatedBy: designerID})
	body := strings.NewReader("name=foo&collider_w=16&collider_h=16&collider_anchor_x=99&collider_anchor_y=4&default_collision_mask=1")
	req := authedReq(http.MethodPost, "/design/entities/"+itoa(et.ID)+"/draft", tok, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}
