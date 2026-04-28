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

	cases := []struct {
		kind, slug, want string
	}{
		{KindMap, "Town Square", "Town-Square.boxmap.zip"},
		{KindAsset, "tree_a", "tree_a.boxasset.zip"},
		{KindTilemap, "Forest", "Forest.boxtilemap.zip"},
		{KindLevel, "Town day", "Town-day.boxlevel.zip"},
		{KindWorld, "Mainland", "Mainland.boxworld.zip"},
	}
	for _, c := range cases {
		if got := FilenameFor(c.kind, c.slug, now); got != c.want {
			t.Errorf("FilenameFor(%s, %q) = %q, want %q", c.kind, c.slug, got, c.want)
		}
	}
	got := FilenameFor(KindAllAssets, "", now)
	if !strings.Contains(got, "2026-04-27") || !strings.HasSuffix(got, ".boxassets.zip") {
		t.Errorf("KindAllAssets filename = %q", got)
	}
}

func TestManifestKindsStable(t *testing.T) {
	// Stable strings: the importer compares by value. If anything below
	// drifts, every previously-exported zip becomes unrecognized.
	cases := []struct{ got, want string }{
		{KindAsset, "boxasset"},
		{KindAllAssets, "boxassets"},
		{KindTilemap, "boxtilemap"},
		{KindMap, "boxmap"},
		{KindLevel, "boxlevel"},
		{KindWorld, "boxworld"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("kind constant drifted: got %q, want %q", c.got, c.want)
		}
	}
	if FormatVersion != 2 {
		t.Fatalf("format version drifted: %d", FormatVersion)
	}
}
