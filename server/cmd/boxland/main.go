// Boxland — server entrypoint.
//
// Subcommands (filled in across PLAN.md tasks):
//
//	boxland serve     run the game server + design tools (task #16+)
//	boxland migrate   run SQL migrations (task #17)
//	boxland seed      seed the database with development fixtures
//
// Currently a placeholder so the module compiles end-to-end while the rest
// of the server is built incrementally.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"boxland/server/internal/assets"
	"boxland/server/internal/auth/csrf"
	authdesigner "boxland/server/internal/auth/designer"
	authplayer "boxland/server/internal/auth/player"
	"boxland/server/internal/automations"
	"boxland/server/internal/backup"
	"boxland/server/internal/characters"
	"boxland/server/internal/config"
	designerhandlers "boxland/server/internal/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/flags"
	"boxland/server/internal/httpserver"
	"boxland/server/internal/hud"
	"boxland/server/internal/logging"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence"
	"boxland/server/internal/playerweb"
	"boxland/server/internal/publishing/artifact"
	"boxland/server/internal/settings"
	"boxland/server/internal/sim/runtime"
	"boxland/server/internal/tli"
	"boxland/server/internal/ws"
)

const Version = "0.0.0-dev"

func main() {
	logging.Init(logging.FromEnv())

	if len(os.Args) < 2 {
		if err := tli.RunAndExec(); err != nil {
			slog.Error("tli failed", "err", err)
			os.Exit(1)
		}
		return
	}
	switch os.Args[1] {
	case "install":
		if err := runInstall(); err != nil {
			slog.Error("install failed", "err", err)
			os.Exit(1)
		}
	case "setup":
		wd, _ := os.Getwd()
		if err := runSetupVerbose(wd); err != nil {
			slog.Error("setup failed", "err", err)
			os.Exit(1)
		}
	case "design":
		if err := runDesign(); err != nil {
			slog.Error("design failed", "err", err)
			os.Exit(1)
		}
	case "up":
		if err := runExternal("docker", "compose", "-f", filepath.Join("docker", "docker-compose.yml"), "up", "-d"); err != nil {
			slog.Error("up failed", "err", err)
			os.Exit(1)
		}
	case "down":
		if err := runExternal("docker", "compose", "-f", filepath.Join("docker", "docker-compose.yml"), "down"); err != nil {
			slog.Error("down failed", "err", err)
			os.Exit(1)
		}
	case "logs":
		if err := runExternal("docker", "compose", "-f", filepath.Join("docker", "docker-compose.yml"), "logs", "-f", "--tail=100"); err != nil {
			slog.Error("logs failed", "err", err)
			os.Exit(1)
		}
	case "serve":
		if err := runServe(); err != nil {
			slog.Error("serve failed", "err", err)
			os.Exit(1)
		}
	case "migrate":
		if err := runMigrate(os.Args[2:]); err != nil {
			slog.Error("migrate failed", "err", err)
			os.Exit(1)
		}
	case "backup":
		if err := runBackup(os.Args[2:]); err != nil {
			slog.Error("backup failed", "err", err)
			os.Exit(1)
		}
	case "test":
		if err := runTest(); err != nil {
			slog.Error("test failed", "err", err)
			os.Exit(1)
		}
	case "build-web":
		if err := runWeb("npm", "run", "build", "--silent"); err != nil {
			slog.Error("build-web failed", "err", err)
			os.Exit(1)
		}
	case "stage-web":
		if err := runExternal("node", filepath.Join("web", "scripts", "stage-web.mjs")); err != nil {
			slog.Error("stage-web failed", "err", err)
			os.Exit(1)
		}
	case "seed":
		slog.Info("subcommand not yet implemented", "cmd", "seed")
		os.Exit(1)
	case "version":
		fmt.Println(Version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: boxland [install|setup|design|up|down|logs|serve|migrate [up|down|version]|backup export|import|test|version]")
}

// installRequirements is the canonical list of system tools `boxland
// install` checks for. Extracted out of runInstall() so tests can
// assert it stays in sync with setup.RequiredCmds() (which the TLI
// reads to decide whether the install is complete).
func installRequirements() []installRequirement {
	return []installRequirement{
		{Name: "Docker Desktop", Cmd: "docker", VersionArgs: []string{"--version"}, URL: "https://www.docker.com/products/docker-desktop/", Packages: map[string]string{"winget": "Docker.DockerDesktop", "choco": "docker-desktop", "brew": "--cask docker", "apt": "docker.io docker-compose-plugin", "dnf": "docker docker-compose-plugin", "pacman": "docker docker-compose"}},
		{Name: "Go", Cmd: "go", VersionArgs: []string{"version"}, URL: "https://go.dev/dl/", Packages: map[string]string{"winget": "GoLang.Go", "choco": "golang", "brew": "go", "apt": "golang-go", "dnf": "golang", "pacman": "go"}},
		{Name: "Node.js", Cmd: "node", VersionArgs: []string{"--version"}, URL: "https://nodejs.org/", Packages: map[string]string{"winget": "OpenJS.NodeJS.LTS", "choco": "nodejs-lts", "brew": "node", "apt": "nodejs npm", "dnf": "nodejs npm", "pacman": "nodejs npm"}},
		{Name: "npm", Cmd: "npm", VersionArgs: []string{"--version"}, URL: "https://docs.npmjs.com/downloading-and-installing-node-js-and-npm", Packages: map[string]string{"winget": "OpenJS.NodeJS.LTS", "choco": "nodejs-lts", "brew": "node", "apt": "npm", "dnf": "npm", "pacman": "npm"}},
		// sqlc + flatc are required by `boxland setup` to regenerate
		// the embedded hot-path queries and FlatBuffers code on a
		// fresh clone. setup itself only warns when these are missing
		// (so a partial boot still works), but Install gives users
		// the one-shot path to a fully functional tree.
		{Name: "sqlc", Cmd: "sqlc", VersionArgs: []string{"version"}, URL: "https://docs.sqlc.dev/en/stable/overview/install.html", Packages: map[string]string{"winget": "sqlc-dev.sqlc", "brew": "sqlc", "pacman": "sqlc"}},
		{Name: "flatc", Cmd: "flatc", VersionArgs: []string{"--version"}, URL: "https://flatbuffers.dev/flatbuffers_guide_building.html", Packages: map[string]string{"winget": "Google.FlatBuffers", "choco": "flatc", "brew": "flatbuffers", "apt": "flatbuffers-compiler", "dnf": "flatbuffers-compiler", "pacman": "flatbuffers"}},
	}
}

func runInstall() error {
	fmt.Println("Boxland install checks")
	fmt.Println()
	// On macOS, Homebrew is the *only* package manager we know about.
	// If it's missing, every brew-using requirement below would fail
	// instantly. Bootstrap brew first so the rest of Install can use
	// it. (Windows has winget/choco fallbacks; Linux has apt/dnf/
	// pacman — neither needs this.)
	if err := ensureBrewOnMac(); err != nil {
		return err
	}
	reqs := installRequirements()
	// Walk every requirement and collect the unresolved ones rather
	// than aborting on the first failure. This way one Install run
	// surfaces every missing dependency, instead of forcing the user
	// to discover them one re-run at a time.
	var unresolved []string
	for _, r := range reqs {
		if err := ensureRequirement(r); err != nil {
			unresolved = append(unresolved, r.Name)
		}
	}
	if len(unresolved) > 0 {
		// Stop here. Anything below this point (npm install, go
		// build, setup) assumes the tools above are on PATH; without
		// them, the next step would explode with a confusing
		// "executable file not found" before the user has had time
		// to read why.
		return fmt.Errorf("install incomplete: %s could not be installed automatically. "+
			"Follow the printed links above, then re-run Install.",
			strings.Join(unresolved, ", "))
	}
	fmt.Println()
	fmt.Println("Installing web dependencies...")
	if err := runWeb("npm", "install", "--silent", "--no-audit", "--no-fund"); err != nil {
		return err
	}
	// Refresh embedded artifacts (fonts, templ, sqlc, flatc). On a
	// fresh clone these are already populated from the committed
	// generator outputs, so this step is fast and idempotent; after
	// a `git pull` that touched schemas/queries/templ it brings
	// generated code back in sync.
	fmt.Println()
	wd, _ := os.Getwd()
	if err := runSetupVerbose(wd); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("Building Boxland CLI to repo root and ./bin ...")
	if err := os.MkdirAll("bin", 0o755); err != nil {
		return err
	}
	rootOut := executableName("boxland")
	binOut := filepath.Join("bin", executableName("boxland"))
	if err := runExternal("go", "build", "-o", rootOut, "./server/cmd/boxland"); err != nil {
		return err
	}
	if err := runExternal("go", "build", "-o", binOut, "./server/cmd/boxland"); err != nil {
		return err
	}
	fmt.Printf("\nInstalled local CLI: ./%s\n", rootOut)
	fmt.Printf("Copy also available: %s\n", binOut)
	fmt.Println("Run `./boxland` from macOS/Linux, `boxland` from Windows, or add ./bin to PATH.")
	return nil
}

type installRequirement struct {
	Name        string
	Cmd         string
	VersionArgs []string
	URL         string
	Packages    map[string]string
}

// ensureRequirement returns nil when r is on PATH (already installed
// or just successfully installed by a package manager). It returns a
// non-nil error in two cases that callers must treat as fatal:
//
//   - no package manager available for the platform that handles r;
//   - every available package-manager attempt failed and r is still
//     not on PATH.
//
// Both cases still print a friendly URL the user can follow manually,
// but they no longer pretend the install step succeeded — that's the
// bug that let later steps (npm install, go build) explode with a
// cryptic "executable file not found" before the user could see
// what was actually missing.
func ensureRequirement(r installRequirement) error {
	if path, err := exec.LookPath(r.Cmd); err == nil {
		fmt.Printf("✓ %-15s %s\n", r.Name, path)
		_ = runExternal(r.Cmd, r.VersionArgs...)
		return nil
	}
	fmt.Printf("✗ %-15s missing\n", r.Name)
	attempts := installAttempts(r)
	if len(attempts) == 0 {
		fmt.Printf("  No supported package manager found. Install from %s\n", hyperlink(r.URL, r.URL))
		return fmt.Errorf("%s: no package manager available", r.Name)
	}
	for _, a := range attempts {
		fmt.Printf("  Trying: %s\n", strings.Join(a, " "))
		if err := runExternal(a[0], a[1:]...); err != nil {
			fmt.Printf("  Installer failed: %v\n", err)
			continue
		}
		if path, err := exec.LookPath(r.Cmd); err == nil {
			fmt.Printf("✓ %-15s %s\n", r.Name, path)
			return nil
		}
	}
	fmt.Printf("  Could not install automatically. Install from %s\n", hyperlink(r.URL, r.URL))
	return fmt.Errorf("%s: not installed after package-manager attempts", r.Name)
}

func installAttempts(r installRequirement) [][]string {
	candidates := packageManagersForPlatform()
	out := make([][]string, 0, len(candidates))
	for _, pm := range candidates {
		if _, err := exec.LookPath(pm); err != nil {
			continue
		}
		pkg := r.Packages[pm]
		if pkg == "" {
			continue
		}
		out = append(out, installCommand(pm, pkg))
	}
	return out
}

func packageManagersForPlatform() []string {
	switch goruntime.GOOS {
	case "windows":
		return []string{"winget", "choco"}
	case "darwin":
		return []string{"brew"}
	default:
		return []string{"brew", "apt", "dnf", "pacman"}
	}
}

func installCommand(pm, pkg string) []string {
	parts := strings.Fields(pkg)
	switch pm {
	case "winget":
		return []string{"winget", "install", "--id", pkg, "--exact", "--source", "winget", "--accept-package-agreements", "--accept-source-agreements"}
	case "choco":
		return []string{"choco", "install", pkg, "-y"}
	case "brew":
		return append([]string{"brew", "install"}, parts...)
	case "apt":
		return append([]string{"sudo", "apt-get", "install", "-y"}, parts...)
	case "dnf":
		return append([]string{"sudo", "dnf", "install", "-y"}, parts...)
	case "pacman":
		return append([]string{"sudo", "pacman", "-S", "--needed", "--noconfirm"}, parts...)
	default:
		return []string{pm, "install", pkg}
	}
}

// ensureBrewOnMac installs Homebrew on macOS when it's missing. On any
// other OS it's a no-op (Linux has apt/dnf/pacman; Windows has
// winget/choco). Returns a non-nil error if brew is required but the
// install fails — callers must treat this as fatal because the rest
// of the requirements loop relies on `brew` being on PATH.
//
// Implementation notes:
//
//   - We invoke the official non-interactive installer
//     (NONINTERACTIVE=1 ... install.sh) per Homebrew's documented
//     unattended path. The user will still be prompted for their
//     macOS admin password by sudo when brew sets up its prefix; we
//     run inside the TLI's interactive job slot, so the prompt
//     reaches the user's terminal.
//
//   - Newly-installed brew is at /opt/homebrew/bin (Apple Silicon)
//     or /usr/local/bin (Intel). Neither lives in the parent
//     process's PATH yet, so we prependPath the right one before
//     returning.
func ensureBrewOnMac() error {
	if goruntime.GOOS != "darwin" {
		return nil
	}
	if _, err := exec.LookPath("brew"); err == nil {
		return nil // already installed; nothing to do
	}
	fmt.Println("Homebrew is the macOS package manager Boxland uses to install")
	fmt.Println("the rest of its dependencies. Installing it now from brew.sh.")
	fmt.Println("(You may be prompted for your admin password.)")
	fmt.Println()

	cmd := exec.Command("/bin/bash", "-c",
		`curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh | NONINTERACTIVE=1 /bin/bash`)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Homebrew install failed: %w. Install manually from https://brew.sh and re-run Install.", err)
	}

	// Add the canonical brew bin to the in-process PATH so subsequent
	// exec.LookPath("brew") (and the per-requirement install attempts)
	// can find it without the user having to restart their shell.
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if _, err := os.Stat(filepath.Join(dir, "brew")); err == nil {
			prependPath(dir)
			break
		}
	}

	if _, err := exec.LookPath("brew"); err != nil {
		return fmt.Errorf("Homebrew installed but `brew` is not on PATH. Restart your shell and re-run Install.")
	}
	fmt.Println()
	fmt.Println("✓ Homebrew installed.")
	fmt.Println()
	return nil
}

