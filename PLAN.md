# Boxland — Master Plan (Initial Release Candidate)

> *Working name: Boxland. A 2D MMORPG engine and design suite. Authoritative Go server, thin Pixi-based web client, native Swift iOS client, Templ+HTMX design tools. 32px pixel-art aesthetic, zero vector curves.*

This plan is structured as **lists of work items, no estimates, no phasing**. You are the project manager — sequence and prioritize as you wish. Each section is independent enough to be picked up by a future spec or dev cycle.

---

## 1. Architecture & technology decisions (locked from Q&A)

| Concern | Decision |
|---|---|
| Server language | **Go** (single binary, great concurrency, ideal for game tick loops) |
| Server scale target (v1) | 100–500 CCU per single-tenant deployment |
| Real-time protocol | **FlatBuffers over WebSocket** (zero-copy reads, versioned schemas) |
| Tick rate | **10 Hz** authoritative simulation |
| Movement | Free pixel coords + AABB collision against tiles + named collision layers |
| Spatial index | Uniform grid (chunked) + per-player AOI subscription radius |
| Persistent store | **Postgres** (with Redis for live state, sessions, pub/sub) |
| Asset blob storage | **CDN-fronted S3-compatible object storage** from day one |
| Tenancy | **Single-tenant** (per-customer deployment) |
| Multiplayer scope | Public + Private maps, instancing per-user OR per-party (designer choice) |
| Map persistence | Persistent / Transient with per-map config (kick / origin-snap / leave players; per-resource reset toggles) |
| Map sizes | **Designer-defined, no nagging caps**; engine designed for arbitrary sizes via chunking |
| Tile collisions | Per-edge + named collision layers (`land`, `water`, `flying`, custom) |
| Map layers | Designer-defined named layers + a dedicated lighting layer |
| Entity model | **ECS** with components composed onto entities |
| Automations | Per-type (with type copy-paste); no-code AST that compiles to ECS systems |
| Scripting escape hatch | **gopher-lua**, sandboxed (no `os`/`io`/`net`) |
| Sandbox runtime | **Identical** to live runtime — no code paths diverge; designer has an HUD overlay with privileged commands |
| Designer changes → live | **Staged** (drafts) with explicit "Push to Live" |
| Versioning of artifacts | None in v1 — latest only |
| Web design tools UI | **Templ + HTMX + Alpine.js + custom pixel-CSS** (server-rendered) |
| Heavy interactive surfaces | Two TS modules: shared **PixiJS renderer** (Mapmaker, Sandbox, web game client) + small **Canvas2D pixel editor** (Asset Manager). Single shared WS/FlatBuffers client lib underneath. |
| Renderer / DPI | **Always integer-scale** (1×/2×/3×…), nearest-neighbor only |
| Web controls | Configurable in Settings; defaults: WASD + arrow keys + click-to-move |
| iOS client | **Native Swift + SwiftUI/SpriteKit** |
| iOS controls | Tap-to-move with pathfinding + tap entities to interact |
| Designer auth | **Robust** (email+password, sessions, role-aware, audit-light) |
| Player auth | **Email/password + OAuth** (Google, Apple, Discord) — separate auth realm |
| Asset import | PNG + Aseprite JSON sidecar (auto-detect frames/tags/directions); TexturePacker JSON; free-tex-packer JSON; generic strips (horizontal/vertical/grid); raw PNG with manual config fallback |
| Pixel editor | **Bare minimum** for v1 (pencil/eraser/picker/undo) but built on an extensible tool/command bus |
| Color swap | **Palette-based** with named, savable, reusable palette presets |
| Audio | In scope: SFX + music upload; automation-triggered; positional/volumetric SFX |
| Chat | None in v1 |
| Persistence flush | Tick-batched mutation flushes |
| Observability | stdout/stderr structured logs only |
| Testing | Unit tests on core sim/ECS; manual QA elsewhere |
| Repo | **Mono-repo** with `/server`, `/web`, `/ios`, `/schemas`, `/shared` |
| Bonus features confirmed | Font picker in Settings, control rebind, gamepad API (web), spectator mode, replay record/scrub |
| Hosting target | Railway primary; Docker-first so any container host works |
| Default font | `fonts/C64esque.ttf` |

