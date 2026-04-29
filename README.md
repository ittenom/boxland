```
   ██████╗   ██████╗  ██╗  ██╗ ██╗       █████╗  ███╗   ██╗ ██████╗
   ██╔══██╗ ██╔═══██╗ ╚██╗██╔╝ ██║      ██╔══██╗ ████╗  ██║ ██╔══██╗
   ██████╔╝ ██║   ██║  ╚███╔╝  ██║      ███████║ ██╔██╗ ██║ ██║  ██║
   ██╔══██╗ ██║   ██║  ██╔██╗  ██║      ██╔══██║ ██║╚██╗██║ ██║  ██║
   ██████╔╝ ╚██████╔╝ ██╔╝ ██╗ ███████╗ ██║  ██║ ██║ ╚████║ ██████╔╝
   ╚═════╝   ╚═════╝  ╚═╝  ╚═╝ ╚══════╝ ╚═╝  ╚═╝ ╚═╝  ╚═══╝ ╚═════╝
```

Boxland is a designer-friendly MMORPG engine that makes it easy to turn on your server and invite players faster. Bring your 32x32 tiles, textures, and sprites (we recommend the wonderful [Aseprite](https://dacap.itch.io/aseprite) for asset generation) and define worlds with persistent places, instanced locations, and NPCs. Use Boxland's no-code event triggers and entity behaviors to make your creations interactive and alive.

## Quickstart

(1) Install [Go](https://go.dev/doc/install) - Boxland can install everything else you need.

(2) Clone the Boxland repo to your home computer or your server.

(3) From the boxland/ directory, run:

```
go run ./server/cmd/boxland
```

(4) Do **Check Installation** from the menu to add any missing system dependencies (Node.js, Docker Desktop) and install the server code. 

(5) Select **Design** to start building your Boxland :)

After your first signup as the project owner, run `boxland seed` once
to import the bundled UI pack — buttons, frames, sliders, slots — as
first-class `ui` entities. They're shared by the design tool's
chrome and your in-game HUDs, so any tweak you make in the asset
manager updates both surfaces. The pack ships under CC BY 4.0; see
[LICENSE-THIRD-PARTY.md](LICENSE-THIRD-PARTY.md) for attribution.

(6) When the TLI shows a pink **Update available** banner, run
`boxland update` to pull the new release, run new migrations, and
rebuild. See [docs/updates.md](docs/updates.md) for details.

## System requirements

Boxland's server runs on any modern PC (of course, under multiplayer load, who knows?) Develop on Win/Mac/Linux or deploy to a Railway instance for quick-shipping public games.

## Warning

This is an early alpha with virtually no import/export features. Don't use it for anything important yet

## License

MIT
