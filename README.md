# Boxland

Boxland is a designer-friendly MMORPG engine that makes it easy to turn on your server and invite players faster. Bring your 32x32 tiles, textures, and sprites (we recommend the wonderful [Aseprite](https://dacap.itch.io/aseprite) for asset generation) and define worlds with persistent places, instanced locations, and NPCs. Use Boxland's no-code event triggers and entity behaviors to make your creations interactive and alive.

## Quickstart

(1) Install these prerequisites:

- [Docker Desktop](https://www.docker.com/products/docker-desktop/)
- [Go](https://go.dev/dl/) 
- [Node](https://nodejs.org/) 
- Go-installed Boxland CLI (`go install ./server/cmd/boxland`) or `go run ./server/cmd/boxland` from this checkout

(2) Clone the Boxland repo to your device or server.

(3) From the boxland/ directory, run

```
boxland
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
boxland              # open the interactive terminal launch interface
boxland install      # check/install dependencies with official fallback links
boxland design       # dependencies, migrations, web build, staging, then server
boxland up           # Docker dependencies only
boxland down         # stop Docker dependencies
boxland serve        # run the Go server only
boxland test         # Go + TS tests + realm-isolation invariant
boxland migrate up   # run SQL migrations
boxland backup export ./backups/boxland.tar.gz
boxland backup import ./backups/boxland.tar.gz --yes
```

## License

MIT
