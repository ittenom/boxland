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
	"os/signal"
	"syscall"
	"time"

	"boxland/server/internal/assets"
	"boxland/server/internal/auth/csrf"
	authdesigner "boxland/server/internal/auth/designer"
	authplayer "boxland/server/internal/auth/player"
	"boxland/server/internal/automations"
	"boxland/server/internal/config"
	designerhandlers "boxland/server/internal/designer"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/httpserver"
	"boxland/server/internal/logging"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/persistence"
	"boxland/server/internal/playerweb"
	"boxland/server/internal/publishing/artifact"
	"boxland/server/internal/settings"
	"boxland/server/internal/sim/runtime"
	"boxland/server/internal/ws"
)

const Version = "0.0.0-dev"

func main() {
	logging.Init(logging.FromEnv())

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
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
	fmt.Fprintln(os.Stderr, "usage: boxland <serve|migrate [up|down|version]|seed|version>")
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
	bakeJob := assets.NewBakeJob(pgPool, objStore, assetSvc)

	componentRegistry := components.Default()
	entitySvc := entities.New(pgPool, componentRegistry)
	mapsSvc := mapsservice.New(pgPool)
	settingsSvc := settings.New(pgPool)

	publishRegistry := artifact.NewRegistry()
	publishRegistry.Register(assets.NewHandler(assetSvc))
	publishRegistry.Register(entities.NewHandler(entitySvc))
	publishRegistry.Register(mapsservice.NewHandler(mapsSvc))
	publishPipeline := artifact.NewPipeline(pgPool, publishRegistry)

	// Automation registries + persistence service. The two registries are
	// shared between the design tools (form renderer) and the runtime
	// compiler. Service writes/reads entity_automations.
	automationTriggers := automations.DefaultTriggers()
	automationActions := automations.DefaultActions()
	automationsSvc := automations.New(pgPool, automationTriggers, automationActions)

	// Live game runtime: per-(map, instance) MapInstances live here. Any
	// JoinMap / DesignerCommand reaching the WS gateway gets routed
	// through this manager.
	instanceMgr := runtime.NewInstanceManager(pgPool, redisCli.Client, mapsSvc)

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
