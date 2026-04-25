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

# Run Go + TS tests.
test: test-go test-web realm-isolation

[working-directory: 'server']
test-go:
    # -p 1 so test PACKAGES run sequentially. The integration tests share
    # a single dev Postgres instance and would clobber each other under
    # parallel execution. Within one package, individual tests still run
    # sequentially by default (no t.Parallel() anywhere yet).
    go test -p 1 ./...

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

# Sync canonical fonts from /shared/fonts/ into the server's static dir so
# they ship with the binary. /shared/fonts/ is the source of truth (used by
# the future iOS bundle too); server/static/fonts/ is a build artifact.
sync-fonts:
    powershell -NoProfile -Command "if (-not (Test-Path 'server/static/fonts')) { New-Item -ItemType Directory -Path 'server/static/fonts' | Out-Null }; Copy-Item -Force shared/fonts/*.ttf server/static/fonts/"

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