---

## 2. Repository layout

```
boxland/
├── server/                  # Go monolith (single binary, multiple subcommands)
│   ├── cmd/boxland/         # main entrypoint
│   ├── internal/
│   │   ├── sim/             # ECS, tick loop, AOI grid, collision, pathfinding
│   │   ├── automation/      # no-code AST → ECS system compiler; Lua sandbox host
│   │   ├── proto/           # FlatBuffers generated code (server side)
│   │   ├── ws/              # WebSocket gateway, session, AOI subscription
│   │   ├── designer/        # design-tool HTTP handlers + Templ views
│   │   ├── auth/            # designer auth + player auth (separate realms)
│   │   ├── assets/          # upload, parsing (Aseprite/TexturePacker/strip), CDN
│   │   ├── entities/        # entity-type CRUD, components catalog
│   │   ├── maps/            # authored & procedural map storage; WFC engine
│   │   ├── persistence/     # Postgres repos, Redis live-state, tick-batched flush
│   │   ├── publishing/      # drafts → live promotion ("Push to Live")
│   │   └── replay/          # tick recorder + scrub server
│   ├── views/               # Templ .templ files (design tools)
│   ├── static/              # compiled CSS, fonts, JS bundles
│   ├── migrations/          # SQL schema migrations
│   └── go.mod
├── web/                     # TypeScript modules (game client + heavy surfaces)
│   ├── src/
│   │   ├── net/             # WS + FlatBuffers client, reconnect, AOI handling
│   │   ├── render/          # PixiJS renderer (shared by Mapmaker, Sandbox, game)
│   │   ├── pixel-editor/    # Canvas2D pixel editor module
│   │   ├── mapmaker/        # Mapmaker-specific tools layered on render/
│   │   ├── sandbox/         # Sandbox HUD + privileged command palette
│   │   ├── game/            # Player web client (consumes render/ + net/)
│   │   ├── input/           # Keyboard, mouse, gamepad input + rebind
│   │   └── boot.ts          # Entry that picks the right module per page
│   ├── styles/              # pixel-CSS (no rounded corners, no shadows w/ blur)
│   └── vite.config.ts
├── ios/                     # Swift app
│   └── Boxland/
│       ├── Net/             # WS + FlatBuffers (swift-flatbuffers)
│       ├── Render/          # SpriteKit scene; nearest-neighbor textures
│       ├── Input/           # tap-to-move, gesture recognizers
│       └── UI/              # SwiftUI screens (login, server pick, settings)
├── schemas/                 # FlatBuffers .fbs — single source of truth
│   ├── world.fbs            # tick snapshots, entity diffs
│   ├── input.fbs            # client → server messages
│   ├── design.fbs           # design-tool live updates (when needed)
│   └── replay.fbs           # replay file format
├── shared/                  # palettes, default fonts, sample assets
│   └── fonts/               # C64esque.ttf (default), AtariGames, BIOSfontII, Kubasta, TinyUnicode
├── docker/                  # Dockerfile, docker-compose for local dev
├── railway.toml             # Railway deployment config
└── README.md
```

---

## 3. FlatBuffers schemas (single source of truth)

**`schemas/world.fbs`** — server → client
- `Snapshot` (full state for a newly subscribed AOI): map id, tick, viewport bounds, tiles[], entities[], audio events[]
- `Diff` (per-tick): added entities[], removed entity ids[], moved entities[] (id, x, y, anim_state), tile changes[], audio events[]
- `EntityState`: id, type_id, x, y, facing, anim_id, anim_frame, tint (palette swap), nameplate?, hp_pct?
- `Tile`: layer_id, x, y, asset_id, frame, edge_collisions (4 bits) + collision_layer_mask
- `AudioEvent`: sound_id, x, y (or null for non-positional), volume, pitch
- `LightingCell`: x, y, color, intensity (only for cells in lighting layer)

**`schemas/input.fbs`** — client → server
- `Auth`: token, client_kind (web/ios), client_version
- `JoinMap`: map_id, instance_hint (party_id?)
- `Move`: vx, vy (normalized intent vector; server is authoritative on speed)
- `Interact`: target_entity_id?, world_x?, world_y?
- `Action`: action_id (number), payload bytes
- `Spectate`: target (player_id or "free-cam")
- `DesignerCommand`: opcode (spawn, set-resource, control-entity, etc.), payload