// prependPath puts dir at the front of the in-process PATH env var so
// freshly-installed binaries become discoverable mid-run. Idempotent:
// if dir is already on PATH, it's a no-op.
func prependPath(dir string) {
	cur := os.Getenv("PATH")
	for _, existing := range strings.Split(cur, string(os.PathListSeparator)) {
		if existing == dir {
			return
		}
	}
	if cur == "" {
		_ = os.Setenv("PATH", dir)
		return
	}
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+cur)
}

func executableName(base string) string {
	if goruntime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func hyperlink(url, label string) string {
	return "\x1b]8;;" + url + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

func runBackup(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: boxland backup <export|import> <path> [--yes]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch args[0] {
	case "export":
		return backup.Export(ctx, cfg, args[1], backup.Options{Version: Version})
	case "import":
		yes := false
		for _, a := range args[2:] {
			if a == "--yes" {
				yes = true
			}
		}
		return backup.Import(ctx, cfg, args[1], yes, backup.Options{Version: Version})
	default:
		return fmt.Errorf("unknown backup subcommand %q", args[0])
	}
}

func runDesign() error {
	// Refresh generated artifacts before kicking the design loop.
	// Idempotent and instant when nothing changed; useful after a
	// `git pull` that touched schemas/queries/templ.
	wd, _ := os.Getwd()
	if err := runSetupVerbose(wd); err != nil {
		return err
	}

	// Re-invoke ourselves rather than relying on a `boxland` entry on
	// PATH. Works whether the user started the TLI via `go run`, the
	// repo-root `boxland.cmd`/`./boxland` launchers, or an installed
	// binary. Without this, Windows refuses to run `boxland` because
	// Go's exec.LookPath (since 1.19) no longer searches the cwd.
	self := boxlandSelf()
	steps := [][]string{
		{self, "up"},
		{self, "migrate", "up"},
		{"npm", "install", "--silent", "--no-audit", "--no-fund"},
		{"npm", "run", "build", "--silent"},
		{self, "stage-web"},
		{self, "serve"},
	}
	for _, step := range steps {
		var err error
		if step[0] == "npm" {
			err = runWeb(step[0], step[1:]...)
		} else {
			err = runExternal(step[0], step[1:]...)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// boxlandSelf returns the path to the currently-running boxland executable,
// falling back to the bare name "boxland" so a missing os.Executable still
// degrades to PATH lookup.
func boxlandSelf() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "boxland"
}

func runTest() error {
	steps := []func() error{
		func() error { return runServer("go", "test", "./...") },
		func() error { return runWeb("npm", "test") },
		func() error {
			return runServer("go", "test", "-count=1", "-run", "TestRealmIsolation_|TestSpectate_(SandboxInstance|PrivateMap)_", "./internal/ws/...")
		},
		func() error { return runExternal("node", filepath.Join("web", "scripts", "scripts.test.mjs")) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func runWeb(name string, args ...string) error { return runIn(filepath.Join("web"), name, args...) }
func runServer(name string, args ...string) error {
	return runIn(filepath.Join("server"), name, args...)
}
func runExternal(name string, args ...string) error { return runIn("", name, args...) }
func runIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runMigrate(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	sub := "up"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "up":
		return persistence.MigrateUp(cfg.DatabaseURL)
	case "down":
		return persistence.MigrateDown(cfg.DatabaseURL)
	case "version":
		v, dirty, err := persistence.MigrateVersion(cfg.DatabaseURL)
		if err != nil {
			return err
		}
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
		return nil
	default:
		return fmt.Errorf("unknown migrate subcommand: %s (want up|down|version)", sub)
	}
}

func runServe() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	slog.Info("boxland starting", "version", Version, "env", cfg.Env, "addr", cfg.HTTPAddr)

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pgPool, err := persistence.NewPool(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pgPool.Close()
	slog.Info("postgres connected")

	redisCli, err := persistence.NewRedis(rootCtx, cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer redisCli.Close()
	slog.Info("redis connected")

	objStore, err := persistence.NewObjectStore(rootCtx, persistence.ObjectStoreConfig{
		Endpoint:        cfg.S3Endpoint,
		Region:          cfg.S3Region,
		Bucket:          cfg.S3Bucket,
		AccessKeyID:     cfg.S3AccessKeyID,
		SecretAccessKey: cfg.S3SecretAccessKey,
		UsePathStyle:    cfg.S3UsePathStyle,
		PublicBaseURL:   cfg.S3PublicBaseURL,
	})
	if err != nil {
		return fmt.Errorf("object store: %w", err)
	}
	slog.Info("object store connected", "bucket", cfg.S3Bucket)

	authSvc := authdesigner.New(pgPool)
	playerAuthSvc := authplayer.New(pgPool, []byte(cfg.JWTSigningSecret))
	assetSvc := assets.New(pgPool)
	importerRegistry := assets.DefaultRegistry()
	// Wire the importer registry into the asset service so sprite
	// uploads auto-slice + synthesize walk_*/idle animations at
	// upload time. The designer-side upload handler already passes
	// the `kind` override; the service uses the registry only for
	// sprite kinds.
	assetSvc.Importers = importerRegistry
	bakeJob := assets.NewBakeJob(pgPool, objStore, assetSvc)

	componentRegistry := components.Default()
	entitySvc := entities.New(pgPool, componentRegistry)
	mapsSvc := mapsservice.New(pgPool)
	settingsSvc := settings.New(pgPool)
	charactersSvc := characters.New(pgPool)
	// Bake-on-publish needs the object store + asset service. Two-step
	// wiring lets the chrome / repo CRUD construct the Service without
	// pulling in the asset graph.
	charactersSvc.SetBakeDeps(objStore, assetSvc)
	// Player-side bakes attribute the new asset row to a "system"
	// designer because assets.created_by is NOT NULL FK to designers.
	// We pick the first existing designer at boot — typically the
	// realm owner. If none exists yet, player-side saves get a clear
	// "configure SystemDesignerID" error until a designer signs up.
	{
		var sysDesignerID int64
		if err := pgPool.QueryRow(rootCtx, `SELECT id FROM designers ORDER BY id ASC LIMIT 1`).Scan(&sysDesignerID); err == nil && sysDesignerID > 0 {
			charactersSvc.SetSystemDesignerID(sysDesignerID)
		} else if err != nil {
			slog.Warn("characters: no designer rows yet; player-side bakes will error until one exists", "err", err)
		}
	}

	publishRegistry := artifact.NewRegistry()
	publishRegistry.Register(assets.NewHandler(assetSvc))
	publishRegistry.Register(entities.NewHandler(entitySvc))
	publishRegistry.Register(mapsservice.NewHandler(mapsSvc))
	// Character generator artifacts. NPC-template publish runs the bake
	// pipeline inline (Phase 2); the other four kinds are pure metadata
	// updates to their live row.
	publishRegistry.Register(characters.NewSlotHandler(charactersSvc))
	publishRegistry.Register(characters.NewPartHandler(charactersSvc))
	publishRegistry.Register(characters.NewStatSetHandler(charactersSvc))
	publishRegistry.Register(characters.NewTalentTreeHandler(charactersSvc))
	publishRegistry.Register(characters.NewNpcTemplateHandler(charactersSvc))
	publishPipeline := artifact.NewPipeline(pgPool, publishRegistry)

	// Automation registries + persistence service. The two registries are
	// shared between the design tools (form renderer) and the runtime
	// compiler. Service writes/reads entity_automations.
	automationTriggers := automations.DefaultTriggers()
	automationActions := automations.DefaultActions()
	automationsSvc := automations.New(pgPool, automationTriggers, automationActions)

	// Per-realm extras: shared "common events" (callable trigger groups)
	// and per-realm flags (switches + variables). Used by the HUD editor
	// to populate the binding + action_group pickers, and by the publish
	// pipeline's HUD validator to cross-check references at publish time.
	actionGroupsRepo := automations.NewGroupsRepo(pgPool)
	flagsSvc := flags.New(pgPool)

	// HUD editor: per-realm widget catalog + repo. The widgets registry
	// is shared between the form renderer (descriptors → editor UI) and
	// the publish-time validator (one source of truth for kind → config).
	hudWidgets := hud.DefaultRegistry()
	hudRepo := &hud.Repo{Pool: pgPool}

	// Live game runtime: per-(map, instance) MapInstances live here. Any
	// JoinMap / DesignerCommand reaching the WS gateway gets routed
	// through this manager.
	//
	// SystemDeps wires the canonical per-instance system pipeline.
	// Animation system needs the asset catalog so it can look up the
	// `walk_<facing>`/`idle` clip for a given sprite.
	instanceMgr := runtime.NewInstanceManager(pgPool, redisCli.Client, mapsSvc, runtime.SystemDeps{
		Animations: &runtime.AssetsAnimationCatalog{Svc: assetSvc},
	})

	// Wire the publish pipeline's post-commit hook to broadcast a
	// LivePublish (HotSwap) to every running map. Each affected
	// entity-type outcome enqueues one HotSwap entry; the scheduler
	// drains them between ticks (PLAN.md §132 + §133).
	publishPipeline.OnPostCommit(func(_ context.Context, outcomes []artifact.PublishOutcome) error {
		for _, o := range outcomes {
			if o.Kind == "entity_type" {
				instanceMgr.BroadcastHotSwap(runtime.HotSwap{EntityTypeID: o.ArtifactID})
			}
		}
		return nil
	})

	// WS gateway: realm-tagged Auth handshake -> ClientMessage dispatch.
	// Both the default verb set (Heartbeat/Move/Interact stubs) and the
	// authoring verbs (PlaceTiles/EraseTiles/PlaceLighting + JoinMap
	// realbinding) register onto the same dispatcher.
	wsAuth := &ws.LiveAuthBackend{Player: playerAuthSvc, Designer: authSvc}
	wsDispatcher := ws.NewDispatcher()
	ws.RegisterDefaultVerbs(wsDispatcher)
	authoringDeps := ws.AuthoringDeps{
		MapsService: mapsSvc,
		Instances:   instanceMgr,
	}
	ws.RegisterAuthoringVerbs(wsDispatcher, authoringDeps)
	ws.RegisterSpectatorVerb(wsDispatcher, authoringDeps)
	wsGateway := ws.NewGateway(wsAuth, wsDispatcher, ws.Options{})
	defer wsGateway.CloseAll("server shutdown")

	csrfMW := csrf.Middleware(csrf.Config{
		Secure:   cfg.Env == "prod",
		SameSite: http.SameSiteStrictMode,
	})

	designerDeps := designerhandlers.Deps{
		Auth:               authSvc,
		Assets:             assetSvc,
		Entities:           entitySvc,
		Components:         componentRegistry,
		Maps:               mapsSvc,
		Importers:          importerRegistry,
		BakeJob:            bakeJob,
		PublishPipeline:    publishPipeline,
		ObjectStore:        objStore,
		Settings:           settingsSvc,
		Automations:        automationsSvc,
		AutomationTriggers: automationTriggers,
		AutomationActions:  automationActions,
		ActionGroups:       actionGroupsRepo,
		Flags:              flagsSvc,
		HUD:                hudRepo,
		HUDWidgets:         hudWidgets,
		Characters:         charactersSvc,
	}
	loadSessionMW := designerhandlers.LoadSession(designerDeps)
	// Order matters: CSRF must run on every request to mint the cookie;
	// LoadSession runs inside CSRF so handlers see both. Inside-out:
	//   csrfMW( loadSessionMW( designer routes ) )
	designerMount := csrfMW(loadSessionMW(designerhandlers.New(designerDeps)))

	playerWebDeps := playerweb.Deps{
		Auth:          playerAuthSvc,
		Maps:          mapsSvc,
		Settings:      settingsSvc,
		HUD:           hudRepo,
		Assets:        assetSvc, // /play/asset-catalog reads from this
		ObjectStore:   objStore, // CDN URLs for the asset catalog
		Characters:    charactersSvc,
		SecureCookies: cfg.Env == "prod",
		// WSURL left empty -> handlers derive ws://host/ws from the
		// request. Production deployments behind a reverse proxy can
		// override via cfg in a future revision.
	}
	playerLoadSessionMW := playerweb.LoadSession(playerWebDeps)
	playerMount := csrfMW(playerLoadSessionMW(playerweb.New(playerWebDeps)))

	rootHandler := httpserver.New(
		httpserver.Health{
			Postgres: pgPool, // *pgxpool.Pool implements Ping(context.Context) error
			Redis:    redisCli,
		},
		httpserver.Mounts{
			Designer: designerMount,
			Player:   playerMount,
			WS:       wsGateway.HTTPHandler(),
		},
	)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           rootHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("http listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-rootCtx.Done():
		slog.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}
	}

	// PLAN.md §140 graceful shutdown sequence:
	//   1. Stop accepting new HTTP connections (srv.Shutdown).
	//   2. Drain every live WS by closing them with StatusGoingAway.
	//   3. Per live MapInstance: flush in-memory state to Postgres +
	//      trim the WAL stream up to the flushed tick.
	//   4. Close pools.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slog.Info("graceful shutdown: stopping HTTP listener")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http shutdown", "err", err)
	}
	slog.Info("graceful shutdown: draining WS connections")
	wsGateway.CloseAll("server shutdown")

	// Flush every live MapInstance + trim its WAL. Failures are
	// logged + skipped so one slow instance can't deadlock shutdown.
	insts := instanceMgr.All()
	slog.Info("graceful shutdown: flushing live instances", "count", len(insts))
	for _, mi := range insts {
		if mi.Persister == nil {
			continue
		}
		if err := mi.Persister.Flush(shutdownCtx, mi.PersistFlushInputs()); err != nil {
			slog.Warn("graceful shutdown: persister flush",
				"map_id", mi.MapID, "instance_id", mi.InstanceID, "err", err)
		}
	}
	slog.Info("boxland stopped")
	return nil
}


