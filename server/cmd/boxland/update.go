// Update orchestration for `boxland update`.
//
// The flow is split into two phases on purpose:
//
//	Phase 1 (this binary, pre-pull):
//	  guards (clean tree, on main, latest known) → backup → git pull
//	  → handoff to the post-pull binary via `go run`.
//
//	Phase 2 (post-pull, --resume):
//	  install (deps + codegen) → migrate up → web
//	  build + stage → done.
//
// Why two phases? After `git pull` the running binary is whatever
// version we started with. Generators (templ/sqlc/flatc), the
// install requirement list, and migration logic might all have moved
// in the new tree. By re-execing through `go run` against the freshly
// pulled source we let the *new* code drive the rest of the upgrade,
// which is the only way to safely cross arbitrary version jumps. The
// old binary is just a launcher for the new one.
//
// Safety nets:
//
//   - We refuse to clobber a dirty working tree (uncommitted local
//     changes would be lost on a complex merge). --force overrides.
//   - We refuse to update on a non-main branch unless --force is set,
//     because the user is probably mid-feature and a `git pull` would
//     be confusing.
//   - We snapshot the database to backups/pre-update-… before running
//     anything destructive. --no-backup skips this for CI; not
//     recommended for humans. The path is printed prominently so a
//     user who needs to roll back can copy-paste it.
//
// Failure recovery:
//
//   - If git pull fails, nothing changed; user can investigate.
//   - If install/migrate/web fails, the source tree is at the new
//     commit but the database may have been partially migrated.
//     Restore from the printed backup path:
//     boxland backup import backups/pre-update-….tar.gz --yes
//     and `git checkout <prev-commit>` to undo the source move.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"boxland/server/internal/backup"
	"boxland/server/internal/config"
	"boxland/server/internal/updater"
	bversion "boxland/server/internal/version"
)

// updateFlags is the parsed argv for `boxland update`.
type updateFlags struct {
	check    bool // --check: print status, exit 0; do not modify anything
	force    bool // --force: skip clean-tree / branch guards
	noBackup bool // --no-backup: skip pre-update DB snapshot (CI only)
	resume   bool // --resume: phase-2 entry point, internal flag
}