**`schemas/design.fbs`** — design-tool live updates pushed to running games when "Push to Live" fires
- `LivePublish`: changeset id, affected map ids[], asset diffs[], entity-type diffs[], map diffs[]

**`schemas/replay.fbs`** — append-only replay log
- header (map id, start tick, seed, entity-type catalog snapshot)
- frames: array of Diff with timestamps and originating-input markers

Versioning: every schema has a `protocol_version` field. Server rejects mismatched majors; minor versions are forward-compatible per FlatBuffers semantics.

---

## 4. Server / Go work items

### 4a. Core foundation
- [ ] Go module setup, golangci-lint config, `make dev`, `make test`, `make build`
- [ ] Single-binary `boxland` with subcommands: `serve`, `migrate`, `seed`, `replay-play`
- [ ] Config via env vars + `.env.example` (Railway-friendly)
- [ ] Structured slog setup (stdout JSON in prod, pretty in dev)
- [ ] Postgres pool (`pgx`), migration runner (`tern` or `golang-migrate`)
- [ ] Redis client (`rueidis` for perf or `go-redis`); pub/sub channel conventions
- [ ] Object storage abstraction (S3 SDK; pluggable backend; signed-URL helper for CDN)
- [ ] Graceful shutdown: drain WS, flush mutation buffer, snapshot live maps

### 4b. Auth — designer realm
- [ ] Designer accounts table (email, argon2id hash, role: owner/editor/viewer)
- [ ] Session cookie (httpOnly, SameSite=Strict, signed) for design tools
- [ ] Login / logout / password reset (email via SMTP; Mailpit for dev)
- [ ] CSRF protection for all design-tool POSTs (HTMX-aware)
- [ ] Audit-light: who-changed-what timestamps on every artifact

### 4c. Auth — player realm (separate from designer)
- [ ] Player accounts table (email, argon2id, OAuth provider links)
- [ ] OAuth providers: Google, Apple, Discord (each behind a feature flag)
- [ ] Email verification flow
- [ ] Player session = JWT (short-lived) + refresh token; sent on WS connect
- [ ] Anonymous/guest tokens reserved as a future option (not enabled v1)

### 4d. Asset pipeline
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
- [ ] Animation tag store (name, frame range, direction: forward/reverse/pingpong, FPS)
- [ ] Color-swap: extract unique colors per asset; named palette presets table; non-destructive remap stored per usage
- [ ] Bare-minimum pixel editor backend: store edits as a new asset version (PNG re-export); extension-ready command bus
- [ ] Audio asset support: WAV/OGG/MP3 upload; metadata (duration, loopable, default volume)

### 4e. Entity Manager
- [ ] Entity-type CRUD (name, sprite asset+anim, components, tags)
- [ ] Component catalog (built-in components: Position, Velocity, Sprite, Collider, Health, Inventory, AIBehavior, Spawner, Resource, Trigger, AudioEmitter, LightSource, …) — extensible via Go interface
- [ ] Entity-type copy-paste / duplicate
- [ ] Edge-socket type definitions (for procedural maps) — global to project, referenced by tile entity types
- [ ] Tile-group definitions (a "tile group" is a meta-tile composed of N×M tiles that act as one)

### 4f. Mapmaker — Authored mode
- [ ] Map CRUD (name, dimensions, tile layers list, lighting layer toggle, public/private + instancing config, persistent/transient + reset config)
- [ ] Tile placement API (bulk apply + single)
- [ ] Per-tile property override store (collision overrides, custom flags) — "this tile" vs "all matching tiles"; latter propagates back to Entity Manager
- [ ] Lighting layer cells (color + intensity)
- [ ] Map chunk model: 16×16 tile chunks for streaming and AOI; arbitrary map size via lazy chunks

