// setup.go — first-run preparation for a fresh Boxland clone.
//
// Five paths in this repo are gitignored build artifacts that the Go
// embed and import tree depend on. Without them, `go build` fails
// before the binary is even produced — the most visible symptom is the
// classic
//
//	server/static/embed.go:18:20: pattern all:fonts: no matching files found
//
// from the missing /server/static/fonts/ directory.
//
// Setup populates all five from sources already in the repo:
//
//   - server/static/fonts/                ← shared/fonts/*.ttf via sync-fonts.mjs
//   - server/static/vendor/               ← committed; only the dir presence is checked
//   - server/views/*_templ.go             ← `templ generate` against ./views
//   - server/internal/persistence/hotpath ← `sqlc generate` against server/queries/
//   - server/internal/proto/              ← `flatc` against /schemas/
//
// Each step is idempotent: it inspects the target and skips when the
// output is already up to date. Re-running setup after a `git pull`
// should be cheap.
//
// `sqlc` and `flatc` are not part of the Go module graph; if they
// aren't on PATH we print a one-line "install ... to enable" message
// and skip that step, so users can still get a partial boot if they
// only need the design tools (templ + fonts + vendor).

package boxlandcmd

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// setupResult captures one step's outcome. The TUI's first-run prompt
// renders these as a checklist so the user sees what changed.
type setupResult struct {
	Step   string // human label, e.g. "fonts"
	Status setupStatus
	Detail string // one-line context (count, skip reason, error message)
}

type setupStatus int

const (
	setupRan setupStatus = iota
	setupSkipped
	setupFailed
)

// runSetup runs the five steps in order from the given repo root,
// returning a structured result per step. The caller decides whether
// to hard-stop on the first failure or report and continue. Today we
// report and continue: a missing flatc shouldn't block the user from
// running the design tools that don't need it.
func runSetup(repoRoot string) []setupResult {
	return []setupResult{
		stepFonts(repoRoot),
		stepVendor(repoRoot),
		stepTempl(repoRoot),
		stepSqlc(repoRoot),
		stepFlatc(repoRoot),
	}
}

// runSetupVerbose drives runSetup and prints a one-line summary per
// step to stdout. Used by the CLI subcommand and by Install.
func runSetupVerbose(repoRoot string) error {
	fmt.Println("Boxland setup")
	fmt.Println()
	results := runSetup(repoRoot)
	var failed int
	for _, r := range results {
		fmt.Println("  " + summarizeSetup(r))
		if r.Status == setupFailed {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("setup completed with %d failure(s)", failed)
	}
	return nil
}

// summarizeSetup produces a single-line "✓ fonts  copied 11 ttf"
// summary. Symbol carries the status; the label is left-padded so
// columns line up across steps.
func summarizeSetup(r setupResult) string {
	mark := "✓"
	switch r.Status {
	case setupSkipped:
		mark = "•"
	case setupFailed:
		mark = "✗"
	}
	return fmt.Sprintf("%s %-18s %s", mark, r.Step, r.Detail)
}

// ---------------------------------------------------------------------------
// Step: fonts. Copy /shared/fonts/*.ttf -> /server/static/fonts/.
// ---------------------------------------------------------------------------

func stepFonts(repoRoot string) setupResult {
	src := filepath.Join(repoRoot, "shared", "fonts")
	dst := filepath.Join(repoRoot, "server", "static", "fonts")

	if !pathExists(src) {
		return setupResult{Step: "fonts", Status: setupFailed,
			Detail: "missing source dir " + src}
	}

	if upToDate, n := fontsUpToDate(src, dst); upToDate {
		return setupResult{Step: "fonts", Status: setupSkipped,
			Detail: fmt.Sprintf("%d ttf already current", n)}
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return setupResult{Step: "fonts", Status: setupFailed, Detail: err.Error()}
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return setupResult{Step: "fonts", Status: setupFailed, Detail: err.Error()}
	}
	copied := 0
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".ttf") {
			continue
		}
		if err := copyFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return setupResult{Step: "fonts", Status: setupFailed, Detail: err.Error()}
		}
		copied++
	}
	return setupResult{Step: "fonts", Status: setupRan,
		Detail: fmt.Sprintf("copied %d ttf", copied)}
}

// fontsUpToDate returns (true, count) when every .ttf in src has a dst
// twin with mtime ≥ src's. count is the total number of .ttf files in
// src so the skip line can still report something useful.
func fontsUpToDate(src, dst string) (bool, int) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return false, 0
	}
	total := 0
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".ttf") {
			continue
		}
		total++
		srcInfo, err := os.Stat(filepath.Join(src, e.Name()))
		if err != nil {
			return false, total
		}
		dstInfo, err := os.Stat(filepath.Join(dst, e.Name()))
		if err != nil || dstInfo.ModTime().Before(srcInfo.ModTime()) {
			return false, total
		}
	}
	return total > 0, total
}

// ---------------------------------------------------------------------------
// Step: vendor scripts. The minified third-party JS lives in
// /server/static/vendor/ and is committed to git, so this step only
// checks that the dir is populated. If it isn't, the user needs to
// re-clone or pull.
// ---------------------------------------------------------------------------

