# Boxland — Elixir + Phoenix LiveView + Pixi Foundation Design

**Status:** Approved (foundation spec — surface specs to follow)
**Date:** 2026-04-30
**Scope:** Architectural foundation for a greenfield rewrite of the Boxland server and web client in Elixir/Phoenix LiveView/Pixi. Establishes tech stack, repo shape, data model, integration patterns, wire protocol, scripting layer, auth, asset pipeline, and build/deploy. Does NOT design any individual feature surface — those get their own brainstorm → design → plan → implement cycle in the order listed in §9.

---

## Table of contents

1. [Architecture overview](#1-architecture-overview)
2. [Data model](#2-data-model)
3. [LiveView ↔ Pixi integration pattern](#3-liveview--pixi-integration-pattern)
4. [Wire protocol & channel topology](#4-wire-protocol--channel-topology)
5. [Scripting layer (Luerl)](#5-scripting-layer-luerl)
6. [Auth (designer + player realms)](#6-auth-designer--player-realms)
7. [Asset pipeline](#7-asset-pipeline)
8. [Build & deploy](#8-build--deploy)
9. [Surface spec decomposition](#9-surface-spec-decomposition)

---

## Locked decisions (foundation-level)

| Decision | Choice |
|---|---|
| Migration topology | **Greenfield rewrite.** Existing Go repo is reference only. |
| Wire format | **Protocol Buffers 3** over Phoenix.Channel for game; native LiveView events for design surfaces |
| Simulation runtime | **Pure Elixir.** GenServer-per-instance, ECS-style storage in process state. No NIFs in v1. Architecture preserves the option to swap hot kernels for Rustler NIFs later. |
| Database access | **Ecto** against Postgres, schema designed fresh from the canonical data model — *not* adapted from existing migrations |
| Drafts / publish workflow | Out for v1 (edit-in-place). Schema designed for future export/versioning. |
| World concept | **In.** A world is a set of levels; the graph emerges from `level_transition`-component entities placed inside levels |
| Scripting | **Luerl-first.** One execution model (Lua via Luerl). The "no-code" picker is a UI over a curated catalog of pre-shipped Lua functions. |
| Animations | Defined on the spritesheet asset. Designer-facing UI (frame-picker + preview) is reached *from* the entity editor. |
| Tile collision | **Per-sprite** (preset enum in foundation; polygon support added in Tile Features surface) |
| Map layers | **In.** Multi-layer maps with explicit per-layer `z_index`. Entity placements also carry `z_index`. Renderer composes a single z-sorted list. |
| Render frame rate | **60 FPS client** with snapshot interpolation between 10 Hz authoritative ticks. Retro feel comes from art direction, not low FPS. |
| TUI | **Elixir-native** via [`term_ui`](https://hex.pm/packages/term_ui) (with Ratatouille → Owl → roll-our-own as documented fallbacks); same release as Phoenix; distributed as single-file binary via [Burrito](https://github.com/burrito-elixir/burrito) |
| Polygon collision algorithm | **SAT** (Separating Axis Theorem) for convex 2D polygons — library-free, deterministic, ports cleanly to TS/Swift |
| CI | **Railway's CI** (no GitHub Actions); `mix proto.gen --check` and `mix format/credo/test` run as Railway build steps |
| Frontend toolchain | **All TypeScript** for `assets/js/**` including hooks. One tsconfig, one linter. |
| Migrations on deploy | Auto-migrate on boot |
| Observability v1 | Structured stdout/stderr logs only (Telemetry events emitted; no exporter) |

---

## 1. Architecture overview

### Tech stack

| Layer | Choice |
|---|---|
| Runtime | Elixir on BEAM, single Phoenix application (not an umbrella) |
| Web framework | Phoenix 1.7+ with LiveView for design surfaces, Phoenix.Channel for game realtime |
| Persistence | PostgreSQL via Ecto |
| Pub/sub | Phoenix.PubSub |
| Wire format | Protocol Buffers 3 over Phoenix.Channel for game; native LiveView events for design |
| Scripting | Luerl for entity behaviors |
| Asset storage | S3-compatible object store, CDN-fronted, content-addressed paths (`sha256/aa/bb/{full_hash}.png`) |
| Image processing | `Vix` + `Image` (libvips binding) |
| Auth | `Phoenix.Token` for player JWTs; DB-backed sessions for designer cookies. No external auth library. |
| Client (Pixi) | Pixi 8. Bundled by esbuild. |
| Client (chrome) | LiveView-rendered HTML. Tailwind for styling. |
| Tests | ExUnit; PhoenixTest or Wallaby for LiveView E2E; vitest where any TS unit-tests exist outside the BEAM test path |
| Deployment | Railway with `mix release` BEAM release, single Docker image |
| TUI | Elixir-native via term_ui; same release as Phoenix; Burrito-packaged single-file binary |

### Process topology

```
┌──────────────────────────────────────────────────────────────────┐
│                       BEAM node (one per deployment)             │
│                                                                  │
│  Phoenix.Endpoint                                                │
│  ├── LiveView                  (per-tab process)                 │
│  ├── Phoenix.Channel           (per-WS-connection process)       │
│  ├── GameInstance.Server       (1 GenServer per active level)    │
│  │   └── ECS world in process state, 10 Hz tick                  │
│  ├── Phoenix.PubSub                                              │
│  ├── Boxland.Repo              (Ecto pool to Postgres)           │
│  ├── Boxland.Scripting.Host    (Luerl host + script catalog)     │
│  └── Boxland.AssetCache                                          │
│                                                                  │
│  + standard supervision tree, Telemetry, Logger                  │
│  + (TUI process when launched via Burrito binary; supervises     │
│     the Phoenix sub-tree as a child rather than peer)            │
└──────────────────────────────────────────────────────────────────┘

S3-compatible object store (Cloudflare R2 default) — assets
PostgreSQL (Railway add-on)                       — durable state
Redis (Railway add-on)                            — session cache, game WAL
```

Single-tenant per deployment. No BEAM clustering required for v1; architecture doesn't preclude it later.

### Repo shape

Single Phoenix app, top-level dirs:

```
boxland/
├── config/
├── lib/
│   ├── boxland/                 # Domain layer (contexts)
│   │   ├── assets/              # Asset upload, processing, serving
│   │   ├── library/             # Sprites, spritesheets, animations
│   │   ├── maps/                # Map definitions
│   │   ├── entities/            # Entity types, components, scripts
│   │   ├── levels/              # Levels (map + entity placements)
│   │   ├── worlds/              # Worlds (level sets)
│   │   ├── auth/                # Designer + player auth
│   │   ├── game/                # Runtime: GameInstance, ECS, Channels
│   │   ├── scripting/           # Luerl host, sandbox, action library
│   │   └── tui/                 # term_ui-based launcher (when run as TUI mode)
│   ├── boxland_web/             # Web layer
│   │   ├── live/                # LiveView modules per surface
│   │   ├── channels/            # Phoenix.Channel modules
│   │   ├── components/          # Shared LiveComponent library (the design system)
│   │   ├── controllers/         # JSON APIs (asset upload, oauth callbacks)
│   │   └── proto/               # Generated Protobuf modules (committed)
│   └── boxland_logic/           # Built-in Lua scripts (the no-code action library)
│       └── actions/*.lua
├── priv/
│   ├── repo/migrations/         # Ecto migrations (per-change files)
│   ├── static/                  # Compiled JS/CSS from esbuild + Tailwind
│   └── proto/                   # *.proto files (referenced from /schemas/)
├── assets/                      # JS/CSS sources for esbuild (TypeScript)
│   ├── js/
│   │   ├── app.ts               # LiveView socket boot + hook registry
│   │   ├── hooks/               # LiveView JS hooks (inc. Pixi mounts)
│   │   ├── pixi/                # Pixi rendering modules per surface
│   │   └── proto/               # Generated TS from /schemas/*.proto (committed)
│   ├── css/
│   └── tailwind.config.js
├── schemas/                     # *.proto wire schemas (single source of truth)
├── test/
└── mix.exs
```

### Why this shape

- One BEAM node, one Phoenix app, one Repo — minimum-friction Phoenix idioms
- Domain contexts under `lib/boxland/` — Phoenix DDD layout. Each context owns its tables and exposes a public function module.
- Web layer in `lib/boxland_web/` — calls into contexts; contexts never reach into web
- Pixi as JS hooks under `assets/js/pixi/`, not standalone Vite entries — the architectural shift. Each editor is a *LiveView page* whose Pixi canvas is one DOM element with a hook attached.
- Built-in Lua scripts as code (`lib/boxland_logic/actions/*.lua`); designer-written scripts as data (DB rows). Both run through the same Luerl host.
- `schemas/*.proto` as wire SoT — generated Elixir + TS modules committed to git.

---

## 2. Data model

Designed fresh from the stated canonical model:

> An ASSET is a game file uploaded by the user, the most important type being a SPRITE (32×32) or SPRITESHEET (32×32×N gridded). SPRITES can be TILES, which are part of MAPS, the non-interactive basis for LEVELS, which are MAPS + ENTITIES. ENTITIES are interactive (PCs, NPCs, spawners, consumers, anything with logic).

### Conventions

- **IDs:** `bigserial` primary keys
- **Timestamps:** Ecto default `inserted_at` + `updated_at`
- **JSON:** `jsonb` for flexible/nested config; embedded Ecto schemas give compile-time safety
- **No soft-delete** in v1 (delete = hard delete)
- **No drafts / published versioning** in v1 (edit-in-place; schema designed for future export per Rule 3 below)
- **Single-tenant** — no `tenant_id` columns

### Auth schemas

```text
designers
├── id :bigserial PK
├── email :string unique
├── password_hash :string
├── display_name :string
└── inserted_at, updated_at

designer_sessions                    # cookie-backed, 30-day TTL
├── id :bigserial PK
├── designer_id → designers.id ON DELETE CASCADE
├── token_hash :binary unique        # sha256 of session token
├── ip :inet
├── expires_at :utc_datetime
└── inserted_at

players
├── id :bigserial PK
├── email :string unique nullable    # nullable for OAuth-only accounts
├── password_hash :string nullable
├── display_name :string
└── inserted_at, updated_at

player_oauth_links
├── player_id → players.id ON DELETE CASCADE
├── provider :string                 # "google" | "apple" | "discord"
├── provider_user_id :string
└── unique(provider, provider_user_id)

player_sessions                      # JWT refresh tokens, hashed
├── id :bigserial PK
├── player_id → players.id ON DELETE CASCADE
├── refresh_token_hash :binary unique
├── expires_at :utc_datetime
└── inserted_at
```

### Assets (single table, kind-discriminated)

```text
assets
├── id :bigserial PK
├── owner_id → designers.id
├── kind :string                     # "sprite" | "spritesheet" | "audio"
├── name :string
├── sha256 :binary unique            # content-addressed; same bytes = same row
├── content_url :string              # CDN URL (denormalized for speed)
├── byte_size :integer
├── mime_type :string
├── metadata :jsonb                  # kind-specific, validated by embedded Ecto schema
└── inserted_at, updated_at
```

Embedded `metadata` schemas per kind:

```text
Sprite.Metadata
├── collision : { kind: "preset", value: "none" | "solid" | "wall_n/e/s/w" | ... }
│             | { kind: "polygon", vertices: [[x,y],...] }       (added by Tile Features surface)
│             | { kind: "polygons", shapes: [[[x,y]...],...] }   (added by Tile Features surface)
└── edge_sockets : { n: string?, e: string?, s: string?, w: string? }  (added by Tile Features)

Spritesheet.Metadata
├── grid_cols :integer
├── grid_rows :integer
├── frame_collision_shapes :map      # frame_index → shape, sparse
└── animations : [
    {
      name :string,                  # "idle", "walk_north"
      frames :[integer],             # ordered frame indices
      fps :integer,
      loop :boolean
    }, ...
  ]

Audio.Metadata
├── duration_ms :integer
├── sample_rate :integer
└── ...
```

**Why one table, not three:** queries like "list all assets I own" or "find by sha256" don't care about kind. Lookups by kind use a partial index. Embedded schemas give Elixir-side type safety.

### Maps (multi-layer)

```text
maps
├── id :bigserial PK
├── owner_id → designers.id
├── slug :string                     # owner-scoped URL/export identifier
├── name :string
├── width :integer                   # in tiles
├── height :integer                  # in tiles
└── inserted_at, updated_at
# Map row holds geometry only. Layers are children.

map_layers
├── id :bigserial PK
├── map_id → maps.id ON DELETE CASCADE
├── name :string                     # "ground", "decoration", "tree_trunks", "tree_leaves"
├── z_index :integer                 # render order; higher = drawn later (on top)
├── tiles :jsonb                     # {"x,y": {sprite_id} | {sheet_id, frame}}
└── inserted_at, updated_at
# index: (map_id, z_index)
```

Mapmaker UI defaults new maps to: `ground` (z=0), `decoration` (z=20), `above` (z=40).

**Why JSONB instead of a row-per-cell `map_tiles` table:** maps are non-interactive. We always read/write whole layers, never query individual cells in production. Single-row reads are simpler and faster.

### Entities (definitions, not instances)

```text
entity_types
├── id :bigserial PK
├── owner_id → designers.id
├── slug :string                     # export identifier
├── name :string
├── visual_ref :jsonb                # {"asset_id": int}
├── animation_bindings :jsonb        # {"idle": "default_idle", "walk_north": "default_walk_n", ...}
├── components :jsonb                # [{kind, config}, ...]
├── scripts :jsonb                   # [{hook, source}, ...]
├── default_collision_mask :string   # "land" | "flying" | "aquatic" | "phasing" | ...
├── default_z_index :integer
└── inserted_at, updated_at
```

Components example:
```json
[
  {"kind": "movable",             "config": {"speed_px_per_sec": 64}},
  {"kind": "player_controllable", "config": {}},
  {"kind": "damageable",          "config": {"max_hp": 100}},
  {"kind": "level_transition",    "config": {"target_level_id": 5, "spawn_x": 12, "spawn_y": 8}}
]
```

Scripts example:
```json
[
  {"hook": "on_collide",  "source": {"type": "builtin", "action": "deal_damage", "config": {"amount": 10}}},
  {"hook": "on_interact", "source": {"type": "custom",  "lua": "function on_interact(self, player) ... end"}}
]
```

### Levels

```text
levels
├── id :bigserial PK
├── owner_id → designers.id
├── slug :string
├── name :string
├── map_id → maps.id
├── world_id → worlds.id NULLABLE   # null = standalone level
├── hud_config :jsonb                # HUD elements (bars, labels, action buttons)
├── instancing :string               # "shared" | "per_party" | "per_user"
└── inserted_at, updated_at

level_entities                       # entity placements within a level
├── id :bigserial PK
├── level_id → levels.id ON DELETE CASCADE
├── entity_type_id → entity_types.id
├── pos_x :integer
├── pos_y :integer
├── z_index_override :integer NULLABLE
├── instance_overrides :jsonb        # per-placement overrides
├── script_state :jsonb              # per-instance Lua self.state
└── inserted_at, updated_at
# index: (level_id), (level_id, z_index_override)
```

Separate table (not jsonb-on-level): entity placements are mutated individually during editing; first-class access via JOIN is preferred over rewriting jsonb on every change.

### Worlds

```text
worlds
├── id :bigserial PK
├── owner_id → designers.id
├── slug :string
├── name :string
└── inserted_at, updated_at

# levels.world_id (nullable FK on levels) is how a level joins a world.
# A level lives in at most one world.
# Transitions emerge from entity_types with a `level_transition` component
# placed in level_entities rows; the world editor walks these to render the graph.
```

### Game runtime state

```text
level_state                          # snapshot of mutable state per running instance
├── id :bigserial PK
├── level_id → levels.id
├── instance_key :string             # "shared" | "party:42" | "user:99" | "sandbox:designer:5"
├── state :binary                    # Protobuf-encoded MapState, gzip
├── flushed_at :utc_datetime
└── unique(level_id, instance_key)
```

Single row per running instance. Boot loads the snapshot + replays Redis WAL since `flushed_at`. WAL between flushes lives in Redis Streams (per PLAN.md §1).

### Z-index rendering rule

Every renderable thing (tile in a map_layer, entity placement) carries an integer `z_index`. Rendering for a level is one pass: collect all map-layer tiles + all entity placements into a single z-sorted list, render in order. Same model in editor preview, sandbox, and live game (one render path).

Example "character behind tree":
- Tree trunk on `map_layers` z=10
- Tree leaves on `map_layers` z=40
- Character `entity_type.default_z_index = 25`
- Character renders above trunk, below leaves; walks behind the tree from the player's perspective

### Export-readiness rules (no v1 build, but schema enables it)

**Rule 1 — Stable string identifiers in addition to bigserial IDs**

| Table | External identifier |
|---|---|
| `assets` | `sha256` (content-addressed) |
| `entity_types` | `slug` (owner-scoped unique) |
| `maps` | `slug` |
| `levels` | `slug` |
| `worlds` | `slug` |
| `map_layers` | `name` (within a map) |
| `level_entities` | exported by ordinal position within their level |

**Rule 2 — JSON refs use external identifiers in export form**

In the database: `entity_types.visual_ref = {"asset_id": 42}` (cheap join). In an export bundle: `{"asset_sha256": "ab12...ef"}` (the bundle includes the asset). Helper functions per context:

```elixir
Boxland.Library.export_entity_type(id) :: map()
Boxland.Library.import_entity_type(map())
```

Schema doesn't change — only serialization does.

**Rule 3 — Every artifact bundle is self-contained**

Exporting a level means: the level + its map + the map's layers + every referenced asset + every referenced entity_type + (transitively) any worlds the level belongs to. Bundle layout:

```
my-level.boxland/
├── manifest.json
├── assets/
│   └── ab/12/ab12...ef.png
├── entity_types/
│   └── goblin.json
├── maps/
│   └── starter-village.json
├── levels/
│   └── tutorial.json
└── worlds/
    └── starter.json
```

### Migration strategy

Per-change Ecto migrations in `priv/repo/migrations/`, dated, generated via `mix ecto.gen.migration`. First migration = everything above, in one go (no production state to preserve).

### Deferred from v1 (deliberately)

- Tile groups, palettes, palette baking, advanced edge socket-driven procedural placement
- Character generator data (separate surface spec)
- Replay / WAL history retention
- Audit logs
- Per-tile collision overrides (collision is per-sprite for v1; extensible later)

---

## 3. LiveView ↔ Pixi integration pattern

### The three modes

| Mode | Surfaces | Data source | LiveView role |
|---|---|---|---|
| **Editor** | Mapmaker, Level editor, Character generator, Asset pixel editor | LiveView push_event (single user, draft state in LiveView assigns) | Owns draft state, palette, inspector, persistence |
| **Game** | Player game, Sandbox preview | Phoenix.Channel binary stream (Protobuf snapshots, multi-user) | Page wrapper + HUD overlays only; canvas talks to Channel directly |
| **Static viewer** | Asset previews, sprite thumbnails | LiveView assigns at mount, never updates | Renders once; no interaction loop |

### Page anatomy

```heex
<div class="layout-3pane">
  <aside class="left">
    <%!-- LiveView-rendered chrome: palette, library, tree, etc. --%>
  </aside>

  <main class="canvas-host">
    <div
      id={"pixi-#{@id}"}
      phx-hook="PixiMapmaker"
      phx-update="ignore"
      data-map-id={@map.id}
      data-bootstrap={Jason.encode!(@bootstrap_payload)}
    />
  </main>

  <aside class="right">
    <%!-- LiveView-rendered inspector --%>
  </aside>
</div>
```

Three rules:

- `phx-update="ignore"` — LiveView does not touch this element's children after mount. Pixi owns the DOM under it.
- `data-bootstrap` — initial state passed via dataset; the hook reads it once on mount.
- Stable `id` — required for hooks. Tied to the entity being edited.

### Editor mode — bidirectional LiveView ↔ Pixi

Hook (TypeScript):

```typescript
// assets/js/hooks/pixi_mapmaker.ts
export const PixiMapmaker = {
  async mounted() {
    const { MapmakerScene } = await import("../pixi/mapmaker/scene");
    const bootstrap = JSON.parse(this.el.dataset.bootstrap);
    this.scene = new MapmakerScene(this.el, bootstrap);

    this.handleEvent("brush_set",       ({ sprite_id, sheet_frame }) => this.scene.setBrush(sprite_id, sheet_frame));
    this.handleEvent("layer_added",     ({ layer })                  => this.scene.addLayer(layer));
    this.handleEvent("layer_z_changed", ({ layer_id, z })            => this.scene.setLayerZ(layer_id, z));
    this.handleEvent("tile_remote",     ({ layer_id, x, y, source }) => this.scene.setTile(layer_id, x, y, source));

    this.scene.onTilePainted = (payload) => this.pushEvent("paint_tile",  payload);
    this.scene.onTileErased  = (payload) => this.pushEvent("erase_tile",  payload);
    this.scene.onCameraIdle  = (payload) => this.pushEvent("save_camera", payload);
  },
  destroyed() { this.scene?.destroy(); },
};
```

Server (Elixir):

```elixir
def handle_event("paint_tile", %{"layer_id" => l, "x" => x, "y" => y, "source" => src}, socket) do
  case Maps.set_tile(socket.assigns.map, l, x, y, src) do
    {:ok, layer} ->
      socket = assign(socket, layer_dirty: layer)
      socket = push_event(socket, "tile_remote", %{layer_id: l, x: x, y: y, source: src})
      {:noreply, socket}

    {:error, changeset} ->
      {:noreply, put_flash(socket, :error, format_error(changeset))}
  end
end
```

### State ownership rules

| Concern | Lives in |
|---|---|
| Persistent draft (DB rows) | Postgres |
| LiveView assigns (current brush, selected layer, undo pointer, dirty flag) | LiveView process |
| Render state (drawn tiles, camera offset, animation phase) | Pixi scene (in TS) |
| Input gestures (drag-paint, marquee) | Pixi scene |
| Visual feedback during interaction (hover-preview tile color) | Pixi scene |

**The rule:** anything the designer reads outside the canvas lives server-side. Anything that's purely visual feedback during interaction stays in the canvas. The canvas pushes a final state event when a gesture completes.

### Game mode — Pixi joins a Channel directly

```typescript
import { Socket } from "phoenix";

export const PixiGame = {
  async mounted() {
    const { GameScene } = await import("../pixi/game/scene");
    const { Snapshot, Input } = await import("../proto/game");
    const { token, levelId, instanceKey } = this.el.dataset;

    this.scene = new GameScene(this.el);
    this.socket = new Socket("/game-socket", { params: { token }, binaryType: "arraybuffer" });
    this.socket.connect();

    this.channel = this.socket.channel(`level:${levelId}:${instanceKey}`);
    this.channel.on("snapshot", (raw) => this.scene.applySnapshot(Snapshot.decode(new Uint8Array(raw))));
    this.channel.join().receive("ok", () => this.scene.start());

    this.scene.onInput = (input) => this.channel.push("input", Input.encode(input));
  },
  destroyed() {
    this.channel?.leave();
    this.socket?.disconnect();
    this.scene?.destroy();
  },
};
```

**Two sockets, by design:**

- `/live` — JSON envelopes, LiveView's own protocol, design surfaces only
- `/game-socket` — binary Protobuf payloads, game runtime only

Different auth (cookie vs JWT), different serializers (JSON vs binary), different lifecycles.

### Sandbox = same Pixi game scene + sandbox-scoped instance key

When a designer presses "Preview" in the level editor, the LiveView server mints a short-lived JWT in player realm and pushes a navigation event. The page they land on is the same `PlayerGameLive` LiveView with `instance_key="sandbox:#{designer_id}:#{level_id}"`. One client codebase, two access paths.

### HUD and text overlays

Text inside the canvas (NPC dialog, damage numbers, HUD bars with labels) renders as **absolutely-positioned HTML overlays** on top of the canvas, not Pixi text. Real fonts, real ligatures, accessibility, easy theming. The "first-class text" payoff.

Coordinate sync: the canvas hook publishes camera transform updates ~10 times per second; LiveView recomputes screen positions for HUD elements and re-renders the overlay subtree.

For high-frequency canvas effects (damage flash, particles, floating numbers): the EVENTS that trigger them fire at the 10 Hz tick; the ANIMATIONS that result run at 60 FPS in Pixi (RAF) or as CSS keyframes on overlays. **No render throttling — 60 FPS is the rendering norm with snapshot interpolation between authoritative ticks.** Retro feel comes from art direction, not low FPS.

### File uploads

LiveView's `<.live_file_input>` handles uploads natively — drag-and-drop, progress, server-side processing. The Asset Library uses it directly:

```elixir
def mount(_, _, socket) do
  {:ok,
    allow_upload(socket, :sprite_file,
      accept: ~w(.png .aseprite .json),
      max_entries: 50,
      max_file_size: 10_000_000,
      auto_upload: true,
      progress: &handle_progress/3)}
end
```

No multipart-form ceremony, no XHR plumbing.

### Care points

- Hook lifecycle hygiene — `destroyed()` must clean up the Pixi Application and disconnect channels
- `phx-update="ignore"` discipline on canvas hosts
- Push-event payload size — small deltas only; full state via `data-bootstrap` or Channel
- Cross-tab consistency requires Phoenix.PubSub broadcasts (handler structure is already prepared for this)
- Pixi-side state must be reconstructible from server data — the canvas should never be the sole authority for anything

---

## 4. Wire protocol & channel topology

### Protobuf as the source of truth

Every game message is a Protobuf 3 schema in `/schemas/`:

```
/schemas/
├── game_snapshot.proto   # Snapshot, EntityView, PlayerView, TileChunkUpdate
├── game_input.proto      # Input verbs (move, interact, ability)
├── game_event.proto      # Ephemeral world events (damage, dialog, sound)
├── game_control.proto    # Channel control (join_ack, kicked, level_changed)
└── common.proto          # Shared types (Vec2, ID, Time)
```

### Codegen pipeline

`mix proto.gen` task wraps `protoc`:

| Output | Plugin | Goes to | Committed to git? |
|---|---|---|---|
| Elixir modules | `protoc-gen-elixir` | `lib/boxland_web/proto/*.pb.ex` | Yes |
| TypeScript modules | `ts-proto` (or equivalent) | `assets/js/proto/*.ts` | Yes |
| Swift modules (later) | `protoc-gen-swift` | `ios/Sources/Proto/*.swift` | Yes |

Generated code committed so building doesn't require `protoc` locally. CI runs `mix proto.gen --check` and fails the build if generated files are stale.

### Versioning rules

| Change | Allowed? | How |
|---|---|---|
| Add new optional field | ✓ | Pick unused field number |
| Add new message type | ✓ | New message in existing or new file |
| Rename a field | ✓ | Keep field number, change name |
| Reuse a field number | ✗ | Permanent contract — mark removed fields as `reserved` |
| Breaking change | rare | Bump topic prefix (`game:` → `gamev2:`); run both during migration |

### Game socket auth

```elixir
defmodule BoxlandWeb.GameSocket do
  use Phoenix.Socket
  channel "level:*", BoxlandWeb.LevelChannel

  @impl true
  def connect(%{"token" => token}, socket, _info) do
    case Boxland.Auth.Tokens.verify_game_token(token) do
      {:ok, %{player_id: pid, realm: realm}} -> {:ok, assign(socket, player_id: pid, realm: realm)}
      :error -> :error
    end
  end

  @impl true
  def id(socket), do: "player:#{socket.assigns.player_id}"
end
```

### Channel topology

One Channel module handles all level instances; topic encodes the instance:

```
level:{level_id}:{instance_kind}:{instance_arg?}

level:42:shared              ← canonical shared instance
level:42:user:99             ← per-user instance for player 99
level:42:party:7             ← per-party instance for party 7
level:42:sandbox:3           ← designer 3's sandbox of level 42
```

### Instance lifecycle

- `level:42:shared` with no subscribers → no GenServer running
- First subscriber → `InstanceManager` starts a `GameInstance.Server` GenServer, loads `level_state` from Postgres + replays Redis WAL since `flushed_at`
- New subscribers register their channel pid with the GenServer; the GenServer's tick loop sends snapshots via `send(channel_pid, {:snapshot, payload})` → channel's `handle_info` calls `push/3`
- Last subscriber leaves → instance idles for N seconds (default 60s) → flushes WAL to Postgres → terminates
- Sandbox instances have a shorter idle timeout (10s) and their `level_state` row is keyed by sandbox instance_key

**Per-subscriber direct-send instead of PubSub broadcast** because AOI snapshots are *per-subscriber* (different visible entities depending on each player's position). PubSub is still useful for fan-out events (level-wide announcements, "hot-swap completed").

### Snapshot structure (v1)

```protobuf
syntax = "proto3";
package boxland.game;

message Snapshot {
  uint32 tick = 1;
  uint64 server_time_ms = 2;
  PlayerView self = 3;
  repeated EntityView entities = 4;
  repeated TileChunkUpdate chunk_updates = 5;
  repeated GameEvent events = 6;
  HudState hud = 7;
}

message EntityView {
  uint64 id = 1;
  uint32 entity_type_id = 2;
  sint32 x_subpixel = 3;
  sint32 y_subpixel = 4;
  int32 z_index = 5;
  uint32 animation_name_id = 6;
  uint32 animation_frame = 7;
  uint32 facing = 8;

  // Per-component runtime state — only fields whose component is present
  // and currently non-default are populated. proto3 omits unset optionals.
  optional DamageableState     damageable     = 100;
  optional NameableState       nameable       = 101;
  optional StatusEffectsState  status_effects = 102;
  optional VisualEffectsState  visual_effects = 103;
  optional ConversationState   conversation   = 104;
  optional CustomState         custom         = 200;  // designer escape hatch
}

message DamageableState {
  uint32 hp_current = 1;
  uint32 hp_max = 2;
  optional uint32 shield_current = 3;
  optional uint32 nameplate_color = 4;
}

message NameableState {
  string display_name = 1;
  optional string title = 2;
}

message StatusEffectsState { repeated StatusEffect effects = 1; }
message StatusEffect {
  uint32 effect_id = 1;
  uint32 magnitude = 2;
  uint32 ticks_remaining = 3;
  optional uint32 stack_count = 4;
}

message VisualEffectsState { repeated VisualEffect effects = 1; }
message VisualEffect {
  uint32 effect_id = 1;
  optional uint32 ticks_remaining = 2;
  optional bytes params = 3;
}

message ConversationState {
  uint64 talking_to_player_id = 1;
  string current_line_id = 2;
}

message CustomState {
  // Generic key/value bag for one-off designer extensions.
  // Prefer making a new component when a pattern recurs across entities.
  map<string, sint64> ints = 1;
  map<string, string> strings = 2;
  map<string, bool> bools = 3;
}

message PlayerView {
  EntityView entity = 1;
}

message TileChunkUpdate {
  uint32 map_id = 1;
  uint32 layer_id = 2;
  uint32 chunk_x = 3;                    // 16-tile chunks
  uint32 chunk_y = 4;
  bytes tiles = 5;
}

message HudState {
  map<string, double> numbers = 1;
  map<string, string> strings = 2;
}
```

Inputs (minimum v1):

```protobuf
message Input {
  uint64 client_time_ms = 1;
  oneof verb {
    Move      move      = 10;
    Interact  interact  = 11;
    Ability   ability   = 12;
  }

  message Move     { sint32 dx = 1; sint32 dy = 2; }
  message Interact { uint64 target_entity_id = 1; }
  message Ability  { uint32 slot = 1; optional uint64 target_entity_id = 2; }
}
```

Schemas grow in the Game Runtime surface spec — this is the minimum needed to validate the foundation.

### Component-keyed runtime state — extensibility model

- Common patterns (HP, status effects, visual effects, name, conversation) get first-class typed messages
- New component kinds = new optional field on EntityView (next available number) + new state message
- Designers compose entities from the catalog of components; new common patterns earn first-class status; one-off needs go in `CustomState`
- Old clients ignore new fields; new clients use them. No version bumps for additive changes.

### Binary on the wire

```elixir
# server
push(socket, "snapshot", Game.Snapshot.encode(snapshot))   # iodata, sent as binary WS frame

# client
this.channel.on("snapshot", (raw) => {
  const snapshot = Snapshot.decode(new Uint8Array(raw));
  this.scene.applySnapshot(snapshot);
});
```

Phoenix envelope is JSON; the payload is a binary frame. ~80 bytes envelope overhead per message — invisible at 10 Hz.

### Heartbeats and reconnection

Phoenix.Socket sends a heartbeat every 30s by default and automatically reconnects on network drop. Pixi hook's `Socket.onOpen`/`onClose` callbacks show "Reconnecting…" overlays during outages. On reconnect, channel join sends an initial snapshot — no extra protocol needed.

---

## 5. Scripting layer (Luerl)

### Why Luerl

[Luerl](https://luerl.org/) is a pure-Erlang implementation of Lua 5.3 by Robert Virding. Pure BEAM:

- Scripts run inside supervised processes; runaway scripts can be killed cleanly
- Sandboxing is real: whitelisting what embedded Lua can see (no `io.*`, no `os.execute`, no `loadstring`)
- Hot-reload is free — re-evaluating Lua produces a new compiled chunk
- Cross-platform deployment identical to deploying Elixir

Trade-off vs. native Lua: ~5–10× slower per instruction. At 10 Hz with hundreds of scripted entities, comfortable. Future swap to real Lua via NIF is localized to the runtime layer.

### One execution model — picker UI is a curated catalog

There is *one* execution path (Lua via Luerl). The "no-code" picker is a UI over a curated catalog of pre-shipped Lua functions. Power users write custom Lua; everyone else picks from the library.

### Where scripts live

`entity_types.scripts` carries a list of attachments:

```json
[
  {
    "id": "uuid-1",
    "hook": "on_tick",
    "source": { "type": "builtin", "action": "patrol_path", "config": {
      "waypoints": [[5,5], [10,5], [10,10], [5,10]],
      "speed_px_per_sec": 32
    }}
  },
  {
    "id": "uuid-2",
    "hook": "on_interact",
    "source": { "type": "custom", "lua": "return function(self, player)\n  api.dialog.open(player.id, 'greet_bandit_captain')\nend" }
  }
]
```

A single entity_type can have multiple scripts on the same hook; they execute in array order each time the hook fires.

### Hooks (event types)

| Hook | When it fires | Args |
|---|---|---|
| `on_spawn` | Entity instance created in a level | `(self)` |
| `on_destroyed` | Entity instance removed | `(self, reason)` |
| `on_tick` | Every server tick (10 Hz) | `(self)` |
| `on_collide` | This entity's AABB overlaps another | `(self, other)` |
| `on_interact` | Player presses interact targeting this entity | `(self, player)` |
| `on_damage` | Damage applied to this entity | `(self, source, amount)` |
| `on_heal` | Healing applied | `(self, source, amount)` |
| `on_player_enter_aoi` | A player's AOI now includes this entity | `(self, player)` |
| `on_player_leave_aoi` | A player's AOI no longer includes this entity | `(self, player)` |
| `on_status_applied` | A status effect added | `(self, effect)` |
| `on_status_expired` | A status effect ends | `(self, effect)` |
| `on_message` | Another script sends this entity a message via `api.send` | `(self, sender, message)` |

Hooks not present on an entity are zero-cost — dispatch only iterates attached scripts.

### Built-in scripts (the no-code catalog)

Lua files at `lib/boxland_logic/actions/*.lua`. Each returns a behavior descriptor:

```lua
-- lib/boxland_logic/actions/patrol_path.lua
return {
  name        = "Patrol Path",
  description = "Walk a fixed loop of waypoints",
  hooks       = { "on_tick" },
  params      = {
    waypoints = {
      type    = "list",
      element = { type = "tile_coord" },
      label   = "Waypoints",
      min_length = 2,
    },
    speed_px_per_sec = { type = "number", default = 32, min = 0, max = 256, label = "Speed" },
    pause_ms_at_each = { type = "number", default = 0, label = "Pause at each (ms)" },
  },
  run = function(self, p)
    local target_idx = (self.state.patrol_idx or 1)
    local target = p.waypoints[target_idx]
    if api.move.toward(self.id, target[1] * 32, target[2] * 32, p.speed_px_per_sec) then
      self.state.patrol_idx = (target_idx % #p.waypoints) + 1
    end
  end,
}
```

The `params` block is a typed schema. The behavior editor reads it and renders a form (number inputs, dropdowns, asset pickers, tile-coord pickers, color swatches). Each parameter's `type` ties to a renderer in the LiveView component library.

Adding a new built-in = drop a `.lua` file + restart (or hot-reload in dev). Catalog appears in the picker.

**Initial built-in catalog (foundation):** `move_toward_player`, `flee_from_player`, `patrol_path`, `random_wander`, `idle`, `attack_on_collide`, `take_damage_until_destroyed`, `level_transition`, `open_dialog_on_interact`, `give_item_on_interact`, `play_sound_on_event`, `spawn_periodically`, `tile_overlap_trigger`. Roughly 13 actions cover the bulk of common tile-RPG patterns.

### Custom scripts

For anything not in the catalog, designers write Lua directly. Same hooks, same host API, no parameter schema needed (the script *is* the behavior).

```lua
return function(self, player)
  if self.state.gave_quest then
    api.dialog.open(player.id, "quest_in_progress")
  else
    api.dialog.open(player.id, "quest_offer")
    self.state.gave_quest = true
  end
end
```

Stored verbatim in `entity_types.scripts[i].source.lua`. Behavior editor shows them in a Monaco-style code editor with Lua syntax highlighting and host-API autocomplete.

### Host API

Scripts call functions through the `api.*` namespace (Elixir functions exposed via `:luerl.set_table`):

```text
api.entity.*       — query/manipulate entities
api.move.*         — movement requests (server-authoritative)
api.combat.*       — damage / healing / status
api.player.*       — player-targeted operations
api.dialog.*       — conversation system
api.world.*        — global queries
api.fx.*           — visual / audio effects
api.send(target_id, message)     — async message to another entity's on_message
```

All host functions are **synchronous** within a tick — they apply effects to the world state immediately. Effects of one entity's scripts are visible to entities later in the tick's run order.

### Execution model

**Compilation cache:** per-`entity_type_id × script_id` Luerl chunk cache. Custom Lua: cache key includes source hash so edits invalidate. Built-in scripts: compiled once at app boot.

**No per-instance Luerl state.** Lua VMs are stateless between calls. Memory linear in entity_type count, not entity instance count.

**Per-call protocol:**

```
1. Tick scheduler decides entity E's hook H is firing
2. For each script attachment (in array order):
   a. Fetch compiled chunk from cache
   b. Build self table:
      - self.id, self.x, self.y, self.facing
      - self.state                                    (mutable; loaded from entity's runtime state)
      - self.components                               (read-only snapshot)
   c. Call run function with self + decoded args
   d. Read self.state back; write changes to entity runtime state
   e. Apply any host-API effects (already applied immediately by host fns)
```

`self.state` lives between ticks in `level_entities.script_state :jsonb`.

**Mutation model:** within a single tick, scripts run in entity ID order. Effects apply immediately; later scripts see them. No double-buffering in v1.

### Limits and sandboxing

| Limit | Default | Behavior on exceed |
|---|---|---|
| Instruction count | 10,000 per call | Script killed, error logged, entity continues |
| Wall-clock time | 5 ms per call | Same |
| Memory growth | 64 KB per call | Same |
| `api.entity.spawn` calls | 4 per call | Subsequent calls return nil |
| `api.entity.destroy` calls | 8 per call | Subsequent calls no-op |

Forbidden Lua surface (not exposed):
- `io.*`, `os.*` (except `os.time`, `os.clock`)
- `loadstring`, `load`, `dofile`, `require`
- `debug.*` (except `debug.traceback`)
- Any FFI-style escape

Scripts cannot reach the file system, network, OS, or other BEAM processes — only the host API.

### Hot-reload

When a designer edits a script:
- Cache entry for that compiled chunk is invalidated
- Next tick, next entity using the script triggers a recompile
- Live entity instances continue without restart; their `self.state` carries forward
- Per-PLAN.md hot-swap pattern, applied to script behavior

### Behavior editor surface (preview)

Detailed in its own surface spec:
- Entity editor's "Behaviors" tab shows the entity's script list in array order
- "Add behavior" → modal with **Picker** (catalog) and **Custom** (code editor) tabs
- Picker entry: name, description, params form (driven by the action's `params` schema)
- Custom tab: Monaco-style editor with Lua mode + autocomplete on `api.*`
- Test mode: select an entity in a level, fire a hook manually, watch state changes

### Deferred

- Inter-entity messaging guarantees (best-effort same-tick in v1)
- Async / coroutine support
- Multiplayer determinism for client-predicted scripts
- Asset loading from inside scripts
- Detailed script-error UI

---

## 6. Auth (designer + player realms)

### Two realms, never cross-authenticated

| Realm | Who | Transport | Credential |
|---|---|---|---|
| **Designer** | IDE users | LiveView socket + HTTP | Cookie-backed session, DB-stored |
| **Player** | Game runtime users | Game socket + HTTP API | JWT access + refresh tokens |
| **Designer-as-Sandbox** | Designer entering their own preview | Game socket only | Short-lived JWT minted by LiveView, bridges the two realms |

A designer cannot accidentally become a player by token confusion. The game socket's `connect/3` accepts only player or designer-sandbox tokens; LiveView never accepts a JWT.

### Module layout

```
lib/boxland/auth/
├── designer.ex             # register, authenticate, sessions
├── player.ex               # register, authenticate (password + OAuth), JWT mint
├── tokens.ex               # Phoenix.Token wrappers, hashing, signing
├── sandbox_bridge.ex       # mints designer-as-sandbox JWTs
├── access_policy.ex        # what a socket/realm can join
├── password.ex             # Argon2 wrapper
└── email.ex                # confirmation/reset token mgmt + Swoosh delivery

lib/boxland_web/auth/
├── designer_session.ex     # Plug + on_mount for LiveView
├── player_session.ex       # Plug for HTTP API: verify Bearer JWT
└── oauth_controller.ex     # OAuth provider callbacks
```

### Designer realm — sessions over a cookie

Login flow:
1. `POST /auth/designer/login {email, password}`
2. `Boxland.Auth.Designer.authenticate(email, password)` (Argon2 verify)
3. `Boxland.Auth.Designer.create_session(designer, ip)` — generates random token, stores sha256 in `designer_sessions.token_hash`
4. `put_session_cookie(conn, plain_token)` — `_boxland_designer_session`, signed, `secure` in prod, `http_only`, `same_site: "Lax"`, `max_age: 30 days`
5. Redirect to `/design`

LiveView integration:
```elixir
defmodule BoxlandWeb.MapmakerLive do
  use BoxlandWeb, :live_view
  on_mount {BoxlandWeb.DesignerSession, :require_designer}
end
```

### Player realm — JWT access + refresh

Login (password):
1. `POST /auth/player/login {email, password}`
2. Authenticate, then mint:
   - **Access token (JWT)** — Phoenix.Token, payload `%{player_id, realm: :player, iat}`, max_age 900s
   - **Refresh token** — random plaintext + sha256 stored in `player_sessions`
3. Respond with `{access_token, refresh_token, expires_in: 900, player}`

Refresh flow: `POST /auth/player/refresh {refresh_token}` → look up by sha256 → check expires_at → mint new pair (rotate refresh on each use).

OAuth (Google, Apple, Discord): standard OAuth 2.0 authorization code flow.
- `GET /auth/oauth/:provider/authorize` → redirect to provider
- `GET /auth/oauth/:provider/callback` → exchange code, fetch profile, link or create player, mint tokens

Email-collision rule: in v1 we auto-link OAuth identities to existing email-matched players. Mitigation: require providers that verify email ownership. Document; revisit with explicit confirmation flow in a security pass.

HTTP API auth: `Authorization: Bearer ...` Plug verifies access token, assigns `current_player_id` to conn.

Game socket auth: shown in §4. `verify_game_token/1` accepts both `:player` and `:designer_sandbox` realms.

### Sandbox bridge

Designer in LiveView clicks "Preview Level". LiveView mints a one-shot JWT scoped to that sandbox:

```elixir
def mint_sandbox_token(designer_id, level_id) do
  Phoenix.Token.sign(BoxlandWeb.Endpoint, "game_token", %{
    player_id: designer_id,
    realm: :designer_sandbox,
    level_id: level_id,
    iat: System.system_time(:second)
  }, max_age: 1800)
end
```

LiveView passes the token in the navigation; the sandbox player game LiveView mounts, opens game socket with the token. AccessPolicy verifies the designer_sandbox realm only joins matching sandbox topics. A designer cannot join the canonical shared instance using a sandbox token.

### Password hashing

Argon2 via `argon2_elixir`. Configurable params per env (lower in test for speed).

### Email confirmation and password reset

- **Email confirmation:** soft-required for v1. Account usable immediately; certain actions (password reset, OAuth linking) require confirmed email. Tokens stored hashed in `*_email_tokens` tables, time-limited (24h).
- **Password reset:** standard flow. Reset tokens single-use, time-limited (1h).
- **Email delivery:** Swoosh with adapter per env (dev: local, prod: Postmark/SES/Mailgun via env vars).

### Rate limiting

Hammer counters:
| Endpoint | Limit |
|---|---|
| `POST /auth/{designer,player}/login` | 10 per IP per hour |
| `POST /auth/player/refresh` | 60 per refresh token per hour |
| `POST /auth/{designer,player}/password-reset` | 3 per email per hour |
| `GET /auth/oauth/:provider/callback` | 30 per IP per hour |
| WS connect to game socket | 60 per IP per minute |

### Security checklist

- Cookies: `secure` in prod, `http_only`, `same_site: "Lax"`
- CSRF: Phoenix's built-in token + `protect_from_forgery` for HTML POSTs (LiveView handles automatically)
- HTTPS-only in prod (HSTS header)
- No long-lived tokens in URL paths
- Refresh tokens hashed, never logged. Access tokens not stored server-side.
- Argon2 (not bcrypt, not pbkdf2)
- Per-realm isolation enforced at socket connect AND at every channel join (defense in depth)

### Deferred

Two-factor auth, SSO/SAML, account deletion / GDPR export, audit log of auth events, multi-step OAuth confirmation, magic link / passwordless, granular permissions within a realm.

---

## 7. Asset pipeline

### End-to-end flow

```
1. Designer drops files in LiveView upload widget
                          │
                          ▼
2. LiveView consume_uploaded_entries/3 hands each path to
   Boxland.Library.ingest_upload(path, filename, designer_id)
                          │
                          ▼
3. Boxland.Library.Pipeline runs:
   ├── compute sha256 of file bytes
   ├── lookup existing asset by sha256 → return if hit (dedup)
   ├── classify (sprite | spritesheet | audio | sidecar) by ext + magic bytes
   ├── validate (PNG dims divisible by 32; audio format known)
   ├── parse sidecar if matched (.json beside .png)
   ├── generate derivatives (thumbnail PNG)
   ├── upload original + derivatives to object storage
   ├── insert assets row with metadata + content_url
   └── return asset
                          │
                          ▼
4. LiveView push_event "asset_added" → Pixi palette refreshes
```

The pipeline is a function, not a GenServer. Synchronous within the LiveView's process. Up to 50 files at once via `live_file_input`.

### Module layout

```
lib/boxland/library/
├── pipeline.ex
├── content_address.ex
├── classifier.ex
├── validator.ex
├── sidecar/
│   ├── aseprite.ex
│   ├── texture_packer.ex
│   └── generic.ex
├── processor/
│   ├── thumbnail.ex
│   └── (palette_bake.ex)             # stub for future
├── storage.ex                         # ExAws.S3 wrapper
└── catalog.ex                         # Ecto context
```

### Image processing — Vix/Image (libvips)

Choice: `Vix` + `Image`. Faster than ImageMagick, streaming (bounded RAM), native binding.

Operations the pipeline does in v1:
- Read PNG dimensions and pixel format
- Validate (dims divisible by 32, RGBA)
- Generate 64×64 thumbnail (nearest-neighbor to preserve pixel art)

Everything else (palette baking, format conversion) deferred to its own surface spec.

### Content addressing

```
sha256 hex digest:        a3f2b9c1...e88d  (64 chars)
storage key:              sprites/a3/f2/a3f2b9c1...e88d.png
```

Two-level prefix avoids hot prefixes in S3-style listings. `assets.content_url` denormalizes the full URL: `https://cdn.boxland.app/sprites/a3/f2/a3f2...e88d.png`.

### Object storage — ExAws.S3 against S3-compatible providers

Same code runs against AWS S3, Cloudflare R2, Backblaze B2, MinIO. Default for prod = R2 (cheap egress); MinIO for local dev (in docker-compose).

`Boxland.Library.Storage.put(key, bytes, opts)` uploads with `Cache-Control: public, max-age=31536000, immutable`. Content addressing means immutable URLs.

### CDN

Cloudflare R2 with public domain attached is the default. Pipeline doesn't know about the CDN — stores to S3, serves via configured `CDN_BASE_URL`. Cache invalidation is a non-issue (immutable URLs).

### Sidecar parsing (foundation level)

| Sidecar | Detection | Result |
|---|---|---|
| **Aseprite** | JSON `meta.app` matches Aseprite | Parse `frames` and `meta.frameTags` → `metadata.animations` and `metadata.grid` |
| **TexturePacker** | JSON `meta.app` matches TexturePacker | Parse `frames` array → `metadata.frames` |
| **Generic strip** | No JSON sidecar | Designer sets grid/animations in editor |

V1 parsers extract: frame layout (cols × rows or named regions), named animations, frame durations.

### Audio assets

- Accept `.wav`, `.mp3`, `.ogg`
- Store as-is at `audio/ab/cd/{hash}.{ext}`
- No transcoding in v1 — designer responsible for browser-compatible formats

### Deletion

`Boxland.Library.Catalog.delete_asset(asset)`:
1. Verify no entity_types, maps, or level_entities reference this asset
2. Refuse if in use, with usage report
3. Delete S3 objects (original + derivatives)
4. Delete the row

No orphan GC in foundation.

### Concurrency & dedup correctness

Two designers uploading the same PNG:
- Both compute same sha256, both attempt INSERT
- Postgres `ON CONFLICT (sha256) DO NOTHING ... RETURNING` resolves atomically
- Loser retries the lookup, finds the now-existing row

### Failure handling

| Failure | Behavior |
|---|---|
| S3 upload fails | Don't insert row; bubble error; user retries |
| sha256 collides | Return existing row (this is dedup) |
| Sidecar parse fails | Log; treat as if no sidecar |
| Validation fails | Don't store; return `{:error, reason}` to LiveView |
| LiveView upload aborted | Phoenix cleans up temp file |

### Deferred

- Palette baking (own surface spec when needed)
- Per-frame collision shape extraction from sidecars (Tile Features handles)
- Asset versioning
- Cross-asset dedup
- WebP/AVIF transcoding
- Bulk import (zip of many assets)
- Asset GC

---

## 8. Build & deploy

### Frontend build — esbuild + Tailwind, no Vite

```elixir
config :esbuild,
  version: "0.20.0",
  default: [
    args: ~w(js/app.ts
             --bundle
             --target=es2022
             --splitting
             --format=esm
             --outdir=../priv/static/assets
             --external:/fonts/*
             --external:/images/*),
    cd: Path.expand("../assets", __DIR__),
    env: %{"NODE_PATH" => Path.expand("../deps", __DIR__)}
  ]

config :tailwind,
  version: "3.4.0",
  default: [
    args: ~w(--config=tailwind.config.js
             --input=css/app.css
             --output=../priv/static/assets/app.css),
    cd: Path.expand("../assets", __DIR__)
  ]
```

One esbuild entry, code-split via dynamic imports. `app.ts` loads LiveView socket + registers all hooks. Each Pixi-bearing hook dynamically imports its scene module on first mount. esbuild's `--splitting --format=esm` handles this.

**All TypeScript** for `assets/js/**` including hooks. One tsconfig, one linter, one idiom across the board.

### Asset digesting

```elixir
# mix.exs aliases
"assets.deploy": [
  "tailwind default --minify",
  "esbuild default --minify",
  "phx.digest"
],
```

`mix phx.digest` content-hashes static assets and writes `cache_manifest.json`. `Plug.Static` serves digested filenames with `Cache-Control: max-age=31536000, immutable`.

### Phoenix release

```elixir
# mix.exs
releases: [
  boxland: [
    include_executables_for: [:unix],
    applications: [runtime_tools: :permanent],
    steps: [:assemble, :tar]
  ]
]
```

`mix release` produces `_build/prod/rel/boxland/`. Self-contained — bundles ERTS.

`config/runtime.exs` reads env at boot:
- `DATABASE_URL`, `REDIS_URL`
- `SECRET_KEY_BASE`, `PHX_HOST`, `PORT`
- `S3_*`, `CDN_BASE_URL`
- `OAUTH_*` per provider
- `MAILER_*`

### Docker image

Multi-stage build (Elixir builder → Debian runtime). `libvips-dev` in builder for compiling Vix NIF; `libvips` in runtime for execution.

### Railway deployment

`railway.toml`:
```toml
[build]
builder = "DOCKERFILE"

[deploy]
startCommand = "bin/boxland start"
healthcheckPath = "/healthz"
healthcheckTimeout = 30
restartPolicyType = "ON_FAILURE"
restartPolicyMaxRetries = 3
```

Services per environment: `boxland-app`, Postgres add-on, Redis add-on, Cloudflare R2 (external).

### Migrations on deploy

Auto-migrate on boot. `Boxland.Release.migrate/0` called from `Boxland.Application.start/2`. `RUN_MIGRATIONS_ON_BOOT=false` opts out for the rare destructive migration that needs manual control.

### Health checks

- `GET /healthz` — liveness, returns 200 if BEAM is up
- `GET /readyz` — readiness, returns 200 if DB + Redis reachable

Railway uses `/healthz` for liveness probe.

### Local development

`docker-compose.yml` provides Postgres + Redis + MinIO. `.env.dev` (committed example, real values uncommitted). `mix phx.server` runs Phoenix locally with hot-reload.

`justfile` orchestrates: `dev-up`, `serve`, `db-reset`, `proto-gen`, `test`, `ci`, `deploy`.

### CI

Railway's built-in CI pipeline. Build steps:
- `mix format --check-formatted`
- `mix credo --strict`
- `mix proto.gen --check`
- `mix test`

On push to `main`, Railway pulls the branch and rebuilds the Docker image.

### Observability

V1 minimum:
- Logger with `Logger.Backends.Console` formatted as JSON in prod
- Telemetry events emitted (no exporter wired in v1)
- Sentry optional via `SENTRY_DSN` env var

No Prometheus, Grafana, APM in v1.

### Deferred

- Multi-region deploys
- BEAM clustering
- Blue/green deploys (Railway's rolling restart suffices)
- Postgres backup/restore strategy (Railway provides snapshots)
- Performance benchmarking infra
- Feature flags
- Runtime config reload without restart

---

## 9. Surface spec decomposition

This foundation establishes the architecture every surface spec depends on. It does NOT design any individual feature in detail. Each surface gets its own brainstorm → design → plan → implement cycle.

### Decomposition principles

- **Independently shippable.** Each surface adds value without requiring the next.
- **Scoped tight.** One designer can hold the whole spec in mind. If not, sub-decompose.
- **One pattern, applied repeatedly.** Surfaces inherit foundation contracts (auth, wire, integration pattern, etc.); they only add what's surface-specific.

### The ordered list

#### 1. Foundation hardening *(this spec → first implementation)*

- Phoenix app skeleton, repo, supervision tree
- Ecto schemas + initial migration for everything in §2
- Auth scaffolding for both realms (modules + tables, no UI yet)
- LiveView root layout + base CSS + design system component library starter
- esbuild + Tailwind + asset.deploy pipeline
- Docker image building + Railway deploy of the empty shell
- Health checks, structured logging
- Protobuf codegen pipeline (`mix proto.gen`) + initial `.proto` files
- Luerl host wired up with empty action catalog

**Outcome:** an empty deployed app with `/healthz` returning ok.

#### 2. TUI — Boxland launcher and operator console *(parallel/continuous)*

- **Stack:** Elixir, [`term_ui`](https://hex.pm/packages/term_ui) (with Ratatouille → Owl → roll-our-own as documented fallbacks), packaged via [Burrito](https://github.com/burrito-elixir/burrito) as single-file binary
- **Architecture:** TUI lives in the same Elixir release as Phoenix. `lib/boxland/tui/` is a supervised module under `Boxland.Application`. Phoenix supervisor is a child of the application supervisor; TUI menu actions invoke supervisor commands on the Phoenix sub-tree. Logs flow into a `Boxland.TUI.LogBackend` (a Logger backend) that the LogViewer widget reads. **One BEAM, one Erlang, one binary.**
- **Capabilities (foundation set):** Design (start dev/personal mode), Serve (production-like), Reboot, Migrate, Backup, Restore, Test, Update (self-update), Settings, Logs
- **Distribution:** one Burrito binary per OS/arch (`boxland-darwin-arm64`, `boxland-windows-amd64.exe`, etc.); Homebrew, Scoop, `.deb`/`.rpm`, raw download
- **First-run wizard:** detect/install Postgres + Redis + libvips via OS package manager; initialize data directory; create first designer account; launch Design mode
- **Embedded ERTS:** Burrito bundles Erlang — users don't need it installed system-wide
- **Embedded Postgres:** open question, deferred to TUI surface spec. Foundation v1 requires user-installed Postgres; TUI helps install via OS package manager.
- **Risks:** term_ui is at v1.0-rc (Feb 2026) — we'd be early adopters of 1.0. Documented fallback chain mitigates. Burrito cross-compile setup is one-time investment (Zig toolchain). Subprocess management is N/A (TUI and Phoenix are same BEAM).

#### 3. Designer auth + login UI

- **Prerequisites:** Foundation
- **Scope:** Public landing → designer signup / login → designer dashboard stub. Email confirmation + password reset. Logout. Account settings (display name, change password).
- **Why first surface:** smallest end-to-end LiveView surface that exercises auth + DB + email + LiveView form patterns.
- **OAuth deferred to a 2.5 follow-up** to keep this spec tight.

#### 4. Asset Library

- **Prerequisites:** Designer auth
- **Scope:** Upload (drag-and-drop multi-file), library list with thumbnails, asset detail view, sidecar parsing (Aseprite + TexturePacker), spritesheet animation editor (frame picker + preview), delete, search/filter. Pixel editor stub.
- **Why second:** no realtime; exercises every layer of LiveView+Pixi at lower stakes. Heavy LiveView form work. Validates asset pipeline end-to-end.
- **Risks:** spritesheet animation editor is the first "real" Pixi+LiveView integration — sets the pattern for every later editor.

#### 5. Mapmaker

- **Prerequisites:** Asset Library
- **Scope:** Map CRUD list, Mapmaker editor (Pixi canvas + LiveView chrome), tile painting with undo/redo, layer add/remove/reorder by z-index, sprite palette panel, save flow, import/export of map artifacts (first surface to use the export rules from §2).
- **Why third:** first big LiveView ↔ Pixi bidirectional integration. Validates the integration pattern at scale. Undo/redo command bus.
- **Risks:** undo/redo state machine — own design pass.

#### 6. Tile Features — collision polygons + edge sockets

- **Prerequisites:** Asset Library, Mapmaker
- **Scope:**
  - **Collision polygons:** sprite metadata extends from preset enum to `{ kind: "preset"|"polygon"|"polygons", ... }`. Editor UX: 32×32 polygon editor in sprite detail view; click to place vertices, drag to move, right-click to remove. Multi-shape mode for irregular tiles.
  - **Edge sockets:** per-sprite, per-edge identifier (n/e/s/w → string). Designer-defined names. Editor UX: four input fields with autocomplete from existing sockets; edge-swatch visualization on sprite preview. Mapmaker integration: optional "snap to compatible edges" mode.
  - **Runtime:** collision system implements preset (fast path, AABB-derived edges), single convex polygon (SAT), multi-shape (per-shape SAT, OR'd). Canonical pseudocode in `/schemas/collision.md` extends to cover polygon SAT for cross-runtime parity.
- **Tile groups (procedural placement)** deferred to its own future surface spec.
- **Risks:** SAT collision is meaningfully slower than AABB. Performance budget addressed in Game Runtime spec; broad-phase AABB → narrow-phase SAT is the standard pattern.

#### 7. Entity Editor + Behavior Editor

- **Prerequisites:** Asset Library, Mapmaker
- **Scope:** Entity type CRUD list, entity editor with tabs for Visuals (sprite/sheet picker, animation bindings), Components (component composer, typed forms per kind), Behaviors (Lua script attachment editor — picker + custom code editor), z-index settings. Built-in script catalog renders as picker entries with parameter forms.
- **Why fourth:** configurable-form renderer (driven by component schemas + script param schemas) is heavy LiveView work. Lua editor is its own focused chunk.
- **Risks:** typed-form-from-schema renderer needs a fixed catalog of widget types: number, enum dropdown, asset picker, tile-coord picker, color, list-of, dialog-tree-picker (later). Possibly carved out as own component-library deliverable.

#### 8. Level Editor (+ World graph)

- **Prerequisites:** Mapmaker, Entity Editor
- **Scope:** Level CRUD, Level editor (Pixi canvas showing map + entity placements, drag-and-drop entity instantiation, instance overrides panel, HUD layout editor as separate tab), World CRUD (list of levels + graph view showing transitions emerging from `level_transition` components).
- **Why fifth:** composes what's now built. No fundamentally new patterns.
- **Risks:** HUD layout editor possibly carved out as sub-spec.

#### 9. Character Generator

- **Prerequisites:** Asset Library, Entity Editor (it's an editor mode for PC/NPC entity types)
- **Scope:** Slot-based character composition (head, body, arms, legs), per-slot part selection, palette tinting, animation preview, bake to a publishable PC/NPC entity type.
- **Why this position:** most complex single-user editor. Doesn't block runtime. Could move earlier or later.
- **Risks:** schema for character slots/parts wasn't designed in foundation. Sub-spec adds: `character_recipes`, `character_parts`, `character_bakes` tables. Keep scoped.

#### 10. Player auth (UI)

- **Prerequisites:** Foundation (auth modules already exist)
- **Scope:** Player landing, signup, login (password + OAuth), email confirmation, password reset.
- **Why now:** unblocks #11 (game runtime needs authenticated players).

#### 11. Game Runtime + Channels

- **Prerequisites:** Player auth (server-side), Level Editor, Tile Features
- **Scope:** GameInstance.Server GenServer per active instance; instance manager; ECS implementation (sparse-set-style storage in Elixir; revisit Rust NIF if profiler demands); 10 Hz tick; movement systems; collision system (ports the canonical algorithm from `/schemas/collision.md` to Elixir); AOI grid + per-subscriber snapshot; Luerl integration in tick loop; level_state persistence + Redis WAL; hot-swap pipeline for designer-pushed changes.
- **Why this position:** biggest, riskiest surface. Carve out sub-specs aggressively (collision-as-its-own-spec; AOI-as-its-own-spec; persistence-as-its-own-spec).
- **Risks:** all the perf risks of foundation §1's "pure Elixir" decision live here. Profile early. Rust NIF retreat path ready (data model and module boundaries support selective NIF replacement).

#### 12. Player Game Client + Sandbox

- **Prerequisites:** Game Runtime, Level Editor
- **Scope:** Player-facing routes (level browser, character selection, in-game LiveView+Pixi page); the Pixi game scene that consumes Channel snapshots; HUD overlay components; supports `dialog.open` from scripts; the Sandbox bridge wired up.
- **Why last (before polish):** consumes everything. Validates the full vertical stack.

#### 13. Settings + Design System polish

- **Prerequisites:** every other surface
- **Scope:** Designer settings (font, theme, hotkeys), player settings (controls, audio, accessibility), the formal LiveView component library (equivalent of the current Pixi UI Gallery, but for HTML components — buttons, cards, forms, modals, tooltips), accessibility audit pass, dark/light theming.

### Cross-cutting concerns (each surface must address)

| Concern | What the spec must include |
|---|---|
| **Routes** | Route table, authorization required, layout used |
| **State ownership** | Per §3's table — what's in LiveView, Pixi, server, DB |
| **Persistence** | Which Ecto context functions added/extended; migration files added |
| **Wire** | Any new Protobuf messages |
| **Scripts** | Any new built-in Lua actions or host API additions |
| **Tests** | ExUnit modules, LiveView tests, integration tests |
| **Telemetry** | Logger lines worth structuring; future metric hooks |
| **Export shape** | If artifacts are involved, the export-import functions per §2 Rule 3 |

### What this foundation spec does NOT do

- Migration plan from the existing Go codebase (greenfield)
- A schedule (no timelines, no estimates)
- A staffing plan
- UI mockups
- A complete design system component palette (lives in #13)
- A complete Protobuf catalog (each surface adds its own)
- An iOS spec (post-v1; foundation keeps the door open per §4 versioning)

---

## Appendix — Glossary

| Term | Meaning in Boxland |
|---|---|
| **Asset** | A user-uploaded game file. Most importantly sprites and spritesheets, also audio. |
| **Sprite** | A 32×32 PNG image. An asset of kind "sprite". |
| **Spritesheet** | A 32×32×N gridded PNG. An asset of kind "spritesheet". Carries animation definitions. |
| **Tile** | A sprite (or one frame of a spritesheet) placed at a grid coordinate in a map layer. |
| **Map** | Non-interactive geometry. Multiple z-indexed layers of tile placements. |
| **Entity Type** | A definition of an interactive game object — visual + components + scripts + default z-index. |
| **Entity Placement** | An instance of an entity type placed into a level at a specific position with optional overrides. |
| **Level** | A map + a set of entity placements + HUD config + instancing policy. |
| **World** | A set of levels. The graph between levels emerges from `level_transition`-component entities. |
| **Component** | A typed bundle of config attached to an entity, recognized by the simulation systems and scripts. |
| **Script** | A Lua function (built-in catalog or designer-written) attached to an event hook on an entity type. |
| **Hook** | A named event in entity lifecycle (`on_tick`, `on_collide`, `on_interact`, ...) that scripts can attach to. |
| **Realm** | An auth boundary. `:designer`, `:player`, `:designer_sandbox`. Sockets/connections are tagged with their realm. |
| **AOI** | Area of Interest. The radius of game state visible to a subscribed player. Drives per-subscriber snapshots. |
| **Sandbox** | A designer's preview of their own draft level. Same engine as live game; sandbox-scoped instance key. |
| **Hot-swap** | Designer changes to entity types / scripts applied to live game state between ticks, atomically. |
| **Burrito** | Tooling that wraps an Elixir release as a single-file cross-platform binary using Zig as the cross-compiler. |
| **Luerl** | Pure-Erlang implementation of Lua 5.3. Sandboxable, hot-reloadable, no NIF. |
