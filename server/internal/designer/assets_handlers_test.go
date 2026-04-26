package designer_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	designerhandlers "boxland/server/internal/designer"
	"boxland/server/internal/persistence"
	"boxland/server/internal/publishing/artifact"
)

// makeStore mirrors the helper in internal/assets/upload_test.go so we
// don't need to import test-only code across packages.
func makeStore(t *testing.T) *persistence.ObjectStore {
	t.Helper()
	cfg := persistence.ObjectStoreConfig{
		Endpoint: "http://localhost:9000", Region: "us-east-1",
		Bucket: "boxland-assets", AccessKeyID: "boxland",
		SecretAccessKey: "boxland_dev_secret", UsePathStyle: true,
		PublicBaseURL: "http://localhost:9000/boxland-assets",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := persistence.NewObjectStore(ctx, cfg)
	if err != nil {
		t.Skipf("minio unavailable: %v", err)
	}
	return store
}

// fullDeps builds a Deps with every Asset Manager dependency wired so the
// handlers can be exercised end-to-end.
func fullDeps(t *testing.T, pool *pgxpool.Pool) (designerhandlers.Deps, int64) {
	t.Helper()
	store := makeStore(t)
	authSvc := authdesigner.New(pool)
	d, _ := authSvc.CreateDesigner(context.Background(), "asset-handler@x.com", "p", authdesigner.RoleEditor)

	assetSvc := assets.New(pool)
	registry := artifact.NewRegistry()
	registry.Register(assets.NewHandler(assetSvc))

	return designerhandlers.Deps{
		Auth:            authSvc,
		Assets:          assetSvc,
		Importers:       assets.DefaultRegistry(),
		BakeJob:         assets.NewBakeJob(pool, store, assetSvc),
		PublishPipeline: artifact.NewPipeline(pool, registry),
		ObjectStore:     store,
	}, d.ID
}

func authedReq(method, path, designerToken string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = "127.0.0.1:1"
	req.AddCookie(&http.Cookie{Name: designerhandlers.SessionCookieName, Value: designerToken})
	return req
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func multipartUpload(t *testing.T, body []byte, filename string) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	hdr["Content-Type"] = []string{"image/png"}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(part, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func TestAssetsList_RendersWithUploadButton(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDeps(t, pool)
	srv := buildHandler(deps)

	tok, _ := deps.Auth.OpenSession(context.Background(), designerID, "ua", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/assets", tok, nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`data-surface="asset-manager"`,
		`>Assets<`,
		`data-bx-action="open-upload"`,
		`hx-get="/design/assets/grid"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body", want)
		}
	}
}

func TestAssetsList_FilterByKind(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDeps(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()

	// Seed: 1 sprite, 1 tile.
	_, _ = deps.Assets.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "boss", ContentAddressedPath: "p1",
		OriginalFormat: "png", CreatedBy: designerID,
	})
	_, _ = deps.Assets.Create(ctx, assets.CreateInput{
		Kind: assets.KindTile, Name: "wall", ContentAddressedPath: "p2",
		OriginalFormat: "png", CreatedBy: designerID,
	})

	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/assets/grid?kind=sprite", tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "boss") || strings.Contains(body, "wall") {
		t.Errorf("expected only the sprite to render; body=%s", body)
	}
}

func TestAssetUpload_ViaHTMX_ReturnsToast(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDeps(t, pool)
	srv := buildHandler(deps)

	body, ct := multipartUpload(t, tinyPNG(t), "boss.png")
	tok, _ := deps.Auth.OpenSession(context.Background(), designerID, "ua", nil)
	req := authedReq(http.MethodPost, "/design/assets/upload", tok, body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("HX-Request", "true")

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "bx-toast--success") {
		t.Errorf("expected success toast; body=%s", rr.Body.String())
	}
}

func TestAssetDetail_ShowsForm(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDeps(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()

	a, _ := deps.Assets.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "candidate", ContentAddressedPath: "p",
		OriginalFormat: "png", Tags: []string{"alpha"}, CreatedBy: designerID,
	})

	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/assets/"+itoa(a.ID), tok, nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`>candidate<`,
		`name="name"`,
		`hx-post="/design/assets/` + itoa(a.ID) + `/draft"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body", want)
		}
	}
}

func TestAssetsGrid_TileSheetShowsCellPreview(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDeps(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()

	md, err := assets.MarshalTileSheetMetadata(assets.TileSheetMetadata{
		TileSize: assets.TileSize, Cols: 2, Rows: 2,
		NonEmptyCount: 3, NonEmptyIndex: []int{0, 1, 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = deps.Assets.Create(ctx, assets.CreateInput{
		Kind: assets.KindTile, Name: "cavern", ContentAddressedPath: "tiles/cavern.png",
		OriginalFormat: "png", MetadataJSON: md, CreatedBy: designerID,
	})

	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodGet, "/design/assets/grid?kind=tile", tok, nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`bx-asset-card__tile-preview`,
		`data-bx-tile-preview="/design/assets/blob/`,
		`data-cols="2"`,
		`data-rows="2"`,
		`data-non-empty="0,1,3"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body", want)
		}
	}
}

func TestAssetDraft_PostStoresInDraftsTable(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDeps(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()

	a, _ := deps.Assets.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "old", ContentAddressedPath: "p",
		OriginalFormat: "png", CreatedBy: designerID,
	})

	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)
	form := strings.NewReader("name=new-name&tags=fresh,boss")
	req := authedReq(http.MethodPost, "/design/assets/"+itoa(a.ID)+"/draft", tok, form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM drafts WHERE artifact_kind = 'asset' AND artifact_id = $1`, a.ID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 draft row, got %d", n)
	}
}

func TestAssetDelete_RemovesRowAndReturnsGrid(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDeps(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()

	a, _ := deps.Assets.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "doomed", ContentAddressedPath: "p",
		OriginalFormat: "png", CreatedBy: designerID,
	})

	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, authedReq(http.MethodDelete, "/design/assets/"+itoa(a.ID), tok, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if _, err := deps.Assets.FindByID(ctx, a.ID); err == nil {
		t.Error("expected asset to be deleted")
	}
	if !strings.Contains(rr.Body.String(), `id="assets-grid"`) {
		t.Errorf("expected refreshed grid in response; body=%s", rr.Body.String())
	}
}

func TestAssetReplace_CreatesNewAssetWithSameKind(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	deps, designerID := fullDeps(t, pool)
	srv := buildHandler(deps)
	ctx := context.Background()

	// Seed the original sprite with a known content path.
	original, _ := deps.Assets.Create(ctx, assets.CreateInput{
		Kind: assets.KindSprite, Name: "boss", ContentAddressedPath: "p-original",
		OriginalFormat: "png", CreatedBy: designerID,
	})

	tok, _ := deps.Auth.OpenSession(ctx, designerID, "ua", nil)
	body, ct := multipartUpload(t, tinyPNG(t), "boss-edited.png")
	req := authedReq(http.MethodPost, "/design/assets/"+itoa(original.ID)+"/replace", tok, body)
	req.Header.Set("Content-Type", ct)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}

	// The original row is untouched.
	stillThere, err := deps.Assets.FindByID(ctx, original.ID)
	if err != nil {
		t.Fatalf("FindByID original: %v", err)
	}
	if stillThere.ContentAddressedPath != "p-original" {
		t.Errorf("original asset's content path mutated: %q", stillThere.ContentAddressedPath)
	}

	// The response is JSON shaped {old_asset_id, new_asset, reused}.
	if !strings.Contains(rr.Body.String(), `"old_asset_id"`) {
		t.Errorf("expected old_asset_id in response; got %s", rr.Body.String())
	}
	// JSON tags on the Asset struct emit snake_case keys (see asset.go).
	if !strings.Contains(rr.Body.String(), `"kind":"sprite"`) {
		t.Errorf("new asset should keep the sprite kind; got %s", rr.Body.String())
	}
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		d := byte(i % 10)
		out = string(rune('0'+d)) + out
		i /= 10
	}
	return out
}
