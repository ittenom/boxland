package exporter

import (
	"strings"
	"testing"
	"time"
)

func TestSafeSlug(t *testing.T) {
	cases := map[string]string{
		"":                  "export",
		"hello":             "hello",
		"My Map":            "My-Map",
		"weird/path/../foo": "weird-path-foo",
		"😀 emoji 😀":         "emoji",
		"a b c":             "a-b-c",
		"---":               "export",
	}
	for in, want := range cases {
		if got := safeSlug(in); got != want {
			t.Errorf("safeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilenameFor(t *testing.T) {
	now := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)

	if got := FilenameFor(KindMap, "Town Square", now); got != "Town-Square.boxmap.zip" {
		t.Errorf("KindMap filename = %q", got)
	}
	if got := FilenameFor(KindAsset, "tree_a", now); got != "tree_a.boxasset.zip" {
		t.Errorf("KindAsset filename = %q", got)
	}
	got := FilenameFor(KindAllAssets, "", now)
	if !strings.Contains(got, "2026-04-27") || !strings.HasSuffix(got, ".boxassets.zip") {
		t.Errorf("KindAllAssets filename = %q", got)
	}
}

func TestManifestRoundtrip(t *testing.T) {
	// Lightweight sanity: make sure the constants resolve to the
	// expected stable strings (importer compares them by value).
	if KindAsset != "boxasset" || KindAllAssets != "boxassets" || KindMap != "boxmap" {
		t.Fatalf("manifest kind constants drifted: %s / %s / %s", KindAsset, KindAllAssets, KindMap)
	}
	if FormatVersion != 1 {
		t.Fatalf("format version drifted: %d", FormatVersion)
	}
}
