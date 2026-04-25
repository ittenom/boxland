# Boxland

A 2D MMORPG engine and design suite. Authoritative Go server, thin Pixi-based web client, Templ+HTMX design tools. 32-pixel pixel-art aesthetic, zero vector curves.

> Working name. Native iOS client deferred to v1.1; v1 ships web-only and is iOS-protocol-ready.

## Status

Pre-alpha. See [PLAN.md](./PLAN.md) for the full architecture, decisions, and the linearized task list (§13) currently being executed.

## Layout

```
server/      Go monolith (single binary, multiple subcommands)
web/         TypeScript modules (game client, Mapmaker, Sandbox, pixel editor, design-tool widgets)
schemas/     FlatBuffers .fbs files — single source of truth for the wire protocol
shared/      Cross-runtime assets: default fonts, palettes, collision test vectors
docker/      Dockerfile, docker-compose, pinned flatc build image
```

## Prerequisites

- [Just](https://just.systems) — task runner. Install via `winget install Casey.Just`, `brew install just`, `cargo install just`, or your distro's package manager.
- Go 1.22+ (`winget install GoLang.Go`)
- Node 20+ (for the web build)
- Docker + Docker Compose (for the local dev stack)
- Optional: [`golangci-lint`](https://golangci-lint.run) for `just lint` (`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`)

## Quickstart

Install [Docker Desktop](https://www.docker.com/products/docker-desktop/),
[Go](https://go.dev/dl/), [Node](https://nodejs.org/), and
[Just](https://just.systems). Then run:

```
just design
```

That single command brings up Postgres + Redis + Mailpit + MinIO in
Docker, runs migrations, builds the web bundle, and starts the Go
server. If everything works, you'll see this:

```
   ██████╗   ██████╗  ██╗  ██╗ ██╗       █████╗  ███╗   ██╗ ██████╗
   ██╔══██╗ ██╔═══██╗ ╚██╗██╔╝ ██║      ██╔══██╗ ████╗  ██║ ██╔══██╗
   ██████╔╝ ██║   ██║  ╚███╔╝  ██║      ███████║ ██╔██╗ ██║ ██║  ██║
   ██╔══██╗ ██║   ██║  ██╔██╗  ██║      ██╔══██║ ██║╚██╗██║ ██║  ██║
   ██████╔╝ ╚██████╔╝ ██╔╝ ██╗ ███████╗ ██║  ██║ ██║ ╚████║ ██████╔╝
   ╚═════╝   ╚═════╝  ╚═╝  ╚═╝ ╚══════╝ ╚═╝  ╚═╝ ╚═╝  ╚═══╝ ╚═════╝

  Design tools  →  http://localhost:8080/design/login
  Game client   →  http://localhost:8080/play/login
  Health check  →  http://localhost:8080/healthz
```

### Other recipes

```
just                   # list available recipes
just up                # just the Docker dependencies (no server)
just down              # stop the Docker dependencies
just serve             # run the Go server only (you build the web bundle yourself)
just dev               # Vite dev server (HMR-friendly TS edits; expects `just serve` running)
just test              # Go + TS tests + the realm-isolation invariant
just build             # Production server binary + web bundle
just bench             # ECS microbenchmarks (regression-gated)
just gen-fb            # Regenerate FlatBuffers Go + TS code from /schemas/
just migrate           # Run SQL migrations
```

## Documentation

- **PLAN.md** — architecture, locked decisions, task list
- `docs/hotkeys.md` — hotkey reference (added in task #36)
- `schemas/collision.md` — canonical swept-AABB pseudocode (the cross-runtime contract)

## License

TBD.
