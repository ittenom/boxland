# Boxland — Master Plan (Initial Release Candidate)

> *Working name: Boxland. A 2D MMORPG engine and design suite. Authoritative Go server, thin Pixi-based web client, Templ+HTMX design tools. Native iOS client deferred to v1.1 (protocol-ready). 32px pixel-art aesthetic, zero vector curves.*

This plan is structured as **lists of work items, no estimates, no phasing**. You are the project manager — sequence and prioritize as you wish. Each section is independent enough to be picked up by a future spec or dev cycle.

---

## 1. Architecture & technology decisions (locked)

| Concern | Decision |
|---|---|
| Server language | **Go** (single binary, great concurrency, ideal for game tick loops) |
| Server scale target (v1) | 100–500 CCU per single-tenant deployment |
| Real-time protocol | **FlatBuffers over WebSocket** (zero-copy reads, versioned schemas) |
| WS message envelope | **Single `ClientMessage` union** (`verb` enum + `payload` bytes) after auth handshake; one dispatcher, one place to add verbs |
| Tick rate | **10 Hz** authoritative simulation |
| Movement | Free pixel coords + AABB entity colliders + named collision layers |
| Tile collision authoring | Per-edge bits, **authored via a small enum of collision-shape presets** (`Open`, `Solid`, `Wall N/E/S/W`, `Diagonal NE/NW/SE/SW`, `Half N/E/S/W`); enum expands to edge bits at load |
| Collision response | **Axis-separated swept AABB**; sliding via blocked-axis zeroing; identical algorithm in server and web (and iOS at v1.1) for prediction/reconciliation |
| Default entity collision mask | `land` (designers only touch the mask for flying/aquatic/phasing types) |
| ECS storage | **Sparse-set** (per-component `dense[]T` + `owners[]EntityID` + `sparse[]int32`); component kinds registered at init; tick-loop microbenchmark gated from day one |
| **Tiles are entities** | Tiles use the same ECS as everything else, with two extra components: `Tile{layer_id, gx, gy}` and `Static`. Movement systems skip `Static`; collision/render share one path. The `map_tiles` Postgres table remains for compact storage and bulk chunk loads. |
| Spatial index | Uniform grid (16-tile chunks) + per-player AOI subscription radius; **per-chunk version vector per subscriber** |
| Persistent store | **Postgres** for canonical persisted state (cold + flushed live state); **Redis** for sessions, pub/sub, and the WAL |
| Mutation WAL | **Redis Streams**, one stream per map instance (`wal:map:{map_id}:{instance_id}`); FlatBuffers-encoded entries; flush+`XTRIM MINID` every 20 ticks (~2s); `MAXLEN ~ 100000` safety bound; refuse new mutations if Postgres flush is failing |
| Live state warm-start | Postgres holds the canonical flushed state; Redis WAL holds mutations since last flush. **No separate `live_state_snapshots` table** — boot loads Postgres state and replays WAL. |
| State serialization | **One FlatBuffers `MapState` schema** used for wire snapshots, WAL checkpoints, and Postgres warm-state blobs. The wire `Snapshot` is a `MapState` clipped to AOI. |
| Asset blob storage | **CDN-fronted S3-compatible object storage** from day one; content-addressed paths (sha256) |
| Postgres access pattern | **Hybrid**: `sqlc` for hot paths (tick loop reads, WAL flush writes, AOI loads); a generic `Repo[T]` (~150 lines, on top of `pgx` + `squirrel`) for design-tool CRUD. Migrations: plain SQL files via `golang-migrate`. |
| Tenancy | **Single-tenant** (per-customer deployment) |
| Multiplayer scope | Public + Private maps, instancing per-user OR per-party (designer choice) |
| Map persistence | Persistent / Transient with per-map config (kick / origin-snap / leave players; per-resource reset toggles) |
| Map sizes | **Designer-defined, no nagging caps**; engine designed for arbitrary sizes via chunking |
| Map layers | Designer-defined named layers + a dedicated lighting layer |
| Entity model | **ECS** with components composed onto entities |
| Configurable JSON pattern | All `*_json` columns use a common **`Configurable[T]` pattern**: typed Go struct + `Descriptor()` (JSON-Schema-ish field info) + `Validate()`. Drives the generic form renderer; covers components, automations, reset rules, layout, palette recipes, custom flags, structured diffs. |
| Automations | Per-type (with type copy-paste); no-code AST that compiles to ECS systems |
| Scripting escape hatch | **None in v1**. Reserved as a future addition; `lua_script` column is **not** added to v1 schema. The no-code automation engine covers v1 needs. |
| Sandbox vs. live | **Same engine code, different data**: sandbox sessions operate on **draft copies in a sandbox-scoped instance id space** (`sandbox:<designer_id>:<map_id>`); live instances are isolated by id and AOI subscription manager refuses cross-realm subscription |
| Broadcast model | **One broadcaster, multiple subscriber policies**: Player (AOI radius, minimal fields), Designer (unbounded, inspector fields), Spectator (AOI radius, minimal fields, no own-entity). Sandbox HUD payloads come from the Designer policy — no parallel code path. |
| WS auth realms | **Realm-tagged connections**: designer-realm presents a one-shot **WS ticket** minted from session cookie via `POST /design/ws-ticket` (~30s TTL, designer-id+IP-bound); player-realm presents JWT; opcode dispatcher checks `connection.realm`, never the token alone |
| Designer changes → live | **Staged** (drafts) with explicit "Push to Live"; hot-swap **between ticks**, atomic per map instance; removed components dropped with structured warn-log; in-flight automations finish their current tick under the old AST |
| Artifact lifecycle | **One generic `Artifact[T]` pattern** for every designer-managed object (assets, entity types, maps, palettes, edge socket types, tile groups): create draft → edit → publish to live. One `drafts` table keyed by `(artifact_kind, artifact_id)`; one `Publishable[T]` interface for diff/validate/publish hooks; one publish pipeline. |
| Draft diff | Per-artifact-kind structured diff with human-readable summary lines (consumed by the diff preview modal) |
| Versioning of artifacts | None in v1 — latest only |
| Palette swap | **Pre-baked variants at publish time**: bake job emits a remapped PNG per (asset × palette variant), CDN-stored at content-addressed paths; `EntityState` carries `variant_id`. The `tint` field is **not** the variant — it is an optional secondary multiply color for runtime effects (damage flash, freeze, etc.) |
| Asset model | **One `assets` table** for sprites, tiles, and audio; kind-specific fields in a `metadata_json` column. Animation rows hang off sprite/tile assets; audio assets have no animations. |
| Web design tools UI | **Templ + HTMX + Alpine.js + custom pixel-CSS** (server-rendered) |
| Heavy interactive surfaces | Two TS modules: shared **PixiJS renderer** (Mapmaker, Sandbox, web game client) + small **Canvas2D pixel editor** (Asset Manager). Single shared WS/FlatBuffers client lib underneath. |
| Command bus | **One command-bus pattern** shared across pixel editor, Mapmaker, Sandbox, and game input — provides undo/redo, hotkey rebinding, gamepad rebinding, and the Cmd-K command palette uniformly. |
| Generic form renderer | **One Templ partial** that consumes a `Descriptor()` (JSON-Schema-ish: string/int/float/bool/enum/asset-ref/entity-ref/color) and renders a styled form. Used for component config, automation AST nodes, reset rules, palette recipes, etc. New component kinds and new automation actions get UI for free. |
| Renderer / DPI | **Always integer-scale** (1×/2×/3×…), nearest-neighbor only |
| Web controls | Configurable in Settings; defaults: WASD + arrow keys + click-to-move |
| iOS client | **Deferred to v1.1**; v1 ships web-only. Architecture remains iOS-ready (see §1a). |
| iOS controls (when built) | Tap-to-move with local pathfinding clipped to AOI; re-path on chunk-edge crossing; server still authoritative on collision |
| Designer auth | **Robust** (email+password, sessions, role-aware, audit-light) |
| Player auth | **Email/password + OAuth** (Google, Apple, Discord) — separate auth realm |
| Asset import | PNG + Aseprite JSON sidecar (auto-detect frames/tags/directions); TexturePacker JSON; free-tex-packer JSON; generic strips (horizontal/vertical/grid); raw PNG with manual config fallback |
| Pixel editor | **Bare minimum** for v1 (pencil/eraser/picker/undo) but built on the shared command bus |
| Audio | In scope: SFX + music upload; automation-triggered; positional/volumetric SFX (Web Audio panner) |
| Chat | None in v1 |
| Replay record/scrub | **Deferred to post-v1** |
| Persistence flush | Tick-batched mutation flushes (default 20 ticks = ~2s) |
| Observability | stdout/stderr structured logs only |
| Testing | Unit tests on core sim/ECS; collision determinism tests across server/web (and iOS at v1.1) using shared test vectors; manual QA elsewhere |
| Repo | **Mono-repo** with `/server`, `/web`, `/schemas`, `/shared`. (`/ios` added at v1.1.) |
| Bonus features confirmed | Font picker in Settings, control rebind, gamepad API (web), spectator mode |
| Hosting target | Railway primary; Docker-first so any container host works |
| Default font | `fonts/C64esque.ttf` |

