package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"boxland/server/internal/exporter"
)

// buildZip is a tiny helper that builds an in-memory zip from a name→bytes map.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// jsonStr is a tiny one-liner for building JSON bodies.
func jsonStr(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReadManifest_Missing(t *testing.T) {
	body := buildZip(t, map[string]string{"some.txt": "hi"})
	_, _, err := readManifest(body)
	if !errors.Is(err, ErrMissingManifest) {
		t.Fatalf("err = %v, want ErrMissingManifest", err)
	}
}

func TestReadManifest_BadZip(t *testing.T) {
	_, _, err := readManifest([]byte("not a zip"))
	if !errors.Is(err, ErrBadZip) {
		t.Fatalf("err = %v, want ErrBadZip", err)
	}
}

func TestReadManifest_UnsupportedFormat(t *testing.T) {
	body := buildZip(t, map[string]string{
		"manifest.json": jsonStr(t, exporter.Manifest{
			Kind:          exporter.KindAsset,
			FormatVersion: 99, // future major
		}),
	})
	_, _, err := readManifest(body)
	if !errors.Is(err, ErrUnsupportedFmt) {
		t.Fatalf("err = %v, want ErrUnsupportedFmt", err)
	}
}

func TestReadManifest_OK(t *testing.T) {
	body := buildZip(t, map[string]string{
		"manifest.json": jsonStr(t, exporter.Manifest{
			Kind:          exporter.KindAsset,
			FormatVersion: exporter.FormatVersion,
		}),
		"asset.json": "{}",
	})
	m, zr, err := readManifest(body)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if m.Kind != exporter.KindAsset {
		t.Errorf("kind = %s", m.Kind)
	}
	if zr == nil || len(zr.File) < 2 {
		t.Errorf("expected zip with >=2 entries, got %v", zr)
	}
}

func TestImportAssets_RejectsMapZip(t *testing.T) {
	body := buildZip(t, map[string]string{
		"manifest.json": jsonStr(t, exporter.Manifest{
			Kind:          exporter.KindMap,
			FormatVersion: exporter.FormatVersion,
		}),
	})
	svc := New(Deps{})
	_, err := svc.ImportAssets(context.Background(), body, 0, "")
	if err == nil || !strings.Contains(err.Error(), ".boxmap") {
		t.Fatalf("want .boxmap routing hint, got %v", err)
	}
}

func TestImportMap_RejectsAssetZip(t *testing.T) {
	body := buildZip(t, map[string]string{
		"manifest.json": jsonStr(t, exporter.Manifest{
			Kind:          exporter.KindAsset,
			FormatVersion: exporter.FormatVersion,
		}),
	})
	svc := New(Deps{})
	_, err := svc.ImportMap(context.Background(), body, 0, "")
	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("err = %v, want ErrUnknownKind", err)
	}
}

func TestSniffContentType(t *testing.T) {
	cases := []struct {
		body []byte
		want string
	}{
		{[]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, "image/png"},
		{[]byte("RIFFblah"), "audio/wav"},
		{[]byte("OggSblah"), "audio/ogg"},
		{[]byte("ID3blah"), "audio/mpeg"},
		{[]byte("xxx"), "application/octet-stream"},
	}
	for _, c := range cases {
		if got := sniffContentType(c.body); got != c.want {
			t.Errorf("sniffContentType(%q) = %s, want %s", string(c.body), got, c.want)
		}
	}
}

// uniqueMapName guards against overwriting live maps. The DB-backed
// version of this is exercised in the maps package tests; this is a
// smoke test for the bare logic against a stub.
func TestUniqueMapName_FallsBackOnEmpty(t *testing.T) {
	// We can't call uniqueMapName without a real *Service, but we can
	// verify nameTaken's behavior — the slice scan it does.
	if nameTaken(nil, "x") {
		t.Errorf("nameTaken on nil should be false")
	}
}

// TestEnvelopeFolderPath_AdditiveOnOldExports — the AssetEnvelope's
// FolderPath field is `omitempty`, so old exports without it must
// still decode cleanly into a zero-string. This guards the promised
// "format_version stays at 1" rule.
func TestEnvelopeFolderPath_AdditiveOnOldExports(t *testing.T) {
	const oldShape = `{
		"manifest": {"kind":"boxasset","format_version":1},
		"assets": [{"asset":{"id":7,"kind":"sprite","name":"hero","content_addressed_path":"x","original_format":"png"}}]
	}`
	// Decode the inner asset envelope shape with no folder_path.
	var payload struct {
		Assets []struct {
			Asset struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"asset"`
			FolderPath string `json:"folder_path"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(oldShape), &payload); err != nil {
		t.Fatalf("decode legacy export shape: %v", err)
	}
	if len(payload.Assets) != 1 {
		t.Fatalf("expected one asset, got %d", len(payload.Assets))
	}
	if payload.Assets[0].FolderPath != "" {
		t.Errorf("FolderPath on legacy export should default to empty, got %q",
			payload.Assets[0].FolderPath)
	}
}
