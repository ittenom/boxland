// Package setup is a tiny helper for inspecting whether a Boxland
// working tree has its embedded build artifacts in place.
//
// All five trees (fonts, vendor JS, templ, sqlc, flatc outputs) are
// committed to the repo, so Need() returns nil on every fresh clone.
// The check remains useful as a tripwire — if a generator output
// disappears (someone re-adds a stale gitignore rule, or hand-deletes
// a directory), the TLI shows a friendly first-run card and the test
// in tripwire_test.go fails CI before users hit the cryptic compile
// error.
//
// Need is detection only. Generator orchestration (sync-fonts, templ,
// sqlc, flatc) lives in cmd/boxland/setup.go so this package stays
// dependency-free.
package setup

import (
	"os"
	"path/filepath"
	"strings"
)

// Need reports which build prerequisites are missing on disk under
// repoRoot. An empty slice means the working tree is ready to compile
// and run; a non-empty slice means a generated output is missing and
// the TLI's friendly first-run card should fire.
func Need(repoRoot string) []string {
	cs := checks(repoRoot)
	var missing []string
	for _, c := range cs {
		if !dirHasExt(c.path, c.ext) {
			missing = append(missing, c.label)
		}
	}
	return missing
}

// Labels returns the full ordered list of step labels, matching what
// Need can return. Useful for tests and for rendering an inline
// checklist that highlights satisfied vs missing entries.
func Labels(repoRoot string) []string {
	cs := checks(repoRoot)
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.label
	}
	return out
}

type check struct {
	label string
	path  string
	// ext: when set, the directory must contain at least one entry
	// with this suffix. Empty ext means "directory must exist".
	ext string
}

func checks(repoRoot string) []check {
	return []check{
		{"fonts", filepath.Join(repoRoot, "server", "static", "fonts"), ".ttf"},
		{"vendor scripts", filepath.Join(repoRoot, "server", "static", "vendor"), ".js"},
		{"templ views", filepath.Join(repoRoot, "server", "views"), "_templ.go"},
		{"sqlc hot path", filepath.Join(repoRoot, "server", "internal", "persistence", "hotpath"), ".go"},
		{"flatbuffers code", filepath.Join(repoRoot, "server", "internal", "proto"), ".go"},
	}
}

// RequiredCmds is the canonical list of executables `boxland install`
// insists on finding before it can run. Both the CLI's install flow
// and the TLI's "is the install complete?" decision read from this
// list, so renames stay in lockstep.
func RequiredCmds() []string {
	return []string{"docker", "go", "node", "npm", "sqlc", "flatc"}
}

// dirHasExt reports whether path exists and (when ext is non-empty)
// contains at least one non-directory entry with that suffix.
func dirHasExt(path, ext string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	if ext == "" {
		return true
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			return true
		}
	}
	return false
}
