# Boxland — top-level task runner (https://just.systems).
# All recipes are stubs at this stage; later tasks fill them in.
# Recipes fail informatively when their tooling isn't wired yet.

# Cross-platform shell selection: PowerShell on Windows, sh elsewhere.
set windows-shell := ["powershell.exe", "-NoLogo", "-NoProfile", "-Command"]

# Default recipe: list available recipes.
default:
    @just --list

# Bring up the local dev dependencies (Postgres, Redis, Mailpit, MinIO).
up:
    docker compose -f docker/docker-compose.yml up -d

# Stop the local dev dependencies.
down:
    docker compose -f docker/docker-compose.yml down

# Tail logs from the dev stack.
logs:
    docker compose -f docker/docker-compose.yml logs -f --tail=100

# Run the Go server in dev mode. 'just up' should be running so the
# dependencies (Postgres, Redis, MinIO, Mailpit) are available.
[working-directory: 'server']
serve:
    go run ./cmd/boxland serve

# Run the web dev server. The Go server runs separately via 'just serve'.
# 'just up' should be running first so the dependencies are available.
[working-directory: 'web']
dev:
    npm run dev

# One-command Boxland: bring up the dependencies, build the web bundle,
# run migrations, and start the Go server. Designed for non-developers
# who just want to start using Boxland on their own machine.
#
# After this runs, the banner prints clickable links to the design
# tools + player game in the user's browser. Re-running is safe: each
# step is idempotent.
#
# Prereqs: Docker Desktop, Go, Node. The Docker stack hosts Postgres,
# Redis, Mailpit, and MinIO so users don't install any of those by hand.
#
# Cross-platform note: each step is a separate sub-recipe with its own
# `working-directory` attribute, so just spawns one shell per command
# without ever needing shell-specific `cd && cmd` chaining. Works
# under PowerShell 5.1 on Windows + sh on macOS/Linux without
# branching.
design: up _design-migrate _design-npm-install _design-npm-build _stage-web _design-banner _design-serve

[private]
[working-directory: 'server']
_design-migrate:
    go run ./cmd/boxland migrate up

[private]
[working-directory: 'web']
_design-npm-install:
    npm install --silent --no-audit --no-fund

[private]
[working-directory: 'web']
_design-npm-build:
    npm run build --silent

[private]
_design-banner:
    node web/scripts/banner.mjs

[private]
[working-directory: 'server']
_design-serve:
    go run ./cmd/boxland serve

# Internal: copy the freshly-built web bundle into the Go server's
# embed tree so /static/web/*.js resolves at runtime. The production
# Docker image does the same copy in its multi-stage build.
#
# Pure-Node implementation so the recipe is identical on every
# platform; requires Node (already a hard dep for the web bundle).
[private]
_stage-web:
    node web/scripts/stage-web.mjs

# Run Go + TS tests.
test: test-go test-web realm-isolation test-scripts

[working-directory: 'server']
test-go:
    # Tests use github.com/peterldowns/pgtestdb for isolation: each call
    # to testdb.New(t) returns a freshly-migrated, per-test PostgreSQL
    # database (clones a content-hashed template in ~10-20ms, dropped on
    # cleanup). Packages run safely in parallel — no -p 1 needed, and
    # individual tests can opt into t.Parallel() without coordination.
    go test ./...

[working-directory: 'web']
test-web:
    npm test

# PLAN.md §135 + §137: realm-isolation invariant test always runs in CI.
# Surfaces every cross-realm rejection scenario (player can't sandbox,
# designer can't dispatch sandbox-only on live, designer-only verbs
# refuse player realm). Failing means a regression in the WS gateway's
# isRealmViolation matcher or in the per-handler realm gate.
[working-directory: 'server']
realm-isolation:
    go test -count=1 -run 'TestRealmIsolation_|TestSpectate_(SandboxInstance|PrivateMap)_' ./internal/ws/...