### 4g. Mapmaker — Procedural mode
- [ ] Edge-socket compatibility matrix (designer-defined types; "field" connects "field", etc., with optional asymmetric sockets)
- [ ] Authored anchor regions (the user pins clusters of tiles that are the same across all seeds)
- [ ] Wave Function Collapse engine (Go implementation):
  - Tile constraints from edge sockets
  - Anchor regions as initial collapsed cells
  - Backtracking + entropy-based selection
  - Seedable RNG (deterministic given anchors + seed)
- [ ] Live preview API (generate sample with seed, show in client)
- [ ] Persisted final state for procedural maps:
  - Persistent → seed stored, generated grid stored after first generation
  - Transient → seed regenerated per refresh window per the map's reset rules

### 4h. ECS simulation core
- [ ] Component storage (archetype-based or sparse-set; pick one and document)
- [ ] System scheduler: ordered list of systems run each tick (input → AI → movement → collision → triggers → audio events → AOI broadcast)
- [ ] Uniform-grid spatial index (chunk size = 16 tiles); insertion/removal/query API
- [ ] AABB-vs-tile collision; sliding response; per-entity collision-layer mask vs per-tile collision-layer + edge bits
- [ ] AOI subscription per player (configurable radius in tiles); diff computed against last sent snapshot per player
- [ ] Pathfinding: A* on tile grid honoring entity's collision-layer mask (used by iOS tap-to-move and "move toward X" automations)

### 4i. No-code automation engine
- [ ] Trigger types: `EntityNearby`, `EntityAbsent`, `ResourceThreshold`, `Timer`, `OnSpawn`, `OnDeath`, `OnInteract`, `OnEnterTile`
- [ ] Action types: `Spawn`, `Despawn (consume)`, `MoveToward`, `MoveAway`, `SetSpeed`, `SetSprite`, `SetAnimation`, `SetTint`, `PlaySound`, `EmitLight`, `AdjustResource`, `RunLuaScript`
- [ ] Conditions: AND / OR / NOT / count-thresholds / range thresholds
- [ ] AST → ECS-System compiler at publish time (so live execution has zero interpretation overhead)
- [ ] gopher-lua sandbox host: per-script CPU-instruction quota, no `os`/`io`/`net`, restricted stdlib; exposes a curated `boxland.*` API
- [ ] Per-type definitions only; per-type duplicate keeps automations attached
- [ ] Automation editor JSON shape (server-side; UI consumes via HTMX)

### 4j. Sandbox & live runtime
- [ ] Sandbox is **literally** the live runtime; no separate code path
- [ ] Designer-only WS message types (gated by designer JWT + role): spawn-any, set-resource, take-control-of-entity, teleport, freeze-tick, step-tick, godmode
- [ ] HUD overlay payloads (entity inspector data, world state summary) computed by server, rendered HTMX-side
- [ ] "Push to Live" pipeline: drafts table → atomic publish transaction → broadcast `LivePublish` to all running maps; runtime hot-swaps entity-type definitions and re-binds component data; assets reloaded by URL (CDN cache busts)

### 4k. Persistence layer
- [ ] Mutation buffer per map (in-memory ring), flushed to Postgres every N ticks/seconds (configurable, default ~2s)
- [ ] Crash-recovery: on boot, replay any unflushed mutations from Redis WAL channel
- [ ] Snapshot format: per-map JSON-ish (or FlatBuffers) blob in Postgres for fast warm-start
- [ ] Per-map reset rules engine: configurable kick/origin-snap/leave for players, per-resource reset toggles, terrain edits reset toggle, inventories reset toggle

### 4l. WebSocket gateway
- [ ] FlatBuffers framing over WS binary
- [ ] Per-connection rate limit (mostly anti-bug, since designers are trusted)
- [ ] Reconnect with last-tick handshake (server resends Snapshot + diffs since last tick)
- [ ] AOI subscription manager (player movement → recompute subscribed chunks)
- [ ] Backpressure: if a client falls behind by > N ticks, drop intermediate diffs and resend Snapshot

### 4m. Spectator & replay
- [ ] Spectator endpoint: subscribe to a map without an entity; free-cam or follow-player
- [ ] Replay recorder: per-instance log of Snapshot + Diffs streamed to object storage
- [ ] Replay player server: streams a recorded log to a client at adjustable speeds with scrubbing

---

## 5. Web — design tools (Templ + HTMX + Alpine + pixel-CSS)