### 1a. iOS-readiness invariants (enforced now so v1.1 slots in cleanly)

Even though iOS is deferred, the v1 build must satisfy these so that adding iOS later is purely additive:

- **All authoritative game state ships through FlatBuffers schemas in `/schemas/`.** No JSON over WS for game state. (Design-tool API is JSON over HTTP and iOS never hits it.)
- **The shared collision module's algorithm is documented as canonical pseudocode in `/schemas/collision.md`** (or co-located with the FBS files). The future Swift port has a single reference.
- **All assets are CDN-served at content-addressed URLs.** iOS hits the same URLs.
- **Auth flows are protocol-level, not transport-coupled.** Player JWT/refresh-token endpoints (`/auth/oauth/*`, `/auth/login`, `/auth/refresh`) work identically for any HTTP client.
- **No game logic assumes mouse + keyboard.** Tap-to-move and click-to-move both produce the same `Move` verb on the server. Gamepad input is a client-side mapper.
- **Vector test cases for collision determinism live in `/shared/test-vectors/collision.json`.** Both runtimes (web now, iOS at v1.1) consume the same data.
- **Levels and assets have no web-only escape hatches.** No "this works because the canvas is X pixels wide" shortcuts; all sizing flows from the integer-scale viewport rule.

---

## 2. Repository layout

```
boxland/
├── server/                  # Go monolith (single binary, multiple subcommands)
│   ├── cmd/boxland/         # main entrypoint
│   ├── internal/
│   │   ├── sim/             # ECS (sparse-set), tick loop, AOI grid, collision, pathfinding
│   │   ├── automation/      # no-code AST → ECS system compiler
│   │   ├── proto/           # FlatBuffers generated code (server side)
│   │   ├── ws/              # WebSocket gateway, realm-tagged sessions, AOI subscription, broadcaster (with subscriber policies)
│   │   ├── designer/        # design-tool HTTP handlers + Templ views; WS-ticket endpoint
│   │   ├── auth/            # designer auth + player auth (separate realms)
│   │   ├── assets/          # upload, parsing, CDN, palette bake (uses Repo[T])
│   │   ├── entities/        # entity-type CRUD, components catalog (uses Repo[T])
│   │   ├── maps/            # authored & procedural map storage; chunked WFC engine (uses Repo[T])
│   │   ├── persistence/     # sqlc-generated hot-path queries; Repo[T] generic; Redis live-state; Streams WAL; tick-batched flush
│   │   ├── publishing/      # generic Artifact[T] pipeline: drafts → live promotion, structured diff, palette bake invocation, hot-swap broadcast
│   │   └── configurable/    # Configurable[T] descriptor framework (drives the generic form renderer)
│   ├── views/               # Templ .templ files; includes the generic form-renderer partial
│   ├── static/              # compiled CSS, fonts, JS bundles
│   ├── migrations/          # plain SQL files (golang-migrate)
│   ├── queries/             # .sql files for sqlc
│   ├── sqlc.yaml
│   └── go.mod
├── web/                     # TypeScript modules (game client + heavy surfaces)
│   ├── src/
│   │   ├── net/             # WS + FlatBuffers client, reconnect, AOI handling
│   │   ├── render/          # PixiJS renderer (shared by Mapmaker, Sandbox, game)
│   │   ├── pixel-editor/    # Canvas2D pixel editor module
│   │   ├── mapmaker/        # Mapmaker-specific tools layered on render/
│   │   ├── sandbox/         # Sandbox HUD + privileged command palette
│   │   ├── game/            # Player web client
│   │   ├── input/           # Keyboard, mouse, gamepad input + rebind (via shared command bus)
│   │   ├── command-bus/     # Shared command/undo/hotkey infrastructure
│   │   ├── collision/       # Shared swept-AABB algorithm (mirrors server)
│   │   └── boot.ts          # Entry that picks the right module per page
│   ├── styles/              # pixel-CSS (no rounded corners, no shadows w/ blur)
│   └── vite.config.ts
├── schemas/                 # FlatBuffers .fbs — single source of truth
│   ├── world.fbs            # MapState, Snapshot (clipped MapState), Diff, EntityState, Tile, AudioEvent, LightingCell
│   ├── input.fbs            # Auth, ClientMessage union (verb + payload), verb enum
│   ├── design.fbs           # LivePublish + structured diff payload
│   └── collision.md         # canonical swept-AABB pseudocode (iOS reference)
├── shared/                  # palettes, default fonts, sample assets, test vectors
│   ├── fonts/               # C64esque.ttf (default), AtariGames, BIOSfontII, Kubasta, TinyUnicode
│   └── test-vectors/        # collision.json (and future cross-runtime vectors)
├── docker/                  # Dockerfile, docker-compose for local dev; pinned flatc version
├── railway.toml             # Railway deployment config
└── README.md

# /ios/ added at v1.1; reserves the layout from §7.
```

---

## 3. FlatBuffers schemas (single source of truth)

**`schemas/world.fbs`** — server → client (and used internally for WAL checkpoints + Postgres warm-state blobs)
- `MapState`: full state of a map instance — map id, tick, tiles[], entities[], lighting cells[]. Wire `Snapshot` is `MapState` clipped to subscriber's AOI.
- `Diff` (per-tick): added entities[], removed entity ids[], moved entities[] (id, x, y, anim_state), tile changes[], audio events[]
- `EntityState`: id, type_id, x, y, facing, anim_id, anim_frame, **variant_id** (palette variant; 0 = base), **tint** (optional secondary multiply color for runtime effects), nameplate?, hp_pct?
- `Tile`: layer_id, x, y, asset_id, frame, **collision_shape (u8 enum)**, edge_collisions (4 bits, derived at load from `collision_shape`), collision_layer_mask (u32)
- `AudioEvent`: sound_id, x, y (or null for non-positional), volume, pitch
- `LightingCell`: x, y, color, intensity (only for cells in lighting layer)

**`schemas/input.fbs`** — client → server
- `Auth`: token, **realm (designer/player)**, client_kind (web/ios), client_version
- `ClientMessage` (post-auth, single dispatch envelope): `verb` enum + `payload` bytes
  - Verbs: `JoinMap`, `Move`, `Interact`, `Action`, `Spectate`, `DesignerCommand`
  - `DesignerCommand` payloads (spawn-any, set-resource, take-control, teleport, freeze-tick, step-tick, godmode) are **rejected by the gateway unless `connection.realm == designer`**

**`schemas/design.fbs`** — design-tool live updates pushed to running games when "Push to Live" fires
- `LivePublish`: changeset id, affected map ids[], asset diffs[], entity-type diffs[], map diffs[], **palette-variant diffs[]**, structured human-readable summary lines[]

**`schemas/collision.md`** — canonical pseudocode reference (used by web `collision/` now and iOS `Collision/` at v1.1)

Versioning: every schema has a `protocol_version` field. Server rejects mismatched majors; minor versions are forward-compatible per FlatBuffers semantics. **`flatc` version pinned in `docker/` and referenced by the `justfile`** to prevent developer-machine drift.

---

## 4. Server / Go work items

### 4a. Core foundation
- [ ] Go module setup, golangci-lint config, `just dev`, `just test`, `just build`, `just bench`
- [ ] Single-binary `boxland` with subcommands: `serve`, `migrate`, `seed`
- [ ] Config via env vars + `.env.example` (Railway-friendly)
- [ ] Structured slog setup (stdout JSON in prod, pretty in dev)
- [ ] Postgres pool (`pgx`); **`golang-migrate`** for SQL migrations (locked)
- [ ] **`sqlc`** for hot-path queries; generated code under `internal/persistence/hotpath/`
- [ ] **`Repo[T]` generic** (~150 lines on `pgx` + `squirrel`) for design-tool CRUD: `Get`, `List(opts)`, `Insert`, `Update`, `Delete`; one-time reflection at init for column discovery, zero reflection per query
- [ ] **Redis client: `rueidis`** (locked); pub/sub channel conventions; Streams helpers
- [ ] Object storage abstraction (S3 SDK; pluggable backend; signed-URL helper for CDN)
- [ ] Graceful shutdown: drain WS, flush mutation buffer, snapshot live maps, trim WAL streams

### 4b. Auth — designer realm
- [ ] Designer accounts table (email, argon2id hash, role: owner/editor/viewer)
- [ ] Session cookie (httpOnly, SameSite=Strict, signed) for design tools
- [ ] Login / logout / password reset (email via SMTP; Mailpit for dev)
- [ ] CSRF protection for all design-tool POSTs (HTMX-aware: meta-tag CSRF token included on every request via `htmx:configRequest`)
- [ ] Audit-light: who-changed-what timestamps on every artifact
- [ ] **Designer WS ticket endpoint** (`POST /design/ws-ticket`): authenticated by session cookie; mints a short-lived (~30s) one-shot ticket bound to designer-id and source IP; consumed by WS gateway on connect