func parseUpdateFlags(args []string) (updateFlags, error) {
	var f updateFlags
	fs := flag.NewFlagSet("boxland update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.BoolVar(&f.check, "check", false, "print update status and exit; make no changes")
	fs.BoolVar(&f.force, "force", false, "skip clean-tree / branch guards")
	fs.BoolVar(&f.noBackup, "no-backup", false, "skip pre-update database snapshot (not recommended)")
	fs.BoolVar(&f.resume, "resume", false, "internal: run phase-2 (install + migrate + web) after git pull")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	return f, nil
}

// runUpdate is the entry point dispatched from main's switch on
// os.Args[1] == "update".
func runUpdate(args []string) error {
	f, err := parseUpdateFlags(args)
	if err != nil {
		return err
	}
	if f.resume {
		return runUpdateResume(f)
	}
	if f.check {
		return runUpdateCheck()
	}
	return runUpdatePrePull(f)
}

// runUpdateCheck prints the current/latest pair without modifying
// anything. Used by the TLI's "Check for updates" menu row and as a
// pre-flight sanity check users can invoke directly. Returns 0 even
// when an update IS available — the exit code reflects "did the
// check itself succeed", not "is the user up to date".
func runUpdateCheck() error {
	if updater.Disabled() {
		fmt.Println("Update checks are disabled (BOXLAND_DISABLE_UPDATE_CHECK=true).")
		fmt.Printf("Running version: %s\n", Version)
		return nil
	}
	cli := updater.NewClient(updater.DefaultRepo)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := cli.CheckLatest(ctx, updater.CheckOpts{ForceRefresh: true})
	if err != nil {
		// Don't fail the command on network errors — the user may be
		// offline and just wanted to know what we last saw.
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	if s == nil {
		fmt.Printf("Running version: %s (no update info available)\n", Version)
		return nil
	}
	fmt.Printf("Running version: %s\n", s.Current)
	if s.Latest == "" {
		fmt.Println("Latest release:  unknown")
		return nil
	}
	fmt.Printf("Latest release:  %s\n", s.Latest)
	if s.HasUpdate {
		fmt.Printf("\n  ▶ Update available. Run `boxland update` to apply it.\n")
		if s.ReleaseURL != "" {
			fmt.Printf("    Release notes: %s\n", s.ReleaseURL)
		}
	} else {
		fmt.Println("You're up to date. ✓")
	}
	return nil
}

// runUpdatePrePull is phase 1 of the update: every check and
// destructive operation that has to happen *before* git changes the
// source tree under our feet. After this function ends we re-exec
// the new binary in --resume mode.
func runUpdatePrePull(f updateFlags) error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return err
	}
	fmt.Printf("Boxland update — running from %s\n", repoRoot)
	fmt.Printf("Current version: %s\n\n", Version)

	// Step 1: guards. Cheap, non-destructive, run them all and
	// surface every problem before doing anything.
	if !f.force {
		if dirty, err := gitWorkingTreeDirty(repoRoot); err != nil {
			return fmt.Errorf("git status: %w", err)
		} else if dirty {
			return errors.New("working tree has uncommitted changes; commit/stash them or pass --force")
		}
		if branch, err := gitCurrentBranch(repoRoot); err != nil {
			return fmt.Errorf("git branch: %w", err)
		} else if branch != "main" {
			return fmt.Errorf("current branch is %q, not main; switch with `git checkout main` or pass --force", branch)
		}
	}

	// Step 2: ask GitHub what's new so the user sees what they're
	// updating to. We don't gate the rest of the flow on the answer
	// (a user with no network may legitimately want to pull a local
	// fork mirror), but we do log it.
	cli := updater.NewClient(updater.DefaultRepo)
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 10*time.Second)
	status, _ := cli.CheckLatest(checkCtx, updater.CheckOpts{ForceRefresh: true})
	checkCancel()
	if status != nil && status.Latest != "" {
		if status.HasUpdate {
			fmt.Printf("Updating to: %s\n", status.Latest)
			if status.ReleaseURL != "" {
				fmt.Printf("Release notes: %s\n", status.ReleaseURL)
			}
		} else {
			fmt.Printf("Already on latest (%s) — running update anyway to refresh local artifacts.\n", status.Latest)
		}
		fmt.Println()
	}

	// Step 3: backup. Best-effort: if the DB isn't reachable yet
	// (fresh clone, never migrated), skip with a warning rather than
	// blocking the update. This matches the "fail soft on infra"
	// pattern the rest of the CLI uses.
	if !f.noBackup {
		if path, err := preUpdateBackup(repoRoot, status); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pre-update backup skipped: %v\n", err)
			fmt.Fprintln(os.Stderr, "         continuing without a rollback snapshot.")
		} else {
			fmt.Printf("✓ Backup written to %s\n", path)
			fmt.Println("  To roll back the database after a bad update:")
			fmt.Printf("    boxland backup import %s --yes\n\n", path)
		}
	}

	// Step 4: pull. After this returns, the local source tree may
	// look completely different — every helper we call from here
	// must come from the new code, which means re-exec.
	fmt.Println("Pulling latest source...")
	if err := runExternal("git", "fetch", "--tags", "origin"); err != nil {
		return fmt.Errorf("git fetch: %w", err)
	}
	if err := runExternal("git", "pull", "--ff-only", "origin", "main"); err != nil {
		return fmt.Errorf("git pull: %w", err)
	}

	// Step 5: hand off to the new binary. `go run ./server/cmd/boxland`
	// compiles whatever's on disk now (post-pull) and runs phase 2.
	// We pass through --no-backup (already done by phase 1) and the
	// secret --resume flag so the new binary doesn't try to git-pull
	// a second time.
	fmt.Println()
	fmt.Println("Handing off to the freshly pulled CLI for install + migrate + web build...")
	resumeArgs := []string{"run", "./server/cmd/boxland", "update", "--resume", "--no-backup"}
	return runIn("server", "go", resumeArgs...)
}