### 5a. Pixel-CSS design system (zero vector curves)
- [ ] Reset / base styles
- [ ] Typography scale using `C64esque.ttf` default + the other 4 fonts as `@font-face` choices
- [ ] Color tokens: a deliberately limited retro palette (saved as a default named palette in the app's palette presets table); high-color, high-contrast, lots of color per the brand brief
- [ ] Components: button (1-pixel borders, no border-radius, hard shadows only), input, select, tabs, modal, toast, tooltip, table, list, panel, splitter, badge, progress bar (chunky pixel segments), scrollbar (pixel-styled via custom JS or `::-webkit-scrollbar` with pixel sprites)
- [ ] Iconography: hand-pixeled 16×16 icons (placeholder set; the design team will iterate)
- [ ] Hotkey + focus conventions documented in a single reference doc; consistent across Asset Manager, Entity Manager, Mapmaker, Sandbox, Settings
- [ ] Lorem ipsum for all copy slots; clearly marked `data-copy-slot` so a human can fill later

### 5b. Shell & navigation
- [ ] App shell: top nav (Asset / Entity / Mapmaker / Sandbox / Settings); user menu; "Push to Live" button (with diff preview modal)
- [ ] Login / signup for designers (HTMX forms)
- [ ] Settings page: font picker (live preview), control rebinder, audio defaults, gameplay defaults

### 5c. Asset Manager
- [ ] Asset grid with tag filters, search, multi-select
- [ ] Upload modal (drag-drop, multi-file, parser auto-detect with override dropdown)
- [ ] Asset detail panel: animation preview (Canvas2D loop), tag editor, palette extractor, palette swap UI, duplicate, rename, delete
- [ ] Pixel-editor modal (Canvas2D, vanilla TS): pencil/eraser/picker/undo/redo + save-as-new-version
- [ ] Palette presets manager (named palettes; apply across N selected assets)
- [ ] Audio asset variant: waveform preview (chunky pixel waveform), play/loop test, volume default

### 5d. Entity Manager
- [ ] Entity-type list + search + tag filter
- [ ] Entity-type editor: sprite + animation picker, components panel (add/remove/configure), tags, automation editor (no-code visual builder using HTMX-rendered nested forms; Alpine for show/hide), edge-socket assignment for tile-type entities, Lua script slot (optional)
- [ ] Duplicate / "save as" with deep clone of automations
- [ ] Tile-group composer: drag tiles from Asset Manager into N×M grid; save as one entity
- [ ] Edge-socket-types editor (project-wide list)

### 5e. Mapmaker
- [ ] Map list + create-new dialog (name, size, layers, public/private + instancing config, persistent/transient + reset rules)
- [ ] Mapmaker page: PixiJS canvas + HTMX-rendered toolbars (tile palette, layer picker, mode toggle, properties panel)
- [ ] Authored mode: paint, fill, rect, eyedrop, eraser; per-tile property editor (collision overrides, "this tile" vs "all matching tiles")
- [ ] Procedural mode: socket-aware tile palette; anchor-region brush; "Generate preview with seed" button; "Lock seed" / "Reroll" controls; final-output preview
- [ ] Lighting layer mode: paint colored cells with intensity slider
- [ ] Save / publish-as-draft

### 5f. Sandbox
- [ ] Map picker; instance selector (or "create private instance")
- [ ] Live game view (PixiJS, same module as the player web client)
- [ ] Designer HUD overlay: entity inspector (click any entity → its components/state), spawn palette, resource sliders, "control this entity" toggle, freeze-tick / step-tick controls, godmode toggle
- [ ] Push-to-Live button visible here too (with diff preview)

### 5g. Settings
- [ ] Font picker (5 fonts; live preview; per-user persistence via cookie + DB)
- [ ] Control rebind (keyboard for desktop; gamepad mapping; iOS controls live in iOS app)
- [ ] Audio defaults (master / music / sfx)
- [ ] Spectator preferences (free-cam vs follow)

---

## 6. Web — game client & shared TS modules

### 6a. Shared net library (`web/src/net/`)
- [ ] WebSocket client with auto-reconnect, exponential backoff
- [ ] FlatBuffers codec (generated TS code from `schemas/`)
- [ ] Subscription/AOI mailbox; diff application against local cache
- [ ] Replay-on-reconnect handshake support

### 6b. Shared PixiJS renderer (`web/src/render/`)
- [ ] Pixi app bootstrapper with **integer-scale viewport** logic (resize → snap scale, nearest-neighbor)
- [ ] Tilemap renderer (chunked, only renders visible chunks)
- [ ] Sprite renderer with animation state machine (driven by server-sent `anim_id` + `anim_frame`)
- [ ] Tint / palette-swap shader (very small fragment shader; still pixel-perfect)
- [ ] Lighting layer compositor (multiply blend mode of lighting cells)
- [ ] Camera controls: follow-entity, free-cam (designer/spectator), nudge-by-pixel
- [ ] Debug overlay (collision boxes, AOI radius, entity ids) — visible only in Sandbox

### 6c. Pixel editor module (`web/src/pixel-editor/`)
- [ ] Canvas2D editor with command bus + undo/redo stack
- [ ] Tools: pencil, eraser, color picker (eyedrop), bucket fill (deferred extension hook)
- [ ] Save → POST PNG buffer to server, reload asset

### 6d. Mapmaker module (`web/src/mapmaker/`)
- [ ] Layered on top of `render/`
- [ ] Tools: paint, rect, fill, eyedrop, eraser (authored)
- [ ] Procedural-mode overlays: socket badges on tile edges, anchor-region highlight, generation-preview ghosting
- [ ] Inspector: clicked tile → property panel (HTMX side panel triggered from canvas events)

### 6e. Sandbox module (`web/src/sandbox/`)
- [ ] Game view (uses `render/` + `net/`)
- [ ] HUD overlay panels (HTMX-driven, summoned via hotkeys)
- [ ] Designer command palette (Cmd-K style; lists privileged commands)

### 6f. Web player client (`web/src/game/`)
- [ ] Login → server picker → map picker → game view
- [ ] Input (`web/src/input/`): keyboard + mouse + click-to-move + gamepad
- [ ] Settings persisted to localStorage and synced to server
- [ ] Spectator mode flag → switches HUD chrome

### 6g. Build system
- [ ] Vite for TS bundling (one entry per page; tree-shaken shared modules)
- [ ] flatc (FlatBuffers compiler) build step generating TS, Go, Swift outputs from `schemas/`
- [ ] Static assets (fonts, icons, base CSS) served from Go's `static/` with long-cache headers

---

## 7. iOS client

- [ ] Xcode project, SwiftUI app entry, SwiftPM dependencies (FlatBuffers, Starscream or URLSessionWebSocketTask)
- [ ] Login screens (email/password + OAuth via ASWebAuthenticationSession)
- [ ] Server picker
- [ ] Map picker
- [ ] **SpriteKit scene** as game view; pixel-perfect setup (`magnificationFilter = .nearest`, integer-scale)
- [ ] Tilemap renderer (mirror of web Pixi behavior; same FlatBuffers diffs in)
- [ ] Tap-to-move: server-side A* request OR local pathfind on cached tile collision (decision: local pathfind on the AOI tiles for responsiveness; server still authoritative on collision)
- [ ] Tap entity → Interact message
- [ ] Settings: font picker (5 included fonts shipped in app bundle), audio levels
- [ ] Spectator mode UI

---

## 8. Database schema (initial sketch)

Tables, single-tenant — no `tenant_id` columns:

- `designers` (id, email, password_hash, role, created_at, updated_at)
- `designer_sessions` (token_hash, designer_id, expires_at)
- `players` (id, email, password_hash NULL, created_at)
- `player_oauth_links` (player_id, provider, provider_user_id)
- `player_sessions` (refresh_token_hash, player_id, expires_at)
- `assets` (id, kind [sprite/tile/audio], name, content_addressed_path, original_format, grid_w, grid_h, frame_count, default_anim_fps, created_by, updated_at, tags[])
- `asset_animations` (id, asset_id, name, frame_from, frame_to, direction, fps)
- `palettes` (id, name, colors[]) — named palette presets
- `palette_swaps` (id, asset_id, source_color, dest_color, palette_id?) — non-destructive
- `audio_assets` (id, name, path, duration_ms, loopable, default_volume)
- `entity_types` (id, name, sprite_asset_id, default_animation_id, tags[])
- `entity_components` (entity_type_id, component_kind, config_json) — composition table
- `entity_automations` (entity_type_id, automation_ast_json, lua_script?)
- `edge_socket_types` (id, name, color)
- `tile_edge_assignments` (entity_type_id, north_socket_id, east_socket_id, south_socket_id, west_socket_id)
- `tile_groups` (id, name, layout_json) — composed multi-tile entities
- `maps` (id, name, width, height, public, instancing_mode, persistence_mode, refresh_window_seconds?, reset_rules_json, mode [authored/procedural], seed?)
- `map_layers` (id, map_id, name, kind [tile/lighting], ord)
- `map_tiles` (map_id, layer_id, x, y, entity_type_id, anim_override?, collision_override_json?) — bulk-loaded by chunk
- `map_anchor_regions` (id, map_id, region_json) — for procedural mode
- `map_lighting_cells` (map_id, layer_id, x, y, color, intensity)
- `live_state_snapshots` (map_id, instance_id, snapshot_blob, last_tick, updated_at)
- `drafts` (artifact_kind, artifact_id, draft_json, created_by, created_at)
- `replays` (id, map_id, instance_id, started_at, duration_s, path)

(Indexes called out in the schema migration files later; obvious ones on `assets.tags`, `map_tiles(map_id, layer_id, x, y)`, AOI lookups, etc.)

---

## 9. Deployment

- [ ] Dockerfile for `boxland` server (multi-stage, distroless final)
- [ ] `docker-compose.yml` for local dev (Postgres + Redis + Mailpit + Boxland + a local S3 like MinIO)
- [ ] `railway.toml` with services: web (Boxland binary), Postgres plugin, Redis plugin, S3-compatible (e.g., R2 via env vars)
- [ ] CDN config notes (Cloudflare in front of R2 is one well-trodden path)
- [ ] Environment variable reference doc

---

## 10. Cross-cutting policies & conventions

- **Hotkeys & focus:** documented once, identical across all surfaces. Tab order respects reading order. Esc closes modals. Cmd/Ctrl-K opens command palette in Sandbox. Number keys select palette swatches. `[` and `]` cycle layers in Mapmaker. Hotkey reference in Settings.
- **Color:** every surface uses the retro palette tokens; never plain white-on-black; lots of color per the brand brief.
- **No vector curves:** zero `border-radius`, zero `box-shadow` with blur, zero SVG icons (icons are pixel PNGs/spritesheets), zero font smoothing (use pixel fonts only with `font-smoothing: none; image-rendering: pixelated`).
- **Copy:** Lorem Ipsum throughout; `data-copy-slot="<key>"` on every text node a human will fill later. A central "copy.json" stub will collect all keys for translation later.
- **N+1 vigilance:** all list endpoints batch-load related rows; tile and entity data shipped via chunked AOI; no per-entity DB hit during tick.
- **Tenant isolation:** N/A by config (single-tenant per deployment), but designer-vs-player auth realms are strictly separated; designer cookies cannot authenticate WS as a player and vice-versa.

---

## 11. Open questions to resolve in subsequent specs (not now)

1. Specific component catalog (full enumeration of built-in components) — will write a small spec when starting Entity Manager.
2. Exact no-code automation UX (visual layout, drag-drop vs. dropdown-based) — design spec needed.
3. Aseprite raw `.aseprite` file support — deferred behind the JSON sidecar path.
4. Versioning / publish-flow polish (drafts UX) — minimal in v1; will need a design pass.
5. Replay UI scrubber design — separate design spec.
6. iOS App Store metadata, TestFlight setup — deferred until iOS milestone.

---

## 12. Things explicitly OUT of scope for v1

- Versioning / history of artifacts
- Chat (any kind)
- Production-grade observability (Prometheus, OTEL, Sentry)
- E2E and load-test suites
- Multi-tenant SaaS hosting
- Anonymous/guest player accounts
- Full Aseprite-lite pixel editor (only bare-minimum tools ship)
- Player-driven UGC inside the game (only designers create content)
