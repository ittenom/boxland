package playerweb_test

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
	"boxland/server/internal/auth/csrf"
	authdesigner "boxland/server/internal/auth/designer"
	authplayer "boxland/server/internal/auth/player"
	"boxland/server/internal/maps"
	"boxland/server/internal/persistence"
	"boxland/server/internal/playerweb"
)

// catalogFixture is a focused fixture for the asset-catalog endpoint:
// pre-creates an authenticated player and a designer-owned sprite asset
// with a couple of animation rows. Wires the playerweb mux with a real
// asset service + a stub object store.
type catalogFixture struct {
	t       *testing.T
	srv     *httptest.Server
	jar     http.CookieJar
	csrfTok string
	asset   *assets.Asset
}

func newCatalogFixture(t *testing.T) *catalogFixture {
	t.Helper()
	// openPool returns a per-test isolated DB via testdb.New; no manual
	// reset needed.
	pool := openPool(t)
	t.Cleanup(pool.Close)

	authP := authplayer.New(pool, []byte("test-jwt-secret-32-bytes-padded__"))
	authD := authdesigner.New(pool)
	mapsSvc := maps.New(pool)
	assetSvc := assets.New(pool)

	dr, err := authD.CreateDesigner(context.Background(), "designer@cat.com", "p", authdesigner.RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	a, err := assetSvc.Create(context.Background(), assets.CreateInput{
		Kind:                 assets.KindSprite,
		Name:                 "hero",
		ContentAddressedPath: "assets/aa/bb/hero",
		OriginalFormat:       "png",
		MetadataJSON:         []byte(`{"grid_w":32,"grid_h":32,"cols":4,"rows":4,"frame_count":16,"source":"auto"}`),
		CreatedBy:            dr.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := assetSvc.ReplaceAnimations(context.Background(), a.ID, []assets.Animation{
		{Name: "walk_east", FrameFrom: 4, FrameTo: 7, FPS: 8, Direction: assets.DirForward},
		{Name: "idle", FrameFrom: 8, FrameTo: 8, FPS: 1, Direction: assets.DirForward},
	}); err != nil {
		t.Fatal(err)
	}

	store := &stubObjectStore{base: "https://cdn.test"}

	deps := playerweb.Deps{
		Auth:          authP,
		Maps:          mapsSvc,
		Assets:        assetSvc,
		ObjectStore:   store.real(),
		SecureCookies: false,
	}
	csrfMW := csrf.Middleware(csrf.Config{Secure: false, SameSite: http.SameSiteStrictMode})
	loadMW := playerweb.LoadSession(deps)
	mux := csrfMW(loadMW(playerweb.New(deps)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar := newJar()
	c := noRedirectClient(jar)
	tok := primeCSRF(t, c, srv.URL)
	// Signup -> session cookie set on the jar.
	resp := postForm(t, c, srv.URL+"/play/signup", tok,
		url.Values{"email": {"catalog-user@x.com"}, "password": {"hunter2pass"}})
	resp.Body.Close()

	return &catalogFixture{t: t, srv: srv, jar: jar, csrfTok: tok, asset: a}
}

func (f *catalogFixture) get(path string) *http.Response {
	c := noRedirectClient(f.jar)
	resp, err := c.Get(f.srv.URL + path)
	if err != nil {
		f.t.Fatal(err)
	}
	return resp
}

func TestAssetCatalog_ReturnsAssetWithAnimations(t *testing.T) {
	f := newCatalogFixture(t)
	resp := f.get("/play/asset-catalog?ids=" + intStr(f.asset.ID))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc == "" || !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control should be set; got %q", cc)
	}
	var out struct {
		Assets []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			URL        string `json:"url"`
			GridW      int    `json:"grid_w"`
			Cols       int    `json:"cols"`
			Animations []struct {
				Name      string `json:"name"`
				FrameFrom int    `json:"frame_from"`
				FrameTo   int    `json:"frame_to"`
				FPS       int    `json:"fps"`
				Direction string `json:"direction"`
			} `json:"animations"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Assets) != 1 {
		t.Fatalf("got %d assets, want 1", len(out.Assets))
	}
	a := out.Assets[0]
	if a.ID != f.asset.ID || a.Name != "hero" {
		t.Errorf("asset fields: %+v", a)
	}
	if !strings.HasPrefix(a.URL, "https://cdn.test/") {
		t.Errorf("URL should be CDN-fronted; got %q", a.URL)
	}
	if a.GridW != 32 || a.Cols != 4 {
		t.Errorf("grid metadata wrong: %+v", a)
	}
	if len(a.Animations) != 2 {
		t.Fatalf("got %d anims, want 2", len(a.Animations))
	}
	byName := map[string]int{a.Animations[0].Name: 0, a.Animations[1].Name: 1}
	if _, ok := byName["walk_east"]; !ok {
		t.Errorf("walk_east missing from response: %+v", a.Animations)
	}
	if _, ok := byName["idle"]; !ok {
		t.Errorf("idle missing from response: %+v", a.Animations)
	}
}

func TestAssetCatalog_AnonymousIsRedirected(t *testing.T) {
	f := newCatalogFixture(t)
	c := noRedirectClient(newJar())
	resp, err := c.Get(f.srv.URL + "/play/asset-catalog?ids=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("anonymous: got %d, want 303", resp.StatusCode)
	}
}

func TestAssetCatalog_EmptyIdsReturnsEmptySet(t *testing.T) {
	f := newCatalogFixture(t)
	resp := f.get("/play/asset-catalog")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"assets":[]`) {
		t.Errorf("expected empty assets array; got %s", body)
	}
}

func TestAssetCatalog_RejectsBadIDs(t *testing.T) {
	f := newCatalogFixture(t)
	resp := f.get("/play/asset-catalog?ids=abc")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got %d, want 400", resp.StatusCode)
	}
}

func TestAssetCatalog_DropsMissingIDsSilently(t *testing.T) {
	f := newCatalogFixture(t)
	resp := f.get("/play/asset-catalog?ids=" + intStr(f.asset.ID) + ",999999")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Assets []struct {
			ID int64 `json:"id"`
		} `json:"assets"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Assets) != 1 || out.Assets[0].ID != f.asset.ID {
		t.Errorf("missing ids should be dropped silently; got %+v", out.Assets)
	}
}

// ---- helpers ----

func intStr(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// stubObjectStore is a tiny shim so the catalog test doesn't need a
// real S3/MinIO. Holds a fake CDN base + builds URLs the same way the
// real ObjectStore.PublicURL does.
type stubObjectStore struct {
	base string
}

// real wraps the stub into a *persistence.ObjectStore. The catalog
// handler only calls PublicURL, which is a deterministic format
// concatenation; we use the real type for type-correctness, configured
// with the stub's base URL.
func (s *stubObjectStore) real() *persistence.ObjectStore {
	return persistence.ObjectStoreForTest(s.base)
}