func stepVendor(repoRoot string) setupResult {
	dir := filepath.Join(repoRoot, "server", "static", "vendor")
	count := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".js") {
			count++
		}
		return nil
	})
	if count == 0 {
		return setupResult{Step: "vendor scripts", Status: setupFailed,
			Detail: dir + " is empty; pull the latest commit"}
	}
	return setupResult{Step: "vendor scripts", Status: setupSkipped,
		Detail: fmt.Sprintf("%d js already present", count)}
}

// ---------------------------------------------------------------------------
// Step: templ generate. Available as a Go tool dependency
// (`tool github.com/a-h/templ/cmd/templ` in server/go.mod), so we run
// it via `go tool` — no host install required.
// ---------------------------------------------------------------------------

func stepTempl(repoRoot string) setupResult {
	srcDir := filepath.Join(repoRoot, "server", "views")
	if newest, ok := newestWithExt(srcDir, ".templ"); ok {
		if oldest, ok := oldestWithExt(srcDir, "_templ.go"); ok && !oldest.Before(newest) {
			return setupResult{Step: "templ views", Status: setupSkipped,
				Detail: "all generated files current"}
		}
	}
	cmd := exec.Command("go", "tool", "github.com/a-h/templ/cmd/templ", "generate", "./views")
	cmd.Dir = filepath.Join(repoRoot, "server")
	if out, err := cmd.CombinedOutput(); err != nil {
		return setupResult{Step: "templ views", Status: setupFailed,
			Detail: trimErr(string(out), err)}
	}
	return setupResult{Step: "templ views", Status: setupRan,
		Detail: "regenerated *_templ.go"}
}

// ---------------------------------------------------------------------------
// Step: sqlc generate. Not in go.mod (it's a separate tool); skip with
// a friendly note when missing.
// ---------------------------------------------------------------------------

func stepSqlc(repoRoot string) setupResult {
	if _, err := exec.LookPath("sqlc"); err != nil {
		return setupResult{Step: "sqlc hot path", Status: setupSkipped,
			Detail: "sqlc not installed (run Install to add it)"}
	}
	queries := filepath.Join(repoRoot, "server", "queries")
	if newest, ok := newestWithExt(queries, ".sql"); ok {
		gen := filepath.Join(repoRoot, "server", "internal", "persistence", "hotpath")
		if oldest, ok := oldestWithExt(gen, ".go"); ok && !oldest.Before(newest) {
			return setupResult{Step: "sqlc hot path", Status: setupSkipped,
				Detail: "generated code current"}
		}
	}
	cmd := exec.Command("sqlc", "generate", "-f", "sqlc.yaml")
	cmd.Dir = filepath.Join(repoRoot, "server")
	if out, err := cmd.CombinedOutput(); err != nil {
		return setupResult{Step: "sqlc hot path", Status: setupFailed,
			Detail: trimErr(string(out), err)}
	}
	return setupResult{Step: "sqlc hot path", Status: setupRan, Detail: "regenerated"}
}

// ---------------------------------------------------------------------------
// Step: flatc generate. Same friendly-skip pattern as sqlc.
// ---------------------------------------------------------------------------

func stepFlatc(repoRoot string) setupResult {
	if _, err := exec.LookPath("flatc"); err != nil {
		return setupResult{Step: "flatbuffers code", Status: setupSkipped,
			Detail: "flatc not installed (run Install to add it)"}
	}
	schemas := filepath.Join(repoRoot, "schemas")
	if newest, ok := newestWithExt(schemas, ".fbs"); ok {
		gen := filepath.Join(repoRoot, "server", "internal", "proto")
		if oldest, ok := oldestWithExt(gen, ".go"); ok && !oldest.Before(newest) {
			return setupResult{Step: "flatbuffers code", Status: setupSkipped,
				Detail: "generated code current"}
		}
	}
	out := filepath.Join(repoRoot, "server", "internal", "proto")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return setupResult{Step: "flatbuffers code", Status: setupFailed, Detail: err.Error()}
	}
	cmd := exec.Command("flatc", "--go", "--gen-onefile", "-o", out, "schemas/")
	cmd.Dir = repoRoot
	if combined, err := cmd.CombinedOutput(); err != nil {
		return setupResult{Step: "flatbuffers code", Status: setupFailed,
			Detail: trimErr(string(combined), err)}
	}
	return setupResult{Step: "flatbuffers code", Status: setupRan, Detail: "regenerated"}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}

// newestWithExt returns the most recent mtime among files matching
// ext directly under dir (non-recursive). ok is false when no match
// exists.
func newestWithExt(dir, ext string) (time.Time, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, false
	}
	var newest time.Time
	var ok bool
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !ok || info.ModTime().After(newest) {
			newest = info.ModTime()
			ok = true
		}
	}
	return newest, ok
}

// oldestWithExt is the inverse: oldest mtime among matches. We use it
// against generated files so that "newest source > oldest output"
// triggers a regeneration.
func oldestWithExt(dir, ext string) (time.Time, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, false
	}
	var oldest time.Time
	var ok bool
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !ok || info.ModTime().Before(oldest) {
			oldest = info.ModTime()
			ok = true
		}
	}
	return oldest, ok
}

// trimErr formats a generator error in one short line, preferring the
// last non-empty line of stdout/stderr (where most CLIs put the real
// error) and falling back to err.Error() when the output is empty.
func trimErr(out string, err error) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return err.Error()
	}
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			if len(s) > 200 {
				s = s[:199] + "…"
			}
			return s
		}
	}
	return err.Error()
}