### 4c. Auth — player realm (separate from designer)
- [ ] Player accounts table (email, argon2id NULL allowed, OAuth provider links)
- [ ] **CHECK constraint**: `password_hash IS NOT NULL OR EXISTS (player_oauth_links row)`
- [ ] OAuth providers: Google, Apple, Discord (each behind a feature flag); endpoints under `/auth/oauth/*` so iOS can reuse them at v1.1
- [ ] Email verification flow
- [ ] Player session = JWT (short-lived) + refresh token; sent on WS connect
- [ ] Anonymous/guest tokens reserved as a future option (not enabled v1)

### 4d. Asset pipeline (one `assets` table, many kinds)
- [ ] Upload endpoint (multipart, max-size + content-type validation)
- [ ] Importer registry — pluggable parsers:
  - Aseprite JSON sidecar (json-hash & json-array; reads `frames`, `meta.frameTags`, directions, `meta.layers`, `meta.slices`)
  - TexturePacker JSON
  - free-tex-packer JSON
  - Generic strip layouts (horizontal / vertical / rows N / cols N / packed via grid)
  - Raw PNG with manual grid config UI
- [ ] Image normalization: enforce 32×32 cell, validate non-square inputs, error UX for bad dimensions
- [ ] Sprite sheet → frame index extraction (stored as DB rows, not re-parsed each request)
- [ ] CDN upload + content-addressed paths (sha256-based)
- [ ] Animation tag store (name, frame range, direction: forward/reverse/pingpong, FPS) — only for sprite/tile-kind assets
- [ ] **Palette variants**: extract unique colors per asset; named palette presets table; per-asset *recipe* rows (`palette_variants`) using the `Configurable[T]` pattern; publish-time **bake job** generates a remapped PNG per recipe, stored as a content-addressed `asset_variants` row
- [ ] Bake job is **idempotent** (content-addressed paths) and runs **inline in the publish transaction** with `sync.WaitGroup` parallelism — no external queue
- [ ] Bare-minimum pixel editor backend: store edits as a new asset version (PNG re-export); uses the shared command bus; new versions trigger re-bake of dependent variants
- [ ] Audio asset support: WAV/OGG/MP3 upload; metadata (duration, loopable, default volume) stored in `assets.metadata_json`

### 4e. Entity Manager
- [ ] Entity-type CRUD via `Repo[entity_types.EntityType]` (name, sprite asset+anim, **AABB collider box** (w, h, anchor), default collision mask, components, tags)
- [ ] Component catalog (built-in components: Position, Velocity, Sprite, Collider, Health, Inventory, AIBehavior, Spawner, Resource, Trigger, AudioEmitter, LightSource, …) — extensible via Go interface; each component declares a `Descriptor()` for the generic form renderer
- [ ] Entity-type copy-paste / duplicate
- [ ] Edge-socket type definitions (for procedural maps) — global to project, referenced by tile entity types
- [ ] Tile-group definitions (a "tile group" is a meta-tile composed of N×M tiles that act as one)

### 4f. Mapmaker — Authored mode
- [ ] Map CRUD via `Repo[maps.Map]` (name, dimensions, tile layers list, lighting layer toggle, public/private + instancing config, persistent/transient + reset config)
- [ ] Tile placement API (bulk apply + single)
- [ ] Per-tile property override store: **collision-shape preset enum** + per-tile collision-layer-mask overrides + custom flags — "this tile" vs "all matching tiles"; latter propagates back to Entity Manager
- [ ] Lighting layer cells (color + intensity)
- [ ] Map chunk model: 16×16 tile chunks for streaming and AOI; arbitrary map size via lazy chunks

