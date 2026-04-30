package setup

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestNeedReportsAllMissingOnEmptyTree confirms the helper returns the
// full ordered label list when run against an empty directory.
func TestNeedReportsAllMissingOnEmptyTree(t *testing.T) {
	root := t.TempDir()
	missing := Need(root)
	want := Labels(root)
	if !slices.Equal(missing, want) {
		t.Fatalf("missing = %v, want %v", missing, want)
	}
}

// TestNeedSkipsSatisfiedDirs simulates a partially-bootstrapped tree:
// fonts and templ output are present, the rest aren't. Need must
// return only the absent ones, in declaration order.
func TestNeedSkipsSatisfiedDirs(t *testing.T) {
	root := t.TempDir()

	// fonts: a .ttf inside server/static/fonts
	mustWrite(t, filepath.Join(root, "server", "static", "fonts", "Probe.ttf"), []byte{0})
	// templ views: a *_templ.go inside server/views
	mustWrite(t, filepath.Join(root, "server", "views", "shell_templ.go"), []byte("package views\n"))

	got := Need(root)
	want := []string{"vendor scripts", "sqlc hot path", "flatbuffers code"}
	if !slices.Equal(got, want) {
		t.Fatalf("Need = %v, want %v", got, want)
	}
}

// TestNeedReturnsNilWhenAllPresent — happy path. Every required dir
// has at least one matching file.
func TestNeedReturnsNilWhenAllPresent(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"server/static/fonts/Probe.ttf":                      {0},
		"server/static/vendor/htmx.min.js":                   []byte("//"),
		"server/views/shell_templ.go":                        []byte("package views\n"),
		"server/internal/persistence/hotpath/queries.sql.go": []byte("package hotpath\n"),
		"server/internal/proto/Boxland.go":                   []byte("package proto\n"),
	}
	for rel, content := range files {
		mustWrite(t, filepath.Join(root, rel), content)
	}
	if missing := Need(root); len(missing) != 0 {
		t.Fatalf("expected no missing, got %v", missing)
	}
}

// TestLabelsIsStable — Labels must enumerate the same set Need can
// return, in the same order. This is a regression guard for the TUI's
// first-run card which lists missing items in the same order.
func TestLabelsIsStable(t *testing.T) {
	root := t.TempDir()
	labels := Labels(root)
	want := []string{"fonts", "vendor scripts", "templ views", "sqlc hot path", "flatbuffers code"}
	if !slices.Equal(labels, want) {
		t.Fatalf("Labels = %v, want %v", labels, want)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
