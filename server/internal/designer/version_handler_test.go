package designer_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
	designerhandlers "boxland/server/internal/designer"
	"boxland/server/internal/updater"
)

// authedRequest builds a GET request carrying a fresh designer
// session cookie. Centralised so the per-test boilerplate is short.
func authedRequest(t *testing.T, pool *pgxpool.Pool, path string) (*http.Request, *authdesigner.Service) {
	t.Helper()
	auth := authdesigner.New(pool)
	ctx := context.Background()
	email := "version-test-" + t.Name() + "@example.test"
	d, err := auth.CreateDesigner(ctx, email, "password-12", authdesigner.RoleEditor)
	if err != nil {
		t.Fatalf("create designer: %v", err)
	}
	tok, err := auth.OpenSession(ctx, d.ID, "test-ua", net.ParseIP("127.0.0.1"))
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: designerhandlers.SessionCookieName, Value: tok})
	return req, auth
}

func TestVersionAPI_RequiresAuth(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	srv := buildHandler(designerhandlers.Deps{Auth: authdesigner.New(pool)})

	req := httptest.NewRequest(http.MethodGet, "/design/api/version", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rr.Code)
	}
}

func TestVersionAPI_NoCacheReturnsBlankStatus(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)

	cli := updater.NewClient("ittenom/boxland-test")
	cli.CachePath = filepath.Join(t.TempDir(), "missing.json") // never written

	deps := designerhandlers.Deps{Auth: authdesigner.New(pool), Updates: cli}
	srv := buildHandler(deps)
	req, _ := authedRequest(t, pool, "/design/api/version")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var body updater.Status
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.HasUpdate {
		t.Errorf("HasUpdate = true on a cold cache: %+v", body)
	}
}

func TestVersionAPI_ReadsFromCache(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)

	// Seed the cache with a "ready to upgrade" snapshot by writing
	// directly through the updater (so the test exercises both ends).
	cli := updater.NewClient("ittenom/boxland")
	cli.CachePath = filepath.Join(t.TempDir(), "cache.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name":"v999.0.0",
			"html_url":"https://example.test/r",
			"body":"notes",
			"published_at":"2026-01-01T00:00:00Z"
		}`))
	}))
	t.Cleanup(srv.Close)
	prev := updater.APIBaseOverride
	updater.APIBaseOverride = srv.URL
	t.Cleanup(func() { updater.APIBaseOverride = prev })
	if _, err := cli.CheckLatest(context.Background(), updater.CheckOpts{}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	deps := designerhandlers.Deps{Auth: authdesigner.New(pool), Updates: cli}
	httpHandler := buildHandler(deps)
	req, _ := authedRequest(t, pool, "/design/api/version")
	rr := httptest.NewRecorder()
	httpHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body updater.Status
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Latest != "v999.0.0" {
		t.Errorf("Latest = %q, want v999.0.0", body.Latest)
	}
	if !body.HasUpdate {
		t.Errorf("HasUpdate should be true (current=%s, latest=%s)", body.Current, body.Latest)
	}
	if body.ReleaseURL == "" {
		t.Errorf("ReleaseURL should be passed through")
	}
}
