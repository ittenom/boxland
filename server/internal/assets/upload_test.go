package assets_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"boxland/server/internal/assets"
	"boxland/server/internal/persistence"
)

// makeStore builds an ObjectStore against the dev MinIO. Skips the test if
// MinIO isn't reachable (matches the postgres/redis pattern).
func makeStore(t *testing.T) *persistence.ObjectStore {
	t.Helper()
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
	return store
}

// pngOf builds a tiny in-memory PNG of size w x h, filled with an opaque
// solid color. Used for upload smoke tests so we don't need any fixture
// files on disk. The fill is deliberate: tile-sheet uploads now reject
// fully-transparent PNGs (every cell skipped → empty palette is the
// designer's worst nightmare), so a one-pixel fill makes a "valid"
// minimal sheet.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 80, G: 130, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

// makeUploadRequest builds a multipart POST suitable for Service.Upload.
func makeUploadRequest(t *testing.T, filename string, body []byte, contentType string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	if contentType != "" {
		hdr["Content-Type"] = []string{contentType}
	}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(body)); err != nil {
		t.Fatalf("copy body: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestUpload_HappyPath_PNG(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)

	body := pngOf(t, 32, 32)
	req := makeUploadRequest(t, "boss.png", body, "image/png")
	res, err := svc.Upload(context.Background(), req, store, designerID, "")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.Reused {
		t.Errorf("expected fresh upload, got Reused=true")
	}
	if res.Asset.Kind != assets.KindSprite {
		t.Errorf("default kind for png should be sprite, got %q", res.Asset.Kind)
	}
	if res.Asset.OriginalFormat != "png" {
		t.Errorf("OriginalFormat: got %q", res.Asset.OriginalFormat)
	}
	if res.Asset.Name != "boss" {
		t.Errorf("display name should strip ext, got %q", res.Asset.Name)
	}
	if !strings.HasPrefix(res.Asset.ContentAddressedPath, "assets/") {
		t.Errorf("content path should be sha-shaped, got %q", res.Asset.ContentAddressedPath)
	}
}

func TestUpload_DedupReusesExistingAsset(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)
	body := pngOf(t, 32, 32)

	first, err := svc.Upload(context.Background(),
		makeUploadRequest(t, "a.png", body, "image/png"), store, designerID, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused {
		t.Error("first upload should NOT be a reuse")
	}

	second, err := svc.Upload(context.Background(),
		makeUploadRequest(t, "a-copy.png", body, "image/png"), store, designerID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused {
		t.Error("second upload of identical bytes should reuse")
	}
	if second.Asset.ID != first.Asset.ID {
		t.Errorf("dedup should return the original asset id; got %d vs %d",
			second.Asset.ID, first.Asset.ID)
	}
}

func TestUpload_KindOverride(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)
	body := pngOf(t, 32, 32)

	res, err := svc.Upload(context.Background(),
		makeUploadRequest(t, "wall.png", body, "image/png"),
		store, designerID, assets.KindTile)
	if err != nil {
		t.Fatal(err)
	}
	if res.Asset.Kind != assets.KindTile {
		t.Errorf("override should win; got %q", res.Asset.Kind)
	}
}

func TestUpload_RejectsUnsupportedType(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)

	body := []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	req := makeUploadRequest(t, "nope.svg", body, "image/svg+xml")
	_, err := svc.Upload(context.Background(), req, store, designerID, "")
	if !errors.Is(err, assets.ErrUnsupportedContentType) {
		t.Errorf("got %v, want ErrUnsupportedContentType", err)
	}
}

func TestUpload_RejectsTooLarge(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)

	// Build a payload one byte over the limit.
	huge := make([]byte, assets.MaxUploadBytes+1)
	req := makeUploadRequest(t, "big.png", huge, "application/octet-stream")
	_, err := svc.Upload(context.Background(), req, store, designerID, "")
	if !errors.Is(err, assets.ErrTooLarge) {
		t.Errorf("got %v, want ErrTooLarge", err)
	}
}

func TestUpload_DifferentBytesProduceDifferentRows(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)

	a, _ := svc.Upload(context.Background(),
		makeUploadRequest(t, "a.png", pngOf(t, 32, 32), "image/png"),
		store, designerID, "")
	// Different size → different bytes → different sha.
	b, _ := svc.Upload(context.Background(),
		makeUploadRequest(t, "b.png", pngOf(t, 16, 16), "image/png"),
		store, designerID, "")

	if a.Asset.ID == b.Asset.ID {
		t.Errorf("expected different rows for different bytes")
	}
	if a.Asset.ContentAddressedPath == b.Asset.ContentAddressedPath {
		t.Errorf("expected different content paths")
	}
}

func TestUpload_AutoDetectsMultiCellPNGAsTileSheet(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)

	body := pngOf(t, 64, 32)
	res, err := svc.Upload(context.Background(),
		makeUploadRequest(t, "terrain.png", body, "image/png"),
		store, designerID, "")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.Asset.Kind != assets.KindTile {
		t.Fatalf("auto-detected kind = %q, want tile", res.Asset.Kind)
	}
	if len(res.TileCells) != 2 {
		t.Fatalf("TileCells len = %d, want 2", len(res.TileCells))
	}
	if !strings.Contains(string(res.Asset.MetadataJSON), `"non_empty_count"`) {
		t.Errorf("tile metadata missing non_empty_count: %s", res.Asset.MetadataJSON)
	}
}

