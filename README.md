# Boxland (Elixir)

A 2D MMORPG engine and design suite. This is the Elixir/Phoenix LiveView/Pixi rewrite of Boxland.

For the architecture and design rationale, see:
- `docs/superpowers/specs/2026-04-30-elixir-phoenix-pixi-foundation-design.md` (foundation spec)
- The Go reference repo at `/Users/cmonetti/boxland/` (legacy implementation, do not edit)

## Quick start (development)

Prerequisites:
- Elixir 1.17+ / OTP 27+
- Docker (for Postgres, Redis, MinIO)
- libvips (`brew install vips` on Mac)
- Node.js 20+ (for esbuild + ts-proto)
- `protoc` (`brew install protobuf` on Mac)
- `just` (`brew install just`)

Bring up local services and run the dev server:

```bash
cp .env.dev.example .env.dev
# Generate a secret: mix phx.gen.secret
# Edit .env.dev to set SECRET_KEY_BASE
set -a && source .env.dev && set +a

just dev-up           # docker compose up -d (postgres + redis + minio)
mix deps.get
mix ecto.create
mix ecto.migrate
just serve            # iex -S mix phx.server
```

Visit http://localhost:4000.

## Common commands

```bash
just                  # list all available recipes
just test             # run the test suite
just db-reset         # drop, create, and migrate the dev DB
just proto-gen        # regenerate Protobuf modules
just ci               # format check + credo + proto check + tests
```

## Project layout

```
lib/boxland/         — domain contexts (auth, library, maps, entities, levels, worlds, game, scripting)
lib/boxland_web/     — web layer (live, channels, components, controllers, proto)
lib/boxland_logic/   — built-in Lua action catalog
priv/repo/migrations — Ecto migration files
schemas/             — *.proto wire schemas (single source of truth)
assets/js/           — esbuild-bundled TypeScript (LiveView socket, hooks, Pixi modules)
assets/css/          — Tailwind sources
test/                — ExUnit + LiveView tests
```

## Production deploy

Pushed to `main` → Railway picks up the change → Docker build → BEAM release → auto-migrate on boot → healthcheck on `/healthz`.

## Docker distribution

Boxland ships as a Docker image plus a tiny launcher shell script.
Docker is already a Boxland prerequisite (the Install workflow uses
docker-compose to bring up Postgres + Redis + MinIO), so no additional
runtime dependency is added.

### Build locally

```bash
just build-image              # builds boxland:dev-test
```

The Dockerfile is multi-stage; first build downloads ~500MB of base images
and takes ~3-5 min. Subsequent builds use cache and complete in ~30s.

### Install (developer / first-run)

After building locally:

```bash
just install-launcher         # writes /usr/local/bin/boxland (sudo)
boxland                       # opens the TUI in a Docker container
```

The launcher (~20-line shell script at `bin/boxland`) takes care of:
- Creating the `boxland-net` Docker network
- Mounting `~/.boxland/` as a persistent data volume
- Mounting the Docker socket so Boxland's Install workflow can manage
  the dep services (postgres/redis/minio) on the host's Docker daemon
- Forwarding container port 4000 → host so http://localhost:4000 works

### Install (end-user — when an image registry is set up)

Future state once `boxland/boxland:VERSION` is published:

```bash
# Pull the image:
docker pull boxland/boxland:0.1.0

# Install the launcher (one-line curl install, future):
curl -fsSL https://boxland.app/install | sh

# Run:
boxland
```

For v1, end-user distribution is not yet wired up — push to a registry +
publish the install script land in a follow-up "Distribution" surface spec.

### Subcommands

```bash
boxland install               # non-interactive install (CI / scripts)
boxland run                   # foreground server (no TUI)
boxland --version
```

### Why Docker instead of a single static binary?

We considered Burrito (single-binary packager). Burrito's strict pin on
Zig 0.15.2 conflicts with current Homebrew Zig and creates ongoing
maintenance friction. Since Boxland already requires Docker for its
runtime dependencies, packaging Boxland itself in Docker is consistent
and removes a whole class of toolchain pain. See the TUI surface spec
for the full rationale.