### 4g. Mapmaker — Procedural mode
- [ ] Edge-socket compatibility matrix (designer-defined types; "field" connects "field", etc., with optional asymmetric sockets)
- [ ] Authored anchor regions (the user pins clusters of tiles that are the same across all seeds)
- [ ] **Chunked Wave Function Collapse engine** (Go implementation):
  - Operates per 64×64 region with seam constraints between chunks (constraints from neighbor chunk's edge cells)
  - Tile constraints from edge sockets
  - Anchor regions as initial collapsed cells
  - Backtracking + entropy-based selection with **bounded backtracking budget per chunk**
  - On budget exhaustion → reseed and retry; after **N reseeds** → surface structured error to designer
  - Seedable RNG (deterministic given anchors + seed)
- [ ] Live preview API (generate sample with seed, show in client)
- [ ] Persisted final state for procedural maps:
  - Persistent → seed stored, generated grid stored after first generation
  - Transient → seed regenerated per refresh window per the map's reset rules

### 4h. ECS simulation core
- [ ] **Sparse-set component storage** (locked): generic `ComponentStore[T]` with `dense []T`, `owners []EntityID`, `sparse []int32`; O(1) add/remove, dense-iteration queries; component kinds registered at server init from the catalog (§4e)
- [ ] **Tiles ARE entities**: a tile is an entity with `Tile{layer_id, gx, gy}` + `Static` + `Sprite` + `Collider` components. Movement systems skip `Static`; collision and rendering treat tiles and free entities identically. Storage on disk uses the compact `map_tiles` table; in-memory there's only one entity store.
- [ ] **Microbenchmark from day one**: "10k entities, Position+Velocity tick" target ≤1ms; CI fails on regression
- [ ] System scheduler: ordered list of systems run each tick (input → AI → movement → collision → triggers → audio events → AOI broadcast); no reflection in hot paths
- [ ] Uniform-grid spatial index (chunk size = 16 tiles); insertion/removal/query API
- [ ] **Collision algorithm — axis-separated swept AABB** (canonical pseudocode below; same algorithm in `web/src/collision/` and in `schemas/collision.md` for the future iOS port):
  ```
  move(entity, Δ):
    for axis in [x, y]:
      sweep entity.AABB along Δ[axis]
      for each tile T overlapped during sweep:
        if (T.collision_layer_mask & entity.mask) == 0: continue
        determine which edge of T the AABB crosses (sign of Δ[axis])
        if T.edge_bits has that edge set:
          clip Δ[axis] to the contact point; mark axis blocked; break
      apply Δ[axis]
    // result: smooth slide along walls, no jitter, no corner stick
  ```
  - Collision math in `int32` fixed-point (sub-pixel precision: 1 px = 256 units) for cross-platform determinism
  - Per-entity AABB comes from the entity *type* (not shipped per-tick in `EntityState`); clients look it up by `type_id`
  - One-way edges (e.g., south-edge-blocks-only-when-Δy>0) reserved as a future extension; not in v1
- [ ] AOI subscription per player: configurable radius in tiles; **per-chunk version vector per subscriber** — server tracks, for each subscriber, the last `(chunk_id, version)` it acknowledged; per-tick the broadcaster sends only chunks whose version advanced
- [ ] Pathfinding: A* on tile grid honoring entity's collision-layer mask (used by "move toward X" automations now, iOS tap-to-move at v1.1)

### 4i. No-code automation engine
- [ ] Trigger types: `EntityNearby`, `EntityAbsent`, `ResourceThreshold`, `Timer`, `OnSpawn`, `OnDeath`, `OnInteract`, `OnEnterTile`
- [ ] Action types: `Spawn`, `Despawn (consume)`, `MoveToward`, `MoveAway`, `SetSpeed`, `SetSprite`, `SetAnimation`, `SetVariant` (palette variant), `SetTint` (runtime multiply), `PlaySound`, `EmitLight`, `AdjustResource`
- [ ] Conditions: AND / OR / NOT / count-thresholds / range thresholds
- [ ] AST → ECS-System compiler at publish time (so live execution has zero interpretation overhead)
- [ ] Per-type definitions only; per-type duplicate keeps automations attached
- [ ] Each trigger and action declares a `Descriptor()`; the **generic form renderer** produces the editor UI for free — new triggers/actions get UI without writing Templ
- [ ] **No Lua scripting in v1.** Reserved as a post-v1 addition.

### 4j. Sandbox & live runtime
- [ ] **Same engine code path** for sandbox and live; isolation is via **data and instance id space**, not branching code
- [ ] Sandbox map instances live under `sandbox:<designer_id>:<map_id>` with **draft-data copies**; AOI subscription manager refuses player-realm subscribers to sandbox ids and refuses designer-realm "control" opcodes on live ids unless the designer holds the relevant role
- [ ] **Realm enforcement**: WS gateway tags every connection with `realm = designer | player` from the auth handshake (ticket vs JWT); designer-only opcodes are dispatched on `connection.realm == designer`, never on token claims
- [ ] **Sandbox HUD comes from the Designer broadcaster policy** (no parallel computation): unbounded subscription radius, inspector fields included
- [ ] **"Push to Live" pipeline** (one generic flow, all artifact kinds):
  - Walk every dirty draft via the `Artifact[T]` interface
  - Run per-artifact `Validate()`
  - Compute structured per-artifact-kind diff with human-readable summary lines (consumed by the diff preview modal)
  - Atomic publish transaction
  - Palette bake job runs inline (idempotent; skips unchanged variants)
  - Broadcast `LivePublish` to all running maps
  - **Hot-swap semantics**: applied between ticks at a tick boundary, atomic per map instance; in-flight automations finish their current tick under the old AST; removed components are dropped from existing entities with structured warn-log; assets reload by URL (CDN cache busts via content-addressed paths)
- [ ] **Realm-isolation invariant test**: integration test asserts that a player-realm token cannot subscribe to a sandbox instance and a designer-realm ticket on a live instance cannot dispatch sandbox-only ops without the role

### 4k. Persistence layer
- [ ] **Mutation WAL — Redis Streams** (`wal:map:{map_id}:{instance_id}`): single writer (the Go process owning the instance); each entry `{tick, seq, op, payload_fb}` where `op` is a `Mutation` table from `world.fbs`
- [ ] **Flush path**: every 20 ticks (~2s, tick-aligned), batch-write to Postgres in a single transaction (the canonical persisted state), then `XTRIM MINID` the stream up to the last-flushed `tick:seq`
- [ ] **Recovery on boot**: load Postgres canonical state for each map this process owns, then `XRANGE` the WAL forward and replay, then resume tick loop. **No separate snapshot table.**
- [ ] **Safety bound**: `XADD ... MAXLEN ~ 100000` (~2.7h at 10 Hz worst case); if approached, refuse new mutations rather than lose old ones
- [ ] State serialization uses the **`MapState` FlatBuffers schema** for both Postgres warm-state blobs and WAL checkpoint entries
- [ ] Per-map reset rules engine: configurable kick/origin-snap/leave for players, per-resource reset toggles, terrain edits reset toggle, inventories reset toggle (reset rules use `Configurable[T]`)

### 4l. WebSocket gateway
- [ ] FlatBuffers framing over WS binary
- [ ] **Realm-tagged connection objects**; opcode dispatcher checks `connection.realm` not token claims
- [ ] **Single `ClientMessage` dispatch**: read envelope → route by verb → check realm → invoke handler. One place to add a verb.
- [ ] Per-connection rate limit (mostly anti-bug, since designers are trusted)
- [ ] **Reconnect handshake** with last-tick token: if gap ≤ N ticks (configurable, default 600 = 1 min), resend Diffs since last tick; otherwise send a fresh Snapshot
- [ ] **One broadcaster, three subscriber policies** (Player / Designer / Spectator): chunk set + field set chosen by policy; same code path
- [ ] AOI subscription manager (movement → recompute subscribed chunks; only chunks whose version vector advanced are sent)
- [ ] Backpressure: if a client falls behind by > N ticks, drop intermediate diffs and resend Snapshot
- [ ] **Live updates to design tools** ride this same WS via `htmx-ext-ws` on the designer realm (publish progress, hot-swap notifications) — no separate SSE channel

### 4m. Spectator
- [ ] Spectator endpoint: subscribe to a map without an entity; uses the Spectator broadcaster policy
- [ ] **Authorization**: spectators allowed on public live maps for any authenticated player; private/sandbox maps require designer realm or explicit invite (recorded on the map config)

### 4n. `Configurable[T]` framework (small but used everywhere)
- [ ] Go interface: `Descriptor() FieldDescriptor[]`, `Validate() error`, `Diff(prev T) StructuredDiff`
- [ ] Field-type vocabulary: `string`, `int`, `float`, `bool`, `enum`, `asset_ref`, `entity_type_ref`, `color`, `vec2`, `range`, `nested`
- [ ] Used by: every `*_json` column (component config, automation AST nodes, reset rules, layout, palette recipes, custom flags, structured diffs)
- [ ] One Templ partial in `views/_form.templ` consumes the `FieldDescriptor[]` and renders a styled, hotkey-consistent form

### 4o. `Artifact[T]` lifecycle framework
- [ ] Generic `Artifact[T]` interface: `Kind() string`, `ID() int64`, `LoadDraft()`, `SaveDraft()`, `Publish(tx) error`, `RollbackDraft()`, uses `Configurable[T]` for the diff
- [ ] One `drafts` table keyed by `(artifact_kind, artifact_id)` storing JSON
- [ ] One publish pipeline iterates dirty drafts, computes diffs, runs publishes in a single transaction, broadcasts `LivePublish`
- [ ] Implemented by every designer-managed artifact kind (assets, entity types, maps, palettes, edge socket types, tile groups)

---

## 5. Web — design tools (Templ + HTMX + Alpine + pixel-CSS)

### 5a. Pixel-CSS design system (zero vector curves)
- [ ] Reset / base styles
- [ ] Typography scale using `C64esque.ttf` default + the other 4 fonts as `@font-face` choices
- [ ] Color tokens: a deliberately limited retro palette (saved as a default named palette in the app's palette presets table); high-color, high-contrast, lots of color per the brand brief
- [ ] Components: button (1-pixel borders, no border-radius, hard shadows only), input, select, tabs, modal, toast, tooltip, table, list, panel, splitter, badge, progress bar (chunky pixel segments), scrollbar (pixel-styled via `::-webkit-scrollbar` with pixel sprites; **Firefox falls back to native scrollbar — accepted**)
- [ ] Iconography: hand-pixeled 16×16 icons (placeholder set; the design team will iterate)
- [ ] Hotkey + focus conventions documented in a single reference doc; consistent across Asset Manager, Entity Manager, Mapmaker, Sandbox, Settings; enforced by the shared command bus
- [ ] Lorem ipsum for all copy slots; clearly marked `data-copy-slot` so a human can fill later; central `copy.json` stub collects all keys

### 5b. Shell & navigation
- [ ] App shell: top nav (Asset / Entity / Mapmaker / Sandbox / Settings); user menu; "Push to Live" button (with **structured-diff preview modal**: per-artifact-kind summary lines)
- [ ] Login / signup for designers (HTMX forms)
- [ ] Settings page: font picker (live preview), control rebinder, audio defaults, gameplay defaults

### 5c. Asset Manager
- [ ] Asset grid with tag filters, search, multi-select
- [ ] Upload modal (drag-drop, multi-file, parser auto-detect with override dropdown)
- [ ] Asset detail panel: animation preview (Canvas2D loop), tag editor, palette extractor, **palette variant manager** (define recipes via the generic form renderer; preview baked variant), duplicate, rename, delete
- [ ] Pixel-editor modal (Canvas2D, vanilla TS): pencil/eraser/picker/undo/redo via the shared command bus
- [ ] Palette presets manager (named palettes; apply across N selected assets to create variants in bulk)
- [ ] Audio asset variant: waveform preview (chunky pixel waveform), play/loop test, volume default

### 5d. Entity Manager
- [ ] Entity-type list + search + tag filter
- [ ] Entity-type editor: sprite + animation picker, **AABB collider box editor** (w, h, anchor visualizer overlaid on sprite), components panel (the **generic form renderer** drives every component's config UI from its `Descriptor()`), tags, automation editor (the same generic renderer drives every trigger and action), edge-socket assignment for tile-type entities
- [ ] Duplicate / "save as" with deep clone of automations
- [ ] Tile-group composer: drag tiles from Asset Manager into N×M grid; save as one entity
- [ ] Edge-socket-types editor (project-wide list)

### 5e. Mapmaker
- [ ] Map list + create-new dialog (name, size, layers, public/private + instancing config, persistent/transient + reset rules — all via the generic form renderer)
- [ ] Mapmaker page: PixiJS canvas + HTMX-rendered toolbars (tile palette, layer picker, mode toggle, properties panel)
- [ ] Authored mode: paint, fill, rect, eyedrop, eraser (all via the shared command bus); per-tile property editor — **collision-shape preset picker** plus collision-layer-mask checkboxes; "this tile" vs "all matching tiles"
- [ ] Procedural mode: socket-aware tile palette; anchor-region brush; "Generate preview with seed" button; "Lock seed" / "Reroll" controls; final-output preview; **WFC failure surface** (designer-readable error if N reseeds fail)
- [ ] Lighting layer mode: paint colored cells with intensity slider
- [ ] Save / publish-as-draft

### 5f. Sandbox
- [ ] Map picker; instance selector (creates a `sandbox:<designer_id>:<map_id>` instance with draft-data copies)
- [ ] Live game view (PixiJS, same module as the player web client)
- [ ] Designer HUD overlay: entity inspector (click any entity → its components/state, rendered via the generic form renderer), spawn palette, resource sliders, "control this entity" toggle, freeze-tick / step-tick controls, godmode toggle — all dispatched through the shared command bus
- [ ] Push-to-Live button visible here too (with structured-diff preview)

### 5g. Settings
- [ ] Font picker (5 fonts; live preview; per-user persistence via cookie + DB)
- [ ] Control rebind via the shared command bus (keyboard + gamepad)
- [ ] Audio defaults (master / music / sfx)
- [ ] Spectator preferences (free-cam vs follow)

---

## 6. Web — game client & shared TS modules

### 6a. Shared net library (`web/src/net/`)
- [ ] WebSocket client with auto-reconnect, exponential backoff
- [ ] FlatBuffers codec (generated TS code from `schemas/`)
- [ ] **Single `ClientMessage` envelope** sender; verb constants from generated FB code
- [ ] Subscription/AOI mailbox; **per-chunk version-vector** application against local cache
- [ ] Replay-on-reconnect handshake support (with the bounded-gap rule)

### 6b. Shared PixiJS renderer (`web/src/render/`)
- [ ] Pixi app bootstrapper with **integer-scale viewport** logic (resize → snap scale, nearest-neighbor)
- [ ] **Unified entity renderer** — tiles and free entities go through the same draw path; tiles batched by texture atlas, not by "tileness"
- [ ] Sprite renderer with animation state machine (driven by server-sent `anim_id` + `anim_frame`)
- [ ] **Variant texture lookup** (`variant_id` → CDN URL); secondary `tint` applied as a multiply color in a small fragment shader (used only for runtime effects, not for palette variants)
- [ ] Lighting layer compositor (multiply blend mode of lighting cells)
- [ ] Camera controls: follow-entity, free-cam (designer/spectator), nudge-by-pixel
- [ ] Debug overlay (collision boxes, AOI radius, entity ids) — visible only in Sandbox

### 6c. Shared collision module (`web/src/collision/`)
- [ ] Axis-separated swept AABB; **identical algorithm to server** (fixed-point math, same edge-bit interpretation, same slide rule)
- [ ] Determinism test suite consumes `/shared/test-vectors/collision.json` — same data the server tests use; same data the iOS module will consume at v1.1

### 6d. Shared command bus (`web/src/command-bus/`)
- [ ] `Command` interface: `id`, `do()`, `undo()`, `description`
- [ ] Hotkey binding registry (rebindable, persisted to user settings)
- [ ] Gamepad mapping registry
- [ ] Cmd-K command palette UI consumes the registry
- [ ] Used by: pixel editor, Mapmaker, Sandbox, game input

### 6e. Pixel editor module (`web/src/pixel-editor/`)
- [ ] Canvas2D editor, tools as `Command` objects on the shared bus
- [ ] Tools: pencil, eraser, color picker (eyedrop), bucket fill (deferred extension hook)
- [ ] Save → POST PNG buffer to server, reload asset

### 6f. Mapmaker module (`web/src/mapmaker/`)
- [ ] Layered on top of `render/`
- [ ] Tools: paint, rect, fill, eyedrop, eraser — all `Command` objects on the shared bus
- [ ] Procedural-mode overlays: socket badges on tile edges, anchor-region highlight, generation-preview ghosting
- [ ] Inspector: clicked tile → property panel (HTMX side panel triggered from canvas events)
- [ ] **Collision-shape preset visualizer**: hover/select a tile → ghost overlay of the resolved edge bits

### 6g. Sandbox module (`web/src/sandbox/`)
- [ ] Game view (uses `render/` + `net/`)
- [ ] HUD overlay panels (HTMX-driven, summoned via hotkeys from the shared bus)
- [ ] Designer command palette uses the shared Cmd-K palette; commands are tagged designer-only

### 6h. Web player client (`web/src/game/`)
- [ ] Login → server picker → map picker → game view
- [ ] Input: keyboard + mouse + click-to-move + gamepad — all `Command` objects on the shared bus; client-side prediction using the shared collision module; server reconciles
- [ ] Settings persisted to localStorage and synced to server
- [ ] Spectator mode flag → switches HUD chrome

### 6i. Build system
- [ ] Vite for TS bundling (one entry per page; tree-shaken shared modules); single `tsconfig.json` with path aliases (`@net/`, `@render/`, etc.) — no monorepo tooling
- [ ] **`flatc` build step** (pinned version) generating TS and Go outputs from `schemas/` (Swift output added at v1.1)
- [ ] Static assets (fonts, icons, base CSS) served from Go's `static/` with long-cache headers

---

## 7. iOS client (deferred to v1.1; layout reserved)

The iOS client is **not in v1 scope**. The server, schemas, and asset pipeline are built to be iOS-ready per §1a. When v1.1 begins, this section becomes the iOS work plan:

- [ ] Add `/ios/Boxland/` to the repo with `Net/`, `Render/` (SpriteKit), `Input/`, `Collision/`, `UI/` (SwiftUI)
- [ ] Add `flatc` Swift output to the build
- [ ] Apple platform compliance: Apple Sign-In primary alongside Google/Discord, App Tracking Transparency, ATS, all current Apple guidelines
- [ ] Login screens (email/password + OAuth via ASWebAuthenticationSession) hitting the same `/auth/oauth/*` endpoints the web uses
- [ ] SpriteKit scene with `magnificationFilter = .nearest`, integer-scale
- [ ] Tilemap renderer mirroring the web Pixi behavior (same FlatBuffers diffs)
- [ ] Variant texture lookup (matches web); optional `tint` multiply via `SKShader`
- [ ] Local A* pathfinding clipped to the AOI; re-path on chunk-edge crossing; server still authoritative on collision
- [ ] Tap entity → Interact verb on the shared `ClientMessage` envelope
- [ ] **Shared collision module** in Swift; consumes `/shared/test-vectors/collision.json` to assert determinism vs. server and web
- [ ] Settings: font picker (5 included fonts shipped in app bundle), audio levels
- [ ] Spectator mode UI

---

## 8. Database schema (initial sketch)

Tables, single-tenant — no `tenant_id` columns:

- `designers` (id, email, password_hash, role, created_at, updated_at)
- `designer_sessions` (token_hash, designer_id, expires_at)
- `designer_ws_tickets` (ticket_hash, designer_id, ip, expires_at, consumed_at)
- `players` (id, email, password_hash NULL, created_at) — **CHECK** (`password_hash IS NOT NULL OR EXISTS player_oauth_links`)
- `player_oauth_links` (player_id, provider, provider_user_id)
- `player_sessions` (refresh_token_hash, player_id, expires_at)
- `assets` (id, **kind** [sprite/tile/audio], name, content_addressed_path, original_format, **metadata_json**, created_by, updated_at, tags[]) — single table, kind-specific fields in `metadata_json`
- `asset_animations` (id, asset_id, name, frame_from, frame_to, direction, fps) — sprite/tile assets only
- `palettes` (id, name, colors[]) — named palette presets
- `palette_variants` (id, asset_id, name, palette_id?, source_to_dest_json) — *recipe* (the swap definition; uses `Configurable[T]`)
- `asset_variants` (id, asset_id, palette_variant_id, content_addressed_path, status [pending/baked/failed], baked_at) — *baked output* from the publish-time bake job
- `entity_types` (id, name, sprite_asset_id, default_animation_id, **collider_w, collider_h, collider_anchor_x, collider_anchor_y**, default_collision_mask, tags[])
- `entity_components` (entity_type_id, component_kind, config_json) — composition table; `config_json` uses `Configurable[T]`
- `entity_automations` (entity_type_id, automation_ast_json) — `Configurable[T]`
- `edge_socket_types` (id, name, color)
- `tile_edge_assignments` (entity_type_id, north_socket_id, east_socket_id, south_socket_id, west_socket_id)
- `tile_groups` (id, name, layout_json) — composed multi-tile entities
- `maps` (id, name, width, height, public, instancing_mode, persistence_mode, refresh_window_seconds?, reset_rules_json, mode [authored/procedural], seed?, spectator_policy)
- `map_layers` (id, map_id, name, kind [tile/lighting], ord)
- `map_tiles` (map_id, layer_id, x, y, entity_type_id, anim_override?, **collision_shape_override?**, **collision_mask_override?**, custom_flags_json?) — bulk-loaded by chunk; in-memory these become entities with `Tile` + `Static` components
- `map_anchor_regions` (id, map_id, region_json) — for procedural mode
- `map_lighting_cells` (map_id, layer_id, x, y, color, intensity)
- `map_state` (map_id, instance_id, state_blob_fb, last_flushed_tick, updated_at) — canonical persisted state, FlatBuffers `MapState`; the only durable game-state table (no separate `live_state_snapshots`)
- `drafts` (artifact_kind, artifact_id, draft_json, created_by, created_at) — used by the generic `Artifact[T]` pipeline for every artifact kind
- `publish_diffs` (id, changeset_id, artifact_kind, artifact_id, summary_line, structured_diff_json, created_at) — populated by Push-to-Live; consumed by the diff preview modal

(Indexes called out in the schema migration files later; obvious ones on `assets.tags`, `map_tiles(map_id, layer_id, x, y)`, `palette_variants(asset_id)`, `asset_variants(asset_id, palette_variant_id)`, AOI lookups, etc.)

---

## 9. Deployment

- [ ] Dockerfile for `boxland` server (multi-stage, distroless final, **pinned `flatc` in build stage**)
- [ ] `docker-compose.yml` for local dev (Postgres + Redis + Mailpit + Boxland + a local S3 like MinIO)
- [ ] `railway.toml` with services: web (Boxland binary), Postgres plugin, Redis plugin, S3-compatible (e.g., R2 via env vars)
- [ ] CDN config notes (Cloudflare in front of R2 is one well-trodden path)
- [ ] Environment variable reference doc

---

## 10. Cross-cutting policies & conventions

- **One primitive, many uses.** Whenever you face two near-duplicate mechanisms, look for the unification: tiles-as-entities, one `ClientMessage` envelope, one broadcaster with policies, one form renderer, one `Artifact[T]` lifecycle, one `Configurable[T]` framework, one command bus, one `MapState` schema, one `assets` table, one `Repo[T]` for CRUD.
- **Hotkeys & focus:** documented once, identical across all surfaces — enforced by the shared command bus. Tab order respects reading order. Esc closes modals. Cmd/Ctrl-K opens command palette everywhere. Number keys select palette swatches. `[` and `]` cycle layers in Mapmaker. Hotkey reference in Settings.
- **Color:** every surface uses the retro palette tokens; never plain white-on-black; lots of color per the brand brief.
- **No vector curves:** zero `border-radius`, zero `box-shadow` with blur, zero SVG icons (icons are pixel PNGs/spritesheets), zero font smoothing (use pixel fonts only with `font-smoothing: none; image-rendering: pixelated`).
- **Copy:** Lorem Ipsum throughout; `data-copy-slot="<key>"` on every text node a human will fill later. A central `copy.json` stub will collect all keys for translation later.
- **Determinism:** collision and movement math is fixed-point (1 px = 256 sub-units); the same algorithm runs on server, web (and iOS at v1.1); shared test vectors gate CI on all runtimes.
- **N+1 vigilance:** all list endpoints batch-load related rows (Repo[T] supports preload hints); tile and entity data shipped via chunked AOI; no per-entity DB hit during tick. Hot path goes through `sqlc`-generated batched queries only.
- **Tenant isolation:** N/A by config (single-tenant per deployment), but **designer-vs-player auth realms are strictly separated**. Connection objects are realm-tagged at handshake; opcode dispatch checks the realm on the connection, never the token claim. Sandbox instance ids live in a separate id space and the AOI manager refuses cross-realm subscription.
- **CSRF (HTMX):** `meta`-tag CSRF token included on every HTMX request via `htmx:configRequest`. All design-tool POSTs verify it.
- **iOS-readiness invariants** (§1a) are checked at PR review: no JSON over WS for game state, no transport-coupled auth, all assets at content-addressed CDN URLs, shared test vectors authoritative.

---

## 11. Open questions to resolve in subsequent specs (not now)

1. Specific component catalog (full enumeration of built-in components with their `Descriptor()`s) — small spec when starting Entity Manager.
2. Exact no-code automation UX visual layout — the generic renderer gives functional UI; design pass needed for polish.
3. Aseprite raw `.aseprite` file support — deferred behind the JSON sidecar path.
4. Versioning / publish-flow polish (drafts UX) — minimal in v1; will need a design pass.
5. iOS App Store metadata, TestFlight setup — deferred until v1.1.
6. One-way collision edges (e.g., south-edge-blocks-only-when-Δy>0 for ledge drops) — out of v1; reserve schema room.
7. Lua scripting reintroduction — out of v1; revisit if no-code automation hits a real ceiling.

---

## 12. Things explicitly OUT of scope for v1

- **iOS client** (deferred to v1.1; protocol-ready per §1a)
- **Lua scripting escape hatch** (no-code automation engine only)
- **Replay record / scrub**
- Versioning / history of artifacts
- Chat (any kind)
- Production-grade observability (Prometheus, OTEL, Sentry)
- E2E and load-test suites
- Multi-tenant SaaS hosting
- Anonymous/guest player accounts
- Full Aseprite-lite pixel editor (only bare-minimum tools ship)
- Player-driven UGC inside the game (only designers create content)
- One-way collision edges and per-tile sub-mask collision (per-edge bits + presets are sufficient for v1)


---

## 13. Linearized task order (every task depends only on prior tasks)

This is the build sequence. Tasks are small and independently verifiable. Where a task is structurally trivial but architecturally important (e.g., wiring a single new verb into the dispatcher), it gets its own line so the diff stays small. The numbering is just for reference; this is not a phase list.

### Foundation — repo, build, schemas, infra

1. Initialize mono-repo skeleton: `/server`, `/web`, `/schemas`, `/shared`, `/docker`, `README.md`, `LICENSE`, `.gitignore`.
2. Create `/shared/fonts/` and copy in the five `.ttf` files (already on disk under `boxland/fonts/`).
3. Create `/shared/test-vectors/collision.json` as an empty array — file exists so future tests have an import target.
4. Add `justfile` (https://just.systems) with empty recipes: `dev`, `test`, `build`, `bench`, `gen-fb`, `migrate`, `seed`. (Recipes fail informatively until later tasks fill them in.) `just` is the developer interface throughout the project — first-class Windows + macOS + Linux support, single-binary install.
5. Pin `flatc` version in `docker/flatc.Dockerfile`; add a `just gen-fb` recipe that runs flatc inside that container against `/schemas/`.
6. Write `schemas/world.fbs` with `protocol_version`, `MapState`, `Snapshot`, `Diff`, `EntityState`, `Tile`, `AudioEvent`, `LightingCell`, and `Mutation`.
7. Write `schemas/input.fbs` with `protocol_version`, `Auth`, `Verb` enum (initially just `JoinMap`), and `ClientMessage` (verb + payload).
8. Write `schemas/design.fbs` with `LivePublish`.
9. Write `schemas/collision.md` containing the canonical swept-AABB pseudocode block from §4h.
10. Run `just gen-fb`; verify Go and TS outputs land in `server/internal/proto/` and `web/src/net/proto/`. Commit generated code paths to `.gitignore`-aware build.
11. Initialize Go module `boxland/server`; add golangci-lint config; flesh out `just test` to run `go test ./...`.
12. Initialize Vite TS project at `/web/`; single `tsconfig.json` with path aliases (`@net/`, `@render/`, `@collision/`, `@command-bus/`); `just dev` runs both Go and Vite.
13. Write `docker/Dockerfile` (multi-stage, distroless final, pinned flatc); `docker/docker-compose.yml` with Postgres + Redis + Mailpit + MinIO + the boxland service.
14. Write `.env.example` with every variable used so far (DB URL, Redis URL, S3 creds, SMTP). `just dev` reads from `.env`.
15. Add `slog` setup with JSON-in-prod / pretty-in-dev based on env var.
16. Add `pgx` pool initializer reading from env; add a single `/healthz` HTTP endpoint that pings Postgres and returns 200/503.
17. Add `golang-migrate` runner wired to `just migrate`; create empty `server/migrations/` with a no-op `0001_init.up.sql` / `.down.sql`.
18. Add `rueidis` initializer reading from env; extend `/healthz` to also ping Redis.
19. Add S3-compatible object-storage client (AWS SDK v2) reading from env (works against MinIO locally, R2 in prod); add a signed-URL helper.

### Generic data infrastructure

20. Add `internal/persistence/repo/Repo[T]` generic: `Get`, `List(ListOpts)`, `Insert`, `Update`, `Delete` on top of `pgx` + `squirrel`. One-time reflection at init for column discovery; zero reflection per query. Unit tests hit a throwaway `_repo_test` table created in a test helper.
21. Add `sqlc.yaml` and `server/queries/` directory with one trivial query (`SELECT 1`) so codegen runs and the generated package compiles.
22. Add `internal/configurable` package: `FieldDescriptor` types (`string`, `int`, `float`, `bool`, `enum`, `asset_ref`, `entity_type_ref`, `color`, `vec2`, `range`, `nested`); `Configurable[T]` interface with `Descriptor()`, `Validate()`, `Diff(prev)`. Pure types, no UI yet.
23. Add `internal/publishing/artifact` package: `Artifact[T]` interface (`Kind`, `ID`, `LoadDraft`, `SaveDraft`, `Publish(tx)`, `RollbackDraft`); the in-process publish pipeline that walks dirty drafts. No artifact kinds wired up yet.
24. Migration: create `drafts(artifact_kind, artifact_id, draft_json, created_by, created_at)`.
25. Migration: create `publish_diffs(id, changeset_id, artifact_kind, artifact_id, summary_line, structured_diff_json, created_at)`.

### Designer auth + design-tool shell

26. Migration: create `designers(id, email, password_hash, role, created_at, updated_at)` and `designer_sessions(token_hash, designer_id, expires_at)`.
27. Implement designer signup/login/logout with argon2id hashing and signed httpOnly SameSite=Strict session cookies; password reset via Mailpit SMTP.
28. CSRF middleware: emit a meta tag with the token; verify on all design-tool POSTs.
29. Migration: create `designer_ws_tickets(ticket_hash, designer_id, ip, expires_at, consumed_at)`.
30. Implement `POST /design/ws-ticket`: authenticated by session cookie; mints a one-shot 30s ticket; returns to the client.
31. Add Templ to the build; create `views/_layout.templ` with HTMX + Alpine script tags, CSRF meta tag, and the (still-empty) pixel-CSS stylesheet link.
32. Build the pixel-CSS design system in `web/styles/`: reset, color tokens (the default retro palette), typography using `C64esque.ttf`, and the component library (button, input, select, tabs, modal, toast, tooltip, table, list, panel, splitter, badge, progress bar, scrollbar). Compile to a single CSS file served from Go's `static/`.
33. Build `views/_form.templ` — the **generic form renderer** that consumes a `FieldDescriptor[]` and emits a styled, hotkey-consistent form. Test it with a fixture descriptor.
34. Build the app shell `views/shell.templ` (top nav placeholder, user menu, "Push to Live" button stub) and a designer login page using the form renderer. End-to-end: a designer can sign up, log in, see the shell.
35. Hand-pixel a placeholder set of 16×16 PNG icons; add a sprite-sheet helper for in-Templ icon use.
36. Add HTMX hotkey + focus convention reference doc at `docs/hotkeys.md`; wire Esc-closes-modal and Tab order globally in the shell.

### Web command bus + Pixi + collision shared modules (used by every interactive surface)

37. Build `web/src/command-bus/`: `Command` interface (`id`, `do`, `undo`, `description`), undo stack, hotkey registry (rebindable, persisted to localStorage), gamepad mapping registry. Unit tests on the registry.
38. Build the Cmd-K command palette UI as a TS module that reads from the command bus.
39. Build `web/src/collision/` implementing the canonical swept-AABB pseudocode in fixed-point (1 px = 256 sub-units). Add a Vitest suite that consumes `/shared/test-vectors/collision.json` (empty for now; the suite asserts it can load the file).
40. Author the first ~30 collision test vectors in `/shared/test-vectors/collision.json` covering: walk-into-wall, slide-along-wall, diagonal-into-corner, mask-mismatch, half-blocker. The web Vitest suite must pass.
41. Build `web/src/render/`: Pixi bootstrapper with integer-scale viewport, nearest-neighbor, **unified entity renderer** (tiles and entities through one draw path), animation state machine driven by `(anim_id, anim_frame)`. No data source yet — drives off a fixture in-memory.
42. Add the variant texture lookup (`variant_id` → URL) and the secondary `tint` multiply fragment shader.
43. Add the lighting layer compositor (multiply blend mode).
44. Add the renderer's debug overlay (collision boxes, AOI radius, entity ids) gated by a runtime flag.

### Asset pipeline (single `assets` table, kinds via `metadata_json`)

45. Migration: create `assets(id, kind, name, content_addressed_path, original_format, metadata_json, created_by, updated_at, tags[])`.
46. Migration: create `asset_animations(id, asset_id, name, frame_from, frame_to, direction, fps)`.
47. Migration: create `palettes(id, name, colors[])`.
48. Migration: create `palette_variants(id, asset_id, name, palette_id, source_to_dest_json)`.
49. Migration: create `asset_variants(id, asset_id, palette_variant_id, content_addressed_path, status, baked_at)`.
50. Implement the asset upload endpoint (multipart, max-size, content-type validation) writing to MinIO/R2 at content-addressed paths.
51. Implement the importer registry interface and the **raw PNG with manual grid config** parser (the simplest one). End-to-end: upload a PNG, see frame rows in `asset_animations`.
52. Add the **generic strip layouts** parser (horizontal / vertical / rows N / cols N).
53. Add the **Aseprite JSON sidecar** parser.
54. Add the **TexturePacker JSON** parser.
55. Add the **free-tex-packer JSON** parser.
56. Implement image normalization: enforce 32×32 cell, validate non-square inputs, structured error responses.
57. Implement audio asset support: WAV/OGG/MP3 upload with metadata in `metadata_json`.
58. Implement the palette-variant bake job: takes `(asset_id, palette_variant_id)`, applies the recipe, writes the remapped PNG to a content-addressed path, upserts an `asset_variants` row. Idempotent. Runs inline with `sync.WaitGroup`.
59. Wire `Asset` as the first concrete `Artifact[T]` and the first user of `Repo[T]`. Drafts of asset metadata flow through the publish pipeline.
60. Build the Asset Manager UI: grid + tag filter + search + multi-select, upload modal (drag-drop, parser auto-detect with override), asset detail panel with animation preview (Canvas2D loop), tag editor, palette extractor, palette-variant manager (uses the generic form renderer). All editing operations go through the shared command bus.
61. Build `web/src/pixel-editor/`: Canvas2D editor with pencil/eraser/picker/undo as `Command` objects on the shared bus; "save" POSTs a PNG buffer to the server, which writes a new content-addressed asset and triggers re-bake of dependent variants.
62. Add the audio asset detail panel: chunky pixel waveform preview, play/loop test, default volume.

### Component catalog + Entity Manager

63. Migration: create `entity_types(id, name, sprite_asset_id, default_animation_id, collider_w, collider_h, collider_anchor_x, collider_anchor_y, default_collision_mask, tags[])`.
64. Migration: create `entity_components(entity_type_id, component_kind, config_json)`.
65. Define the component-kind registry interface in `internal/entities/components`: each component declares its name, Go struct, `Descriptor()`, and ECS storage type. Initial registrations: `Position`, `Velocity`, `Sprite`, `Collider`. (More components added in later tasks alongside their consumers.)
66. Wire `EntityType` as an `Artifact[T]` with `Repo[T]`.
67. Build the Entity Manager UI: list with search/tag filter; editor with sprite+animation picker, AABB collider visualizer overlaid on sprite, components panel driven by the **generic form renderer** (so adding a future component kind costs zero UI code), tags. Duplicate / save-as deep-clones automations (when those exist).
68. Migration: create `edge_socket_types(id, name, color)` and `tile_edge_assignments(entity_type_id, north, east, south, west)`.
69. Build the Edge-Socket-Types editor (project-wide list).
70. Add edge-socket assignment to the Entity Manager editor (only visible for tile-kind entities).
71. Migration: create `tile_groups(id, name, layout_json)`.
72. Build the Tile-Group composer (drag tiles into N×M grid, save as one entity).

### ECS simulation core

73. Implement `internal/sim/ecs`: sparse-set generic `ComponentStore[T]` with `dense`, `owners`, `sparse`. Unit tests for add/remove/query. Component kinds register at server init from the catalog.
74. Wire the four initial components (`Position`, `Velocity`, `Sprite`, `Collider`) into the ECS; basic spawn/despawn API.
75. Add the **microbenchmark** in `just bench`: 10k entities with `Position+Velocity` ticked once. Assert ≤1ms; CI fails on regression.
76. Implement the system scheduler: ordered list of systems run per tick; deterministic order (input → AI → movement → collision → triggers → audio → broadcast). Empty stub systems initially.
77. Implement the uniform-grid spatial index (16-tile chunks) with insert/remove/query. Unit tests on the chunk math.
78. Implement the **server-side swept AABB collision** in fixed-point, mirroring the canonical pseudocode. Unit test it against the same `/shared/test-vectors/collision.json` corpus the web tests use — both runtimes must agree byte-for-byte on resolved positions.
79. Add `Tile` and `Static` components to the registry. Movement system skips entities with `Static`.
80. Implement chunked AOI subscription with **per-chunk version vectors per subscriber**. Unit-test: a stationary subscriber receives a chunk once and never again until the chunk version advances.
81. Implement A* pathfinding on the tile grid honoring entity collision-layer mask. Unit tests on small fixtures.

### MapState persistence + WAL

82. Migration: create `map_state(map_id, instance_id, state_blob_fb, last_flushed_tick, updated_at)`.
83. Implement the `MapState` FlatBuffers serializer/deserializer in Go.
84. Implement the Redis Streams WAL: per-instance stream key, FlatBuffers `Mutation` entries, `MAXLEN ~ 100000` safety bound.
85. Implement the tick-batched flush: every 20 ticks, write canonical state to `map_state` in a single transaction, then `XTRIM MINID` the stream up to last-flushed `tick:seq`.
86. Implement the recovery-on-boot path: load `map_state`, replay `XRANGE` of the WAL forward, resume tick loop.
87. Implement the "refuse new mutations if Postgres flush is failing" backpressure — surfaces as a structured error to designers and a refused mutation result to clients.

### Player auth + WS gateway + broadcaster

88. Migration: create `players(id, email, password_hash NULL, created_at)` with the CHECK constraint, plus `player_oauth_links` and `player_sessions`.
89. Implement player email/password signup + email verification (Mailpit) + login that mints a short-lived JWT + refresh token.
90. Implement OAuth flows under `/auth/oauth/*` for Google, Apple, Discord (each behind a feature flag).
91. Implement the WebSocket gateway: accepts a binary-framed FlatBuffers connection; the **first message must be `Auth`** with `realm` + token; on success the connection is tagged with `realm`.
92. Implement the **single `ClientMessage` dispatcher**: read envelope → route by verb → check realm → invoke handler. Initially the only handler is `JoinMap` returning a fresh `Snapshot`.
93. Add the per-connection rate limiter and basic abuse logging.
94. Implement the **broadcaster with three subscriber policies** (Player, Designer, Spectator). The broadcaster reads from the AOI subscription manager, applies the policy to choose chunks and field set, and emits `Diff`s per tick.
95. Wire `Move` and `Interact` verbs into the dispatcher; movement system consumes input, runs collision, broadcaster emits diffs.
96. Implement reconnect handshake with last-tick token: gap ≤ 600 ticks → resend Diffs; otherwise full Snapshot. Plus the backpressure rule: client behind by > N ticks → drop intermediates and full Snapshot.
97. Add `htmx-ext-ws` integration on the designer realm: design-tool surfaces can subscribe to publish-progress and hot-swap notifications over the same gateway.

### Maps — authored mode

98. Migration: create `maps(id, name, width, height, public, instancing_mode, persistence_mode, refresh_window_seconds, reset_rules_json, mode, seed, spectator_policy)`.
99. Migration: create `map_layers(id, map_id, name, kind, ord)`.
100. Migration: create `map_tiles(map_id, layer_id, x, y, entity_type_id, anim_override, collision_shape_override, collision_mask_override, custom_flags_json)` with the index on `(map_id, layer_id, x, y)`.
101. Migration: create `map_lighting_cells(map_id, layer_id, x, y, color, intensity)`.
102. Wire `Map` as an `Artifact[T]` with `Repo[T]`. Map create-new dialog uses the generic form renderer (size, layers, public/private, instancing, persistence, reset rules — reset rules are a `Configurable[T]`).
103. Implement the chunked map loader: given a `(map_id, instance_id)`, materialize tiles into ECS entities with `Tile` + `Static` + `Sprite` + `Collider` components, batched by chunk.
104. Build the Mapmaker page shell: PixiJS canvas + HTMX-rendered toolbars (tile palette, layer picker, mode toggle, properties panel).
105. Implement authored-mode tools as `Command` objects on the shared bus: paint, rect, fill, eyedrop, eraser. Each tool emits a tile-placement mutation via the WS gateway.
106. Implement the per-tile property editor: collision-shape preset picker (`Open`, `Solid`, `Wall N/E/S/W`, `Diagonal NE/NW/SE/SW`, `Half N/E/S/W`), collision-layer-mask checkboxes, "this tile" vs "all matching tiles" toggle.
107. Implement the **collision-shape preset visualizer**: hovered/selected tile shows a ghost overlay of resolved edge bits.
108. Implement the lighting layer mode: paint colored cells with intensity slider.

### Maps — procedural mode (chunked WFC)

109. Migration: create `map_anchor_regions(id, map_id, region_json)`.
110. Implement the **chunked Wave Function Collapse engine** in `internal/maps/wfc`: 64×64 chunks with seam constraints, edge-socket constraints, anchor regions as initial collapsed cells, bounded backtracking budget per chunk, reseed-and-retry on budget exhaustion, structured error after N reseeds. Seedable RNG.
111. Implement the live-preview API: generate a sample chunk with a given seed, return as a `MapState` slice.
112. Add procedural mode to the Mapmaker: socket-aware tile palette, anchor-region brush, "Generate preview with seed" / "Lock seed" / "Reroll" controls, generation-preview ghosting overlay, designer-readable WFC failure surface.
113. Implement persistent vs transient procedural maps: persistent stores the generated grid after first generation; transient regenerates per refresh window per the map's reset rules.

### Spectator + game client + audio

114. Implement the Spectator WS verb and its broadcaster policy (free-cam or follow-player). Authorization: public live maps for any authenticated player; private/sandbox require designer realm or invite.
115. Build `web/src/net/`: WS client with auto-reconnect + exponential backoff, FlatBuffers codec, single `ClientMessage` envelope sender, AOI mailbox applying per-chunk version-vector diffs against local cache.
116. Build `web/src/game/`: login → server picker → map picker → game view that wires `net/` + `render/` + `collision/` + `command-bus/` together. Client-side prediction uses the shared collision module; server reconciles.
117. Add the input module (`web/src/input/`): keyboard, mouse, click-to-move, gamepad — every input is a `Command` on the shared bus, rebindable via Settings.
118. Build the Settings page (designer + player surfaces share it where applicable): font picker (live preview, persisted), control rebinder, audio defaults, spectator preferences.
119. Add positional audio in the renderer: SFX events from `AudioEvent` drive Web Audio panners; music tracks supported via the same path.
120. Add nameplates and `hp_pct` rendering when present on `EntityState`.
121. Add spectator UI affordances: switched HUD chrome, free-cam camera control, follow-player toggle.

### Automations

122. Define the trigger-type registry: `EntityNearby`, `EntityAbsent`, `ResourceThreshold`, `Timer`, `OnSpawn`, `OnDeath`, `OnInteract`, `OnEnterTile`. Each trigger declares a `Descriptor()`.
123. Define the action-type registry: `Spawn`, `Despawn`, `MoveToward`, `MoveAway`, `SetSpeed`, `SetSprite`, `SetAnimation`, `SetVariant`, `SetTint`, `PlaySound`, `EmitLight`, `AdjustResource`. Each action declares a `Descriptor()`.
124. Add the automation AST type and `Configurable[T]` implementation. Conditions: AND / OR / NOT / count-thresholds / range thresholds.
125. Migration: create `entity_automations(entity_type_id, automation_ast_json)`.
126. Implement the AST → ECS-System compiler: at publish time, each entity type's automations compile to an array of pre-bound system functions. Live execution has zero interpretation overhead.
127. Build the automation editor UI: the **generic form renderer** drives every trigger and action automatically because each declares a `Descriptor()`. Nested forms via HTMX, show/hide via Alpine. New triggers and actions get UI for free.
128. Add the remaining components needed by automations: `Health`, `Inventory`, `AIBehavior`, `Spawner`, `Resource`, `Trigger`, `AudioEmitter`, `LightSource`. Each registers with `Descriptor()` so the Entity Manager UI updates automatically.

### Sandbox + Push-to-Live

129. Implement the sandbox instance-id namespace (`sandbox:<designer_id>:<map_id>`); the AOI subscription manager refuses player-realm subscribers to sandbox ids.
130. Add the designer-only verbs (`spawn-any`, `set-resource`, `take-control`, `teleport`, `freeze-tick`, `step-tick`, `godmode`) to the dispatcher. Each is rejected unless `connection.realm == designer` and the role permits it.
131. Build the Sandbox UI: map picker, instance selector that creates a sandbox instance with **draft-data copies** of the artifacts; live game view (reuses `web/src/game/`); designer HUD overlay (entity inspector via the generic form renderer, spawn palette, resource sliders, "control this entity", freeze/step controls, godmode); designer command palette (Cmd-K).
132. Implement the publish pipeline (the generic flow): walk dirty drafts via `Artifact[T]`, run `Validate()`, compute structured diffs with summary lines, atomic publish transaction, run palette bake inline, broadcast `LivePublish` to running maps.
133. Implement the **between-tick atomic hot-swap** in the live runtime: at a tick boundary, swap entity-type definitions and re-bind component data; in-flight automations finish their current tick under the old AST; removed components are dropped from existing entities with a structured warn-log; assets reload by URL.
134. Build the Push-to-Live diff preview modal in the shell, consuming `publish_diffs.summary_line`. Visible from any design surface.
135. **Realm-isolation invariant test**: integration test asserts (a) a player-realm token cannot subscribe to a sandbox instance, (b) a designer-realm ticket cannot dispatch sandbox-only ops on a live instance without the relevant role, (c) crossing realms always produces a structured rejection rather than a silent drop.

### Cross-cutting tests + observability + deployment

136. Expand `/shared/test-vectors/collision.json` to a comprehensive corpus (slide along all four edge directions, all preset shapes, mask combinations, pathological corner cases). Server and web suites must pass identically.
137. Add the **realm-isolation invariant test** to CI.
138. Add a smoke integration test: bring up the docker-compose stack, sign up a designer, upload a sprite, define an entity type, create a map, place a tile, push to live, log in as a player, join the map, see the entity.
139. Confirm structured slog output covers: every WS connect/disconnect with realm, every publish with changeset id, every WAL flush with tick range, every WFC failure with seed and chunk coords.
140. Confirm graceful shutdown: SIGTERM drains WS, flushes mutation buffers, snapshots live maps, trims WAL streams. Test it.
141. Build the production Docker image (multi-stage, distroless final, pinned flatc); verify it boots against a Railway-like environment locally.
142. Write `railway.toml` with the four services (boxland, Postgres, Redis, R2/S3-compatible). Document Cloudflare-in-front-of-R2 as the recommended CDN config.
143. Write the environment-variable reference doc.
144. Write the hotkey reference doc, the brand-voice copy stub (`copy.json`), and a "how to add a new component" developer guide that demonstrates the zero-UI-code path through `Descriptor()`.
145. **iOS-readiness audit pass**: walk §1a's invariants and verify (no JSON over WS for game state, content-addressed CDN URLs, transport-agnostic auth, shared test vectors, no input-mode assumptions). Fix any drift before tagging v1.

---

End of v1 task list. v1.1 begins with the §7 iOS items.