// runUpdateResume is phase 2: invoked by phase 1 via `go run` against
// the post-pull source tree, so every helper here is the new code.
//
// Output is verbose by design — this is the high-stakes part of the
// upgrade and the user wants to see what's happening in real time.
func runUpdateResume(_ updateFlags) error {
	fmt.Println("Phase 2: installing dependencies, regenerating code, applying migrations.")
	fmt.Println()

	// install: deps + codegen. Idempotent and the authoritative way to
	// bring a working tree to a runnable state.
	if err := runInstall(); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	// migrate up: SQL ladder to the new schema head. Each migration
	// is wrapped in its own transaction by golang-migrate, so a bad
	// migration aborts cleanly.
	fmt.Println()
	fmt.Println("Applying database migrations...")
	if err := runMigrate([]string{"up"}); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}

	// web rebuild + stage. Skip-tolerant: if `npm run build` fails
	// the rest of the system still boots from any stale dist; we
	// surface the error so the user knows.
	fmt.Println()
	fmt.Println("Rebuilding web client...")
	if err := runWeb("npm", "run", "build", "--silent"); err != nil {
		return fmt.Errorf("web build: %w", err)
	}
	if err := runExternal("node", filepath.Join("web", "scripts", "stage-web.mjs")); err != nil {
		return fmt.Errorf("stage web: %w", err)
	}

	// Print MIGRATION_NOTES.md (the section for the new version, if
	// present). Best-effort: missing file or unparseable input is
	// not an error — the upgrade still succeeded.
	printMigrationNotes(bversion.Current())

	fmt.Println()
	fmt.Printf("✓ Boxland updated to %s.\n", bversion.Current())
	fmt.Println("  Restart any running `boxland serve` or `boxland design` to pick up the new build.")
	return nil
}

// preUpdateBackup writes a timestamped tarball into ./backups/ named
// for the from→to version pair so a user with a directory full of
// snapshots can tell at a glance which one to restore from.
func preUpdateBackup(repoRoot string, s *updater.Status) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	to := "next"
	if s != nil && s.Latest != "" {
		to = sanitizeForFilename(s.Latest)
	}
	from := sanitizeForFilename(Version)
	ts := time.Now().UTC().Format("20060102-150405")
	dst := filepath.Join(repoRoot, "backups",
		fmt.Sprintf("pre-update-%s-to-%s-%s.tar.gz", from, to, ts))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := backup.Export(ctx, cfg, dst, backup.Options{Version: Version}); err != nil {
		return "", err
	}
	return dst, nil
}

// printMigrationNotes scans MIGRATION_NOTES.md for a `## v<version>`
// heading and prints the markdown beneath it. We accept either
// `vX.Y.Z` or bare `X.Y.Z` headings to be lenient. Missing or empty
// file is silently ignored — a release without notes is fine.
func printMigrationNotes(version string) {
	candidates := []string{
		filepath.Join("MIGRATION_NOTES.md"),
		filepath.Join("..", "MIGRATION_NOTES.md"),
	}
	var data []byte
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err == nil {
			data = b
			break
		}
	}
	if len(data) == 0 {
		return
	}
	wantA := "## v" + version
	wantB := "## " + version
	lines := strings.Split(string(data), "\n")
	start := -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == wantA || t == wantB {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "## ") || strings.HasPrefix(t, "---") {
			end = i
			break
		}
	}
	body := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	if body == "" {
		return
	}
	fmt.Println()
	fmt.Println("──────────────────────────────────────────────────────────────")
	fmt.Printf(" Notes for v%s\n", version)
	fmt.Println("──────────────────────────────────────────────────────────────")
	fmt.Println(body)
	fmt.Println("──────────────────────────────────────────────────────────────")
}

// gitWorkingTreeDirty reports whether `git status --porcelain` has
// any output. `--porcelain` is the stable, machine-readable format
// (untracked + modified + staged) so any non-empty result means the
// user has *something* uncommitted.
func gitWorkingTreeDirty(repoRoot string) (bool, error) {
	cmd := exec.Command("git", "-C", repoRoot, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}

// gitCurrentBranch returns the active branch name. In a detached-HEAD
// state (release tag checkout, mid-rebase, etc.) git prints "HEAD"
// for the symbolic ref; we pass that through and let the caller
// decide whether to treat it as off-main.
func gitCurrentBranch(repoRoot string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "symbolic-ref", "--short", "-q", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		// Detached HEAD; fall back to commit short-SHA.
		shaCmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD")
		sha, shaErr := shaCmd.Output()
		if shaErr != nil {
			return "", err
		}
		return "detached@" + strings.TrimSpace(string(sha)), nil
	}
	return strings.TrimSpace(string(out)), nil
}

// sanitizeForFilename strips characters that would make a path
// awkward to type or paste back into a shell. SemVer is friendly,
// but a `+build.metadata` suffix would survive untouched and that's
// fine.
func sanitizeForFilename(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, " ", "-")
	if s == "" {
		return "unknown"
	}
	return s
}