func TestUpload_ExplicitAnimatedSpriteStaysSpriteSheet(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)
	svc.Importers = assets.DefaultRegistry()

	// 4 cols × 4 rows = top-down character strip.
	body := pngOf(t, 4*32, 4*32)
	res, err := svc.Upload(context.Background(),
		makeUploadRequest(t, "hero.png", body, "image/png"),
		store, designerID, assets.KindOverrideAnimatedSprite)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.Reused {
		t.Fatalf("first upload should not be Reused")
	}
	rows, err := svc.ListAnimations(context.Background(), res.Asset.ID)
	if err != nil {
		t.Fatalf("ListAnimations: %v", err)
	}
	names := make(map[string]assets.AnimationRow, len(rows))
	for _, r := range rows {
		names[r.Name] = r
	}
	for _, want := range []string{
		assets.AnimWalkN, assets.AnimWalkE, assets.AnimWalkS, assets.AnimWalkW, assets.AnimIdle,
	} {
		if _, ok := names[want]; !ok {
			t.Errorf("upload should have synthesized %q (got %v)", want, rows)
		}
	}
	// Sheet metadata folded into the asset row so the runtime catalog
	// can compute frame rects without re-parsing the PNG.
	mdStr := string(res.Asset.MetadataJSON)
	if !strings.Contains(mdStr, `"grid_w"`) || !strings.Contains(mdStr, `"cols"`) {
		t.Errorf("sprite metadata should carry grid info, got %s", mdStr)
	}
}

func TestUpload_ExplicitSpriteSheetBackfilledOnReuse(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)
	// First upload: importers off → no animations persisted.
	body := pngOf(t, 4*32, 4*32)
	first, err := svc.Upload(context.Background(),
		makeUploadRequest(t, "hero.png", body, "image/png"),
		store, designerID, assets.KindOverrideSpriteSheet)
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	rows, _ := svc.ListAnimations(context.Background(), first.Asset.ID)
	if len(rows) != 0 {
		t.Fatalf("pre-condition: expected 0 rows when importer disabled, got %d", len(rows))
	}
	// Now re-upload with importers wired: must backfill.
	svc.Importers = assets.DefaultRegistry()
	second, err := svc.Upload(context.Background(),
		makeUploadRequest(t, "hero-v2.png", body, "image/png"),
		store, designerID, assets.KindOverrideSpriteSheet)
	if err != nil {
		t.Fatalf("re-upload: %v", err)
	}
	if !second.Reused {
		t.Errorf("expected reuse on identical bytes")
	}
	rows, _ = svc.ListAnimations(context.Background(), second.Asset.ID)
	if len(rows) == 0 {
		t.Errorf("animations should have been backfilled on reuse")
	}
}

func TestUpload_WAVPopulatesAudioMetadata(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	designerID := resetDB(t, pool)
	store := makeStore(t)
	svc := assets.New(pool)

	// Reuse the WAV builder from audio_test.go via a tiny inline copy
	// (can't import _test files across files in different test binaries
	// without exporting; the builder is small enough to inline).
	wav := makeTestWAV(t, 44100, 1, 16, 22050) // 0.5s
	req := makeUploadRequest(t, "ping.wav", wav, "audio/wav")
	res, err := svc.Upload(context.Background(), req, store, designerID, "")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.Asset.Kind != assets.KindAudio {
		t.Errorf("kind: got %q, want audio", res.Asset.Kind)
	}
	if !strings.Contains(string(res.Asset.MetadataJSON), `"duration_ms"`) {
		t.Errorf("metadata should contain duration_ms, got %s", res.Asset.MetadataJSON)
	}
	// Postgres jsonb reformats with spaces around colons; match either form.
	mdStr := string(res.Asset.MetadataJSON)
	if !strings.Contains(mdStr, `"format": "wav"`) && !strings.Contains(mdStr, `"format":"wav"`) {
		t.Errorf("metadata should record wav format, got %s", mdStr)
	}
}

// makeTestWAV mirrors makeWAV in audio_test.go. Inlined to avoid sharing
// helpers across separate _test.go files in the same package which can
// land in different test binaries.
func makeTestWAV(t *testing.T, sampleRate, channels, bitDepth, dataSamples int) []byte {
	t.Helper()
	bytesPerSample := bitDepth / 8
	dataBytes := dataSamples * bytesPerSample * channels
	byteRate := sampleRate * channels * bytesPerSample
	blockAlign := channels * bytesPerSample

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+dataBytes))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(bitDepth))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(dataBytes))
	buf.Write(make([]byte, dataBytes))
	return buf.Bytes()
}

// stub to keep the os import alive on platforms where the tests above are
// all skipped (no postgres/minio).
var _ = os.Stdin
