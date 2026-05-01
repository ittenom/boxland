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