# Smoke test the Node helper scripts just/CI rely on (banner, stage-web,
# sync-fonts). Pure-Node so it runs identically on Windows / macOS /
# Linux without a shell-specific test runner.
test-scripts:
    node web/scripts/scripts.test.mjs

# Run the Go linter (golangci-lint must be on PATH or installed via 'go install').
[working-directory: 'server']
lint:
    golangci-lint run ./...

# Typecheck the web project without producing build artifacts.
[working-directory: 'web']
typecheck:
    npm run typecheck

# Build production server binary + web bundle
build:
    @echo "[stub] just build: not yet implemented (see PLAN.md task #141)"
    @exit 1

# Run ECS microbenchmarks. The hard 1ms regression gate lives in the test
# suite (TestPerf_10kEntitiesTickUnder1ms); this recipe is for ad-hoc
# profiling with full stats.
#
# Note the package path uses 'boxland/server/internal/sim/...' rather than
# './internal/...' because PowerShell on Windows tries to glob-expand './'
# arguments in the calling shell before they reach `go test`. The fully-
# qualified module path bypasses that.
[working-directory: 'server']
bench:
    go test -benchmem -run nomatch '-bench=.' boxland/server/internal/sim/...

# Regenerate sqlc-typed hot-path queries from server/queries/.
[working-directory: 'server']
gen-sqlc:
    sqlc generate

# Regenerate Templ HTML templates -> typed Go from server/views/.
[working-directory: 'server']
gen-templ:
    templ generate -path views

# Sync canonical fonts from /shared/fonts/ into the server's static dir
# so they ship with the binary. /shared/fonts/ is the source of truth
# (used by the future iOS bundle too); server/static/fonts/ is a build
# artifact (gitignored). Pure-Node implementation -- identical on
# every platform.
sync-fonts:
    node web/scripts/sync-fonts.mjs

# Regenerate the placeholder 16x16 icon sprite sheet at server/static/icons/sprites.png.
# Replaced with hand-pixeled art before v1; for now ensures the templates
# have something to render. Build-tagged so it doesn't bloat normal builds.
[working-directory: 'server']
gen-icons:
    go run -tags=iconsgen ./static/icons

# Re-author /shared/test-vectors/collision.json by running each scenario
# through the web `move` implementation. Only run when intentionally
# rebuilding the cross-runtime corpus (rare); the file is hand-audited.
[working-directory: 'web']
author-collision:
    npx tsx src/collision/_author_vectors.ts > ../shared/test-vectors/collision.json
    @echo "Re-authored collision corpus."

# Regenerate FlatBuffers Go + TS code from /schemas/ using the pinned flatc Docker image.
gen-fb:
    @echo "Building pinned flatc image (cached on subsequent runs)..."
    docker build -q -f docker/flatc.Dockerfile -t boxland-flatc docker
    @echo "Generating Go bindings -> server/internal/proto/"
    docker run --rm -v "{{justfile_directory()}}:/work" boxland-flatc \
        --go --gen-onefile --go-namespace boxland.proto \
        -o /work/server/internal/proto \
        /work/schemas/world.fbs /work/schemas/input.fbs /work/schemas/design.fbs
    @echo "Generating TypeScript bindings -> web/src/net/proto/"
    docker run --rm -v "{{justfile_directory()}}:/work" boxland-flatc \
        --ts \
        -o /work/web/src/net/proto \
        /work/schemas/world.fbs /work/schemas/input.fbs /work/schemas/design.fbs
    @echo "Done. Generated outputs are .gitignored; regenerate via 'just gen-fb'."

# Run pending SQL migrations against the dev database.
[working-directory: 'server']
migrate:
    go run ./cmd/boxland migrate up

# Roll back the most recent migration.
[working-directory: 'server']
migrate-down:
    go run ./cmd/boxland migrate down

# Print the current migration version.
[working-directory: 'server']
migrate-version:
    go run ./cmd/boxland migrate version

# Seed the database with development fixtures
seed:
    @echo "[stub] just seed: not yet implemented"
    @exit 1

# Remove build artifacts
clean:
    @echo "[stub] just clean: nothing to clean yet"
