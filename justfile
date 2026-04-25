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
test: test-go test-web

[working-directory: 'server']
test-go:
    go test ./...

[working-directory: 'web']
test-web:
    npm test

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

# Run ECS microbenchmarks (CI-gated against regressions)
bench:
    @echo "[stub] just bench: not yet implemented (see PLAN.md task #75)"
    @exit 1

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
