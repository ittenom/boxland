# Boxland

Boxland is a designer-friendly MMORPG engine that makes it easy to turn on your server and invite players faster. Bring your 32x32 tiles, textures, and sprites (we recommend the wonderful [Aseprite](https://dacap.itch.io/aseprite) for asset generation) and define worlds with persistent places, instanced locations, and NPCs. Use Boxland's no-code event triggers and entity behaviors to make your creations interactive and alive.

## Quickstart

(1) Install these prerequisites:

- [Docker Desktop](https://www.docker.com/products/docker-desktop/)
- [Go](https://go.dev/dl/) 
- [Node](https://nodejs.org/) 
- [Just](https://just.systems)

(2) Clone the Boxland repo to your device or server.

(3) From the boxland/ directory, run

```
just design
```

This works the same on Windows, macOS, and Linux. Boxland will provision a virtual machine with Postgres, Redis, Mailpit, and MinIO services. Then it sets up a database, runs the web server, runs the game server, and if everything works you'll see this:

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

## License

MIT
