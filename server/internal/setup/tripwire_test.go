package setup_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"boxland/server/internal/setup"
)

// TestRepoEmbedDirsArePopulated is a tripwire: if anyone gitignores
// another generated tree without wiring it into setup, or if the
// existing generators stop running, this test fails loudly inside the
// repo's own CI before users hit the cryptic compile error.
//
// Test resolves the repo root from this file's path so it doesn't
// depend on cwd.
func TestRepoEmbedDirsArePopulated(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// state_test.go is at <repo>/server/internal/setup/, so step up 4.
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(repoRoot, "go.work")); err != nil {
		t.Fatalf("expected go.work at inferred repo root %s: %v", repoRoot, err)
	}
	missing := setup.Need(repoRoot)
	if len(missing) > 0 {
		t.Fatalf("repo is missing build prerequisites %v — run `boxland setup` to regenerate, then check this in", missing)
	}
}
