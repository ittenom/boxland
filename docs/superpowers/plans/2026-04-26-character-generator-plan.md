# Character Generator and Asset Pipeline — Implementation Plan

Source spec: `docs/superpowers/specs/2026-04-26-character-generator-design.md`

This plan resolves every ambiguity flagged during spec review. It plugs the
character system into existing Boxland infrastructure rather than creating
parallel concepts. Read alongside the spec — the spec defines *what*, this
plan defines *how* and *in what order*.

---

## 0. Resolved spec decisions

These are the answers I'll build to. They override anything the spec says to
the contrary.

| # | Topic | Decision |
|---|-------|----------|
| 1 | Lifecycle | Character parts, stat sets, talent trees, NPC templates, and the *baked sprite asset* register as new artifact `Kind`s. **No per-row `published` columns.** Designer save = upsert into `drafts`. Publish-to-Live runs the existing `Pipeline`. **Bake runs inside the publish transaction** (matches `assets/bake.go`). |
| 2 | Player catalog | New `GET /play/character-catalog` endpoint, scoped to (a) live-published character content AND (b) what's actually in the requesting player's inventory/unlocks. `/play/asset-catalog` is unchanged. |
| 3 | HUD bindings | Extend the binding parser to accept 4-part `entity:host:resource:<name>` and `character:<id>:stat:<key>`. Existing 3-part bindings continue to work byte-identically. |
| 4 | Frame contract | Each part declares `frame_map_json` mapping canonical-anim-name → source frame range. Ship a recommended default canonical set (idle + walk × 4 directions) that the part-registration UI offers as a one-click template. Bake composes only the canonical anims every selected part covers. |
| 5 | Slot vocabulary | Migration seeds the ~24 default slots into `character_slots`. Designers can edit/delete. `default_layer_order` per slot is the baseline; `character_parts.layer_order` overrides when non-null. |
| 6 | Recipe ownership | Recipes are library-shared. Editing a recipe re-bakes and updates every linked NPC template's `active_bake_id` atomically (dedup hash makes no-ops cheap). Player characters get their own per-player recipe row. |
| 7 | NPC ↔ entity_type | Saving an NPC template auto-mints a *draft* entity_type with default collider, `sprite_asset_id` = the bake asset. Designer can override later. |
| 8 | Talents | `currency_key` references a stat row of `kind=resource`. Talent nodes get a `mutex_group` string column; validation rejects spending into two nodes sharing a non-empty `mutex_group`. |
| 9 | Player controls | Tabs + visual preview + stat allocation + talent selection + summary + save/finalize + **Randomize**. No Reset, no Copy-JSON in player mode. |
| 10 | Hotkeys | Suppressed while typing. Designer adds shortcuts for animation/facing/slot traversal; documented in `docs/hotkeys.md`. |
| 11 | Stats formula | Numeric literals, stat refs, `+ - * /`, parens, plus a small allow-list of helpers: `clamp(x, lo, hi)`, `min(a,b)`, `max(a,b)`. Pure expression evaluator. No identifiers outside stat keys. |

---

## 1. Architecture at a glance

```
docs/superpowers/specs/2026-04-26-character-generator-design.md   (what)
docs/superpowers/plans/2026-04-26-character-generator-plan.md     (this file: how)

server/migrations/
  0034_characters.up.sql      ← all 9 new tables + seed slots, in one migration
  0034_characters.down.sql

server/internal/characters/   ← new domain package, mirrors server/internal/entities/
  definitions.go    Slot, CreationRules, owner kinds, validation constants.
  parts.go          CharacterPart row, slot compatibility, frame coverage helpers.
  recipes.go        Recipe row, canonical normalization, recipe_hash (sha256).
  bake.go           Composition + content-addressed asset upsert (uses persistence.ObjectStore).
  stats.go          StatSet, StatDefinition, formula evaluator, point-buy validation.
  talents.go        TalentTree, TalentNode, prerequisite + mutex_group validation.
  templates.go      NpcTemplate, PlayerCharacter rows + repo wiring.
  repo.go           Owner-scoped persistence helpers (player_id-aware queries).
  validate.go       Cross-entity validation surface used by handlers + artifact handlers.
  artifact.go       artifact.Handler implementations for each new artifact Kind.
  service.go        Service struct (constructed once at boot); aggregates Repo[T] handles.

server/internal/hud/
  binding.go        ← extended to accept 4-part bindings.
  binding_test.go   ← extended.

server/internal/designer/
  characters_handlers.go   ← new file. All /design/characters/* routes, written like
                             server/internal/designer/handlers.go entity-block.
  handlers.go              ← mount the new routes; add Characters service to Deps.

server/internal/playerweb/
  character_catalog.go        ← new GET /play/character-catalog.
  character_catalog_test.go
  handlers.go                 ← mount the new route.

server/internal/publishing/artifact/artifact.go
  +KindCharacterSlot, +KindCharacterPart, +KindCharacterStatSet,
  +KindCharacterTalentTree, +KindNpcTemplate     ← new Kind constants.

server/views/
  characters/
    list.templ             Designer list page.
    generator.templ        The Character Generator page (designer mode).
    components/...         Shared partials (slot picker row, stat row, talent node).

cmd/boxland/main.go
  - Construct characters.New(...) once at boot, pass into designer.Deps and playerweb.Deps.
  - publishRegistry.Register(charactersHandler) for each new artifact Kind.
  - publishPipeline.OnPostCommit hook: when a character bake publishes, broadcast
    a hot-swap to entity instances using its baked asset (analogous to existing
    EntityType broadcast).

web/src/character-generator/
  entry-character-generator.ts   Page boot for /design/characters/generator/{id}.
  state.ts                        Pure reducer (no DOM, fully unit-tested).
  preview.ts                      Pixi-based layered preview (uses @render).
  hotkeys.ts                      Registers character-generator commands on the shared CommandBus.
  validation.ts                   Mirrors server validate.go for fast client feedback.
  randomize.ts                    Deterministic-seeded randomizer.
  styles.css                      Plain CSS, bx-* class prefixes.
```

### Boundary notes

- **Recipes are NOT artifact rows.** They're plain mutable rows owned by their
  designer/player. Only the *outputs* (parts, stat sets, talent trees, NPC
  templates, baked sprite assets, auto-minted entity_types) flow through the
  publish pipeline. This keeps designer iteration fast and matches user
  decision #6.
- **Bakes are NOT artifact rows either.** A bake is the *result* of running an
  NPC-template publish handler. The handler's `Publish` function builds the
  PNG, writes it via `ObjectStore.Put`, and inserts/updates an `assets` row
  (kind=`sprite`) inside the publish tx. The new `assets` row goes through the
  publish pipeline's normal `entity_types`/`assets` plumbing on the *next*
  publish only if exposed; in practice the row is created live in the same tx,
  matching how `entities/artifact.go` updates `entity_types` directly.
  - Rationale: bakes deduplicate by `recipe_hash`. Sticking them in a separate
    `drafts` row would force two-phase staging that the designer doesn't see
    or care about.
- **Auto-minted entity_types DO go through drafts.** The first time an NPC
  template is saved (drafted), the handler upserts a `drafts` row of kind
  `entity_type` with the auto-derived `EntityTypeDraft` (name, sprite_asset_id
  = the bake asset id). The designer then publishes both at once. This keeps
  the existing entity_type lifecycle authoritative.

### Non-conflicts to be careful about

- The package has its own `bake.go`. The existing `assets/bake.go` is for
  *palette variants* and writes to `asset_variants`. Our character bake writes
  to `assets` (a new sprite row). Different output table, different inputs;
  share the `persistence.ContentAddressedKey` helper but no other code.
- The `assets` table has no `published` column. Adding character-bake outputs
  as `assets` rows means they're visible to anything that holds the id. The
  player catalog scoping (Phase 4) is what enforces "draft bakes don't leak."

---

## 2. Phased delivery

I'll deliver in five phases. Each phase ends with passing Go + Vitest tests
and a working manual-smoke surface; you can stop after any phase and ship
something useful.

| # | Phase | Why this order |
|---|-------|----------------|
| 1 | Schema + slot/part CRUD + designer list page | Foundation. No bake, no UI complexity. Confirms migrations + artifact wiring work end-to-end. |
| 2 | Recipe + bake pipeline + minimal designer Generator UI | The vertical slice spine. Composes a sprite, writes it through the publish pipeline, designer can see it in `/design/sandbox`. |
| 3 | Stats + talents | Layered on the existing recipe shell. Mostly server logic + form UI; no new graphics work. |
| 4 | Player-side character catalog + scoping audit | Closes the security loop. Adds the `/play/character-catalog` endpoint and the player-mode generator stub. |
| 5 | Polish + hotkeys + integration tests + docs | Hotkey docs, focus traps, randomizer, end-to-end integration tests. |

Within each phase I follow strict TDD: write the Go test (or Vitest spec)
first, watch it fail, then implement.

---

## 3. Phase 1 — Schema, slots, parts, designer list

### 3.1 Migration `0034_characters.up.sql`

One migration creates all nine tables. Defaults are baked in so future
columns can be added with `ADD COLUMN ... NOT NULL DEFAULT ...` cleanly.

Tables (key columns; PKs are `BIGSERIAL` unless noted):

```
character_slots
  id, key TEXT UNIQUE, label TEXT, required BOOL, order_index INT,
  default_layer_order INT, allows_palette BOOL,
  created_by BIGINT REFERENCES designers(id), created_at, updated_at.

character_parts
  id, slot_id REFERENCES character_slots(id) ON DELETE RESTRICT,
  asset_id BIGINT REFERENCES assets(id) ON DELETE RESTRICT,
  name TEXT, tags TEXT[] NOT NULL DEFAULT '{}',
  compatible_tags TEXT[] NOT NULL DEFAULT '{}',
  layer_order INT,                          -- nullable; null = inherit from slot
  frame_map_json JSONB NOT NULL,            -- canonical-anim → frame range
  palette_regions_json JSONB,               -- nullable
  created_by, created_at, updated_at.
  GIN index on tags.
  Unique (slot_id, asset_id).

character_recipes
  id, owner_kind TEXT CHECK IN ('designer','player'),
  owner_id BIGINT NOT NULL,
  name TEXT, appearance_json JSONB, stats_json JSONB, talents_json JSONB,
  recipe_hash BYTEA NOT NULL,           -- sha256
  created_by, created_at, updated_at.
  Index (owner_kind, owner_id).
  Index (recipe_hash).

character_bakes
  id, recipe_id REFERENCES character_recipes(id) ON DELETE CASCADE,
  recipe_hash BYTEA NOT NULL,
  asset_id BIGINT REFERENCES assets(id) ON DELETE SET NULL,
  status TEXT CHECK IN ('pending','baked','failed'),
  failure_reason TEXT,
  baked_at TIMESTAMPTZ, created_at, updated_at.
  Unique (recipe_hash) WHERE status = 'baked'.

character_stat_sets
  id, key TEXT UNIQUE, name TEXT,
  stats_json JSONB NOT NULL, creation_rules_json JSONB NOT NULL,
  created_by, created_at, updated_at.
  -- NO `published` column; lifecycle via artifact pipeline.

character_talent_trees
  id, key TEXT UNIQUE, name TEXT, description TEXT,
  currency_key TEXT,                       -- references a stat key in some stat_set
  layout_mode TEXT CHECK IN ('tree','tiered','free_list','web'),
  created_by, created_at, updated_at.

character_talent_nodes
  id, tree_id REFERENCES character_talent_trees(id) ON DELETE CASCADE,
  key TEXT, name TEXT, description TEXT,
  icon_asset_id BIGINT REFERENCES assets(id) ON DELETE SET NULL,
  max_rank INT NOT NULL DEFAULT 1,
  cost_json JSONB NOT NULL,
  prerequisites_json JSONB NOT NULL,
  effect_json JSONB NOT NULL,
  layout_json JSONB,
  mutex_group TEXT NOT NULL DEFAULT '',    -- empty = no group
  created_at, updated_at.
  Unique (tree_id, key).

player_characters
  id, player_id BIGINT REFERENCES players(id) ON DELETE CASCADE,
  recipe_id BIGINT REFERENCES character_recipes(id),
  active_bake_id BIGINT REFERENCES character_bakes(id),
  name TEXT NOT NULL,
  public_bio TEXT NOT NULL DEFAULT '',
  private_notes TEXT NOT NULL DEFAULT '',
  created_at, updated_at.
  Index (player_id).

npc_templates
  id, name TEXT NOT NULL,
  recipe_id BIGINT REFERENCES character_recipes(id),
  active_bake_id BIGINT REFERENCES character_bakes(id),
  entity_type_id BIGINT REFERENCES entity_types(id),
  tags TEXT[] NOT NULL DEFAULT '{}',
  created_by, created_at, updated_at.
  GIN index on tags.
```

**Seed:** the migration `INSERT`s the 24 default slots
(body/skin/face/eyes/eyebrows/mouth/hair_back/hair_front/facial_hair/
ears_horns/headwear/neck/torso_under/torso_outer/arms_gloves/legs/boots/
cloak/backpack/accessory_a/accessory_b/main_hand/off_hand/aura) with
human labels, `required` flags matching common sense (only `body` required),
ascending `order_index` and starter `default_layer_order` values spaced 10
apart so designers can wedge custom slots between them.

`0034_characters.down.sql` drops the tables in reverse FK order.

### 3.2 Go domain — `server/internal/characters/`

Skeleton mirrors `server/internal/entities/` exactly:

- `service.go`: `type Service struct { Pool *pgxpool.Pool; Slots, Parts,
  StatSets, TalentTrees, NpcTemplates, PlayerCharacters *repo.Repo[...];
  Store *persistence.ObjectStore; Assets *assets.Service }` plus
  `func New(...)`. Repo handles registered just like `entities.Service.Repo`.
- Row types with `db:"..." pk:"auto"` / `repo:"readonly"` tags exactly like
  `entities.EntityType`.
- `Validate()` on every typed input struct; small validation helpers in
  `validate.go`.

### 3.3 Artifact handlers — `characters/artifact.go`

Add five new `Kind` constants in `server/internal/publishing/artifact/artifact.go`:

```
KindCharacterSlot      = "character_slot"
KindCharacterPart      = "character_part"
KindCharacterStatSet   = "character_stat_set"
KindCharacterTalentTree = "character_talent_tree"
KindNpcTemplate        = "npc_template"
```

Each gets a `Handler` (`Kind/Validate/Publish`) following `entities/artifact.go`:

- `Validate` unmarshals `DraftJSON` into a typed `Draft` struct, calls
  `.Validate()` on it, runs cross-entity checks (e.g., a part's `slot_id`
  exists, a talent node's `prerequisites_json` references existing nodes).
- `Publish` opens-tx-aware: `tx.QueryRow(...)` to load prev state for diff,
  `tx.Exec(...)` to update the live row, `configurable.DiffJSON(prev, draft)`
  for the diff. Same shape as the entity_type handler.
- `KindNpcTemplate.Publish` is the special one: it
  1. Resolves the linked `recipe_id` and current `recipe_hash`.
  2. Looks up an existing successful bake for that hash inside the tx.
  3. If none, calls `bake.Run(ctx, tx, recipe, ...)` which composes the PNG,
     writes via `ObjectStore.Put`, inserts a new `assets` row with
     `kind = 'sprite'`, and inserts the `character_bakes` row.
  4. Updates `npc_templates.active_bake_id`.
  5. If `entity_type_id` is null, auto-mints a `drafts` row of kind
     `entity_type` for the same designer (a *draft*, even though we're
     mid-publish — this is the "designer can override later" path; on the
     *next* publish it goes live). If `entity_type_id` is non-null, updates
     the live entity_type row's `sprite_asset_id` directly.

Register handlers in `cmd/boxland/main.go` next to the existing
`publishRegistry.Register(...)` calls.

### 3.4 Designer routes — `designer/characters_handlers.go`

Routes (mounted in `designer/handlers.go` next to the entities block):

```
GET    /design/characters                       list page (parts + slots + stat_sets + talents + npcs)
GET    /design/characters/slots                 slot CRUD page
POST   /design/characters/slots                 create slot
DELETE /design/characters/slots/{id}            delete slot
POST   /design/characters/slots/{id}/draft      upsert slot draft

GET    /design/characters/parts                 part list
GET    /design/characters/parts/new             modal: pick slot + asset + canonical-set
POST   /design/characters/parts                 create part (live row, then drafts on edit)
GET    /design/characters/parts/{id}            part detail
DELETE /design/characters/parts/{id}            delete part
POST   /design/characters/parts/{id}/draft      upsert part draft

GET    /design/characters/stat-sets[...]        analogous CRUD
GET    /design/characters/talents[...]          analogous CRUD (tree + nodes)
GET    /design/characters/npc-templates[...]    analogous CRUD

GET    /design/characters/generator             new template (Phase 2)
GET    /design/characters/generator/{npcId}     edit existing template (Phase 2)
```

Each `POST .../draft` route inlines the same UPSERT into `drafts` that the
existing entities/assets/maps handlers use — no helper. Each calls
`writeDraftSavedToast(w, "<kind>")`.

### 3.5 Tests — Go (Phase 1)

In `server/internal/characters/`:

- `parts_test.go`: slot validation; part validation (missing asset, bad
  layer_order, frame_map shape); `frame_map_json` parser unit tests.
- `repo_test.go`: owner-scoped repo helpers for player_characters never
  return another player's row (table-driven, uses `testdb.New(t)`).
- `artifact_test.go`: each handler's `Validate` rejects bad drafts and
  `Publish` updates the live row inside a `pgx.Tx` (uses `testdb.New(t)` +
  manual `pool.Begin`). Mirrors `server/internal/entities/artifact_test.go`.

In `server/internal/designer/`:

- `characters_handlers_test.go`: smoke that each route hits the right helper
  and returns the right toast. Reuses the existing designer-handler test
  fixtures.

In `server/internal/publishing/artifact/`:

- Update existing `Kind` enum tests (any) to recognize the new constants.

### 3.6 Designer list page — `views/characters/list.templ`

Renders four cards (Slots, Parts, Stat Sets, Talent Trees, NPC Templates)
each showing live-row counts and any drafts. Matches the `views/shell.templ`
home dashboard idiom.

**Phase 1 done when**: I can sign in as a designer, register an existing
sprite asset as a character part for a slot, see drafts appear on the
publish-preview modal, and publish them live.

---

## 4. Phase 2 — Recipe + bake + designer Generator UI

### 4.1 Recipe normalization and hashing — `recipes.go`

```
type Recipe struct {
    ID         int64
    OwnerKind  string  // "designer" or "player"
    OwnerID    int64
    Name       string
    Appearance Appearance
    Stats      StatSelection
    Talents    TalentSelection
    Hash       []byte
}
```

`Normalize(r Recipe) []byte` produces canonical JSON: keys sorted, slot
selections sorted by slot_key, palette overrides sorted by region key, no
floating-point in stats (point-buy spends are ints), no time-dependent
fields. `Hash(r) []byte` returns `sha256.Sum256(Normalize(r))`. Both unit
tested with table cases that prove permutation-stability.

### 4.2 Bake — `characters/bake.go`

Pure function shape (called from `KindNpcTemplate.Publish`):

```
func Bake(
    ctx context.Context,
    tx pgx.Tx,
    deps BakeDeps,         // ObjectStore, Assets service, Pool fallback for read
    recipe Recipe,
) (BakeResult, error)
```

Steps:

1. Resolve every part's source asset via batched `Assets.ListByIDs(tx, ids)`.
   Use `WithTx` overload if it exists; otherwise read via the tx directly.
2. Resolve animations via `Assets.ListAnimationsByAssetIDs(tx, ids)`.
3. Compute the canonical animation set = intersection of every part's
   `frame_map_json` keys. Validation error if empty.
4. For each canonical animation × each frame index in that animation:
   - For each layered part in slot order (and `default_layer_order` /
     `layer_order` overrides): map canonical-frame → source-frame → source
     pixel rect; blit into the output sheet using nearest-neighbor-safe
     paletted PNG composition (Go `image.NRGBA` + `draw.Draw` with
     `draw.Over`).
5. Encode the composed sheet as PNG, capture bytes.
6. `outKey := persistence.ContentAddressedKey("character_bakes", body)`.
7. `Store.Put(ctx, outKey, "image/png", bytes.NewReader(body), int64(len(body)))`.
8. Build `assets.SheetMetadata{GridW:32, GridH:32, Cols:..., Rows:...,
   FrameCount:..., Source:"character_bake"}` and insert a new row in
   `assets` via `tx.Exec(...)` (kind=`sprite`, `created_by` = template's
   designer). Insert animation rows in `asset_animations` matching the
   canonical set.
9. Insert `character_bakes` row with `status='baked'`, `asset_id` = new
   asset id, `recipe_hash` = recipe.Hash. Use
   `ON CONFLICT (recipe_hash) WHERE status='baked' DO NOTHING` so a
   concurrent bake of the same recipe is harmless.

Failure path: any step before PNG encode → return validation error (no row
written). PNG encode or upload failure → insert `character_bakes` with
`status='failed'`, `failure_reason` populated, leave previous
`active_bake_id` untouched.

### 4.3 Server tests for Phase 2

- `recipes_test.go`: `Normalize` and `Hash` are stable across input
  permutation; differ when meaningful fields differ.
- `bake_test.go`: full integration via `testdb.New(t)` + MinIO `makeStore(t)`
  (skip if MinIO not up; see `assets/bake_test.go` for the exact pattern).
  Cases:
  - Two-part composition produces a deterministic PNG (compare to a fixture
    bytes-identical, regenerable via `go test -update`).
  - Same recipe twice → only one bake row, one object key.
  - Recipe with a part missing required canonical anim → validation error,
    no bake row.
  - Source asset deleted between recipe save and bake → validation error,
    not a panic.
  - Concurrent bakes of the same recipe → exactly one row, no error.

### 4.4 Designer Generator UI — `/design/characters/generator/{npcId}`

Server-rendered Templ page (`views/characters/generator.templ`) following
the spec's "90s RPG character sheet" layout. Three columns + tab strip +
action bar exactly per the spec ASCII mockup. All chrome uses `bx-*`
classes; jewel-tone slot tabs come from new CSS custom properties added in
the page-scoped stylesheet.

Client side: `web/src/character-generator/`

- `entry-character-generator.ts` — page boot. Reads npc id from
  `data-bx-npc-id`, fetches the recipe over a small JSON endpoint
  (`GET /design/characters/recipes/{id}`), constructs Pixi preview, wires
  up CommandBus.
- `state.ts` — pure reducer:
  ```
  type State = {
      animation: string;       // canonical anim key
      facing: Facing;          // 'south'|'north'|'east'|'west'
      selectedSlotId: number;
      slots: Map<number, SlotEntry>;       // slot_id → { partId, layerOrder, palette }
      stats: Record<string, number>;
      talents: Record<string, number>;     // node_key → rank
      validation: ValidationReport;
  };
  type Action =
    | { kind: 'select-slot'; slotId: number }
    | { kind: 'select-part'; slotId: number; partId: number }
    | { kind: 'set-palette'; slotId: number; palette: PaletteSelection }
    | { kind: 'allocate-stat'; key: string; delta: number }
    | { kind: 'set-talent-rank'; nodeKey: string; rank: number }
    | { kind: 'randomize'; seed: number }
    | { kind: 'reset' }
    | { kind: 'load'; recipe: RecipeSnapshot };
  function reduce(s: State, a: Action): State;
  ```
  100% headless; tested with Vitest only.
- `preview.ts` — uses `BoxlandApp` from `@render` and the `TextureCache`
  helper from `@render/textures` (already forces nearest-neighbor). Layers
  parts in real-time; not a baked sprite.
- `validation.ts` — mirrors server validate.go. Same error keys.
- `randomize.ts` — seeded LCG; given a seed and a slot vocabulary, picks
  one part per non-empty slot.

Hotkeys (registered on the existing `CommandBus` per Phase 5):

| Key       | Command                            | whileTyping |
|-----------|------------------------------------|-------------|
| `←` / `→` | Cycle slot                         | false       |
| `↑` / `↓` | Cycle part within slot             | false       |
| `[` / `]` | Cycle animation                    | false       |
| `1`–`4`   | Set facing (S, W, N, E)            | false       |
| `R`       | Randomize                          | false       |
| `Mod+S`   | Save draft                         | false       |
| `Mod+Shift+S` | Save & request publish (toast → preview modal) | false |
| `?`       | Show keymap overlay                | false       |

Documented in `docs/hotkeys.md` (new "Character Generator" section).

### 4.5 Vitest tests for Phase 2

`web/src/character-generator/state.test.ts`:
- Reducer: select-slot persists, select-part replaces only the targeted
  slot, randomize is deterministic for the same seed, reset clears.
- Preview layer ordering matches slot `default_layer_order` then part
  `layer_order` override.

`web/src/character-generator/validation.test.ts`:
- Required slot empty → error.
- Part incompatible with another via `compatible_tags` → error.
- Stat overspend → error.
- Talent prereq missing → error.
- Two nodes in same `mutex_group` both ranked → error.

**Phase 2 done when**: a designer can open the Generator on a new NPC
template, drag-or-click parts into slots, see the live preview update, save
a draft, hit Push-to-Live, and see the baked sprite appear in
`/design/sandbox` as a spawnable entity_type.

---

## 5. Phase 3 — Stats and talents

### 5.1 Stats formula evaluator — `characters/stats.go`

```
type StatSet struct { ... StatDefs []StatDef; CreationRules CreationRules }
type StatDef struct {
    Key, Label string
    Kind StatKind            // core | derived | resource | hidden
    Default, Min, Max int
    CreationCost int
    DisplayOrder int
    Formula string           // empty for core/resource
    Cap *int
}
```

`Eval(formula string, scope map[string]int) (int, error)`:

- Tokenizer: integers, identifiers (a-z + digits + _), `+ - * / ( ) ,`.
- Parser: recursive-descent for expr/term/factor with the four binary ops
  and the three named helpers (`clamp`, `min`, `max`). No other identifiers.
- All ops use integer math (`/` rounds toward zero). Helpers truncate too.
- Errors: `unknown identifier "Foo"`, `division by zero`, `unexpected token`.
  Errors include a 1-based column for designer feedback.

Tested with a table covering: literals, addition, precedence, parens, all
three helpers, unknown identifier, divide-by-zero, missing operand, deeply
nested expressions.

### 5.2 Point-buy validation

`ValidateAllocation(set StatSet, alloc map[string]int) error`:

- Each core stat `default + alloc[key]` must be in `[Min, Max]`.
- `sum(alloc[k] * StatDefs[k].CreationCost)` must equal `CreationRules.Pool`.

Tested as a pure function.

### 5.3 Talents — `characters/talents.go`

Validation:

- `prerequisites_json` graph (DAG of node_key → required_rank) is acyclic.
- Selecting a node requires every prereq met at the named rank.
- For any non-empty `mutex_group`: at most one node in the group may have
  rank > 0 in the recipe's selection.
- `cost_json` resolves against a configured currency; total spend ≤ pool.
- `effect_json` is validated structurally (one of: `stat_mod`, `resource_max`,
  `add_tag`, `set_flag`, `unlock_action_key`); no code execution.

Per-tree currency is a stat reference: `currency_key` must match a stat
def in the linked stat set with `kind = 'resource'`. Validation step in the
NPC template handler ensures every linked tree's currency is satisfiable.

### 5.4 UI

Add tabs **Sheet** and **Talents** to the generator. Both render
server-side from the loaded stat_set and talent_tree, plus a small
`talents-graph.ts` widget that draws the node graph in plain DOM (no SVG
library — `<div>`s positioned from `layout_json`).

Vitest covers stat-allocator increment/decrement (with caps), talent rank
toggling, prereq highlighting, mutex disabling.

**Phase 3 done when**: a designer can attach a stat set and talent tree to
an NPC template, allocate stats inside the rules, pick talents with prereqs
respected, and the validation panel reflects every rule in real time.

---

## 6. Phase 4 — Player catalog and player-mode generator stub

### 6.1 `GET /play/character-catalog`

Mounted in `playerweb/handlers.go`:
```
mux.Handle("GET /play/character-catalog", auth(getCharacterCatalog(d)))
```

Behavior, pulled together to avoid N+1:

1. `playerID := PlayerFromContext(ctx).ID` (RequirePlayer guarantees).
2. Read query: optional `slot_keys=body,hair_front` filter.
3. SQL (one round trip): inner-join `character_parts` to a player-inventory
   view that lists exactly which parts a given player may use. The
   inventory view is a thin SQL view; first-cut implementation grants every
   *live-published* part to every player (the inventory mechanics are
   deferred per spec §Deferred), but the view is the seam where future
   inventory rules plug in.
4. Batched `assets.Service.ListByIDs(ctx, assetIDs)` for the source
   sprites of the visible parts.
5. Batched `assets.Service.ListAnimationsByAssetIDs(ctx, assetIDs)`.
6. Response shape mirrors `playerweb/catalog.go`'s `catalogAsset` plus a
   `parts: [{id, slot_key, name, asset_id, frame_map}]` field per asset.

Caching: `Cache-Control: public, max-age=60, stale-while-revalidate=600`,
identical to the existing asset catalog.

Critical: the player catalog **never** queries `character_parts` outside the
inventory join. It does not look at draft rows. It does not look at
unlinked recipes or bakes. This is the audit's central guarantee.

### 6.2 Player-mode generator stub

Routes `/play/characters/new` and `/play/characters/{id}/edit` reuse the
same Templ page as designer mode but with a `data-bx-mode="player"`
attribute. Client-side:

- The same reducer, the same preview, the same Pixi setup.
- `entry-character-generator.ts` reads the mode and:
  - Hides Copy-JSON and Reset.
  - Shows Randomize and Save/Finalize.
  - Hides debug toggles, raw IDs, formula authoring, bake diagnostics.
  - Fetches catalog from `/play/character-catalog` instead of designer
    routes.

Player save POSTs to `POST /play/characters` (or `PATCH /play/characters/{id}`),
which writes a `player_characters` row scoped by `player_id` from
context. **Never** trusts the body's `player_id`. Bake runs synchronously
inside the same request because there is no publish step for player rows;
the player owns their character, not the realm. (This is a small carve-out
from the artifact pipeline justified by user decision #2 saying player
rows live outside the publish flow.)

For Phase 4 this can be a stub: only the catalog fetch + the very-minimal
form is wired; the full player flow ships in a later vertical slice. The
spec lists "Full player onboarding flow" as deferred — I'll deliver the
endpoint surface and one screen so the integration tests can exercise the
boundary, but won't build the polished player flow now.

### 6.3 Tests

`server/internal/playerweb/character_catalog_test.go`:
- Anonymous → 302.
- Authed player → only sees published parts.
- Authed player → does not see another designer's draft parts (set up via
  `drafts` row only, no live row).
- Inventory view filter applied (seed a row marking part as not-owned →
  not in response).
- Batching: a single `ListByIDs` and a single `ListAnimationsByAssetIDs`
  call regardless of how many parts (assert via captured calls if a
  spying interface is feasible; otherwise via a query-count helper on
  pgxpool).

`server/internal/characters/repo_test.go`:
- Player A cannot read Player B's `player_characters` via repo helpers.

### 6.4 HUD binding extension — `hud/binding.go`

Concretely:

- Replace the shape gate `len(parts) < 2 || len(parts) > 3` with
  `len(parts) < 2 || len(parts) > 4`.
- For `entity:host:resource:<name>`: when `parts[0]=="entity"` and
  `len(parts)==4`, require `parts[2]=="resource"`,
  `validateBindingKey(parts[3])`. Encode by extending `BindingRef` with a
  `Detail string` field (default empty); update `String()` to include it
  only when non-empty. Existing 3-part forms keep `Detail==""` and round-trip
  byte-identically.
- Add `BindCharacter BindingKind = "character"`. Accept
  `character:<id>:stat:<key>` (numeric id; `parts[2]=="stat"`;
  `validateBindingKey(parts[3])`).
- `ParseTemplateBindings` needs no changes (it slices on `{`/`}`).

Tests in `binding_test.go` extended:
- New cases for both 4-part shapes.
- Existing 3-part and 2-part cases still pass.
- `Map<string,...>` round-trip stability for old forms.

Client-side mirror: search `web/src/` for `validEntityResource`-equivalent
strings; whichever picker UI lists resource names also needs to learn
`resource:<name>` and `character:<id>:stat:<key>`. The specific file isn't
located in the research pass; one ripgrep + one edit will find and fix it.

**Phase 4 done when**: a player route returns only published parts the
player owns, the HUD parser accepts the two new binding shapes, and a
spawned character entity in the live game can bind a `resource_bar` to
`entity:host:resource:focus`.

---

## 7. Phase 5 — Polish, randomizer, hotkey docs, integration tests

### 7.1 Hotkey docs

Append a "Character Generator" section to `docs/hotkeys.md` listing every
key in §4.4 of this plan. Match the existing tone (terse, two-column).

### 7.2 Focus traps and accessibility

- Every modal opened from the generator (part picker, palette picker,
  randomize-with-options) uses the same focus-trap pattern as the existing
  designer modals (search `web/src/asset-manager/` for a reference).
- Tab order: Look-tab → animation/facing buttons → slot list → editor →
  summary panel → action bar. Verified via Vitest with jsdom focus
  assertions.

### 7.3 Randomizer

Already present in the reducer; Phase 5 adds a "Randomize…" submenu modal
where the designer can pin specific slots. Pure additive UI.

### 7.4 Integration tests

`server/internal/characters/integration_test.go`:

1. Designer creates parts, registers a stat set, creates an NPC template,
   saves draft, publishes. Asserts:
   - A new `assets` row exists with kind=`sprite`.
   - The PNG bytes at the content-addressed object key are present.
   - `npc_templates.active_bake_id` is non-null.
   - `entity_types` row exists (auto-minted), `sprite_asset_id` matches.
   - `publish_diffs` has rows for each new artifact Kind.
2. Same designer edits one part (changes `layer_order`), publishes again.
   Asserts the bake's `recipe_hash` changes, a new `assets` row is created,
   and `active_bake_id` advances.
3. Bake failure: substitute an `ObjectStore.Put` that errors. Assert the
   `character_bakes` row is `status='failed'`, `npc_templates.active_bake_id`
   stays at the previous value, and the publish *as a whole* fails the tx
   (so live data isn't half-updated).
4. Player catalog: a draft-only part is invisible to the player route; the
   same part after publish is visible.

### 7.5 Code review pass

Final pass against my own checklist:

- No N+1: every list page batches via `ListByIDs` + companion APIs.
- Every player route reads `player_id` from context only.
- No unpublished asset id leaks through `/play/asset-catalog` (verified by
  re-reading every new caller).
- Recipe size cap (e.g., 32 KB after Normalize) enforced server-side.
- `name`, `public_bio`, `private_notes` length caps enforced via
  `configurable.FieldDescriptor.MaxLen`.
- Dead-code scan: no orphaned imports, no `// TODO` stubs left behind.

---

## 8. Wiring summary (single source of truth)

Files I will create or modify, grouped:

**Create:**
- `server/migrations/0034_characters.up.sql` + `.down.sql`.
- `server/internal/characters/{service,definitions,parts,recipes,bake,stats,talents,templates,repo,validate,artifact}.go` + paired `_test.go` files.
- `server/internal/designer/characters_handlers.go`.
- `server/internal/playerweb/character_catalog.go` + test.
- `server/views/characters/{list,generator}.templ` + small partials.
- `web/src/character-generator/{entry-character-generator,state,preview,validation,randomize,hotkeys,styles}.ts/.css` + tests.
- `docs/superpowers/plans/2026-04-26-character-generator-plan.md` (this file).

**Modify:**
- `server/internal/publishing/artifact/artifact.go` — add five `Kind` constants.
- `server/internal/designer/handlers.go` — mount character routes; add `Characters *characters.Service` to `Deps`.
- `server/internal/playerweb/handlers.go` — mount `/play/character-catalog`; add `Characters *characters.Service` to `Deps`.
- `server/internal/hud/binding.go` + `binding_test.go` — accept 4-part bindings.
- `server/internal/hud/layout.go` — re-export `BindingKind` constant if needed.
- `cmd/boxland/main.go` — construct `characters.New(...)`, register five artifact handlers, add post-commit hook for entity_type hot-swap when the linked template's bake changes.
- `web/vite.config.ts` — add `entry-character-generator` to `rollupOptions.input` and `@character-generator` alias.
- `web/src/<binding-picker>` (path TBD; one ripgrep) — extend resource picker for new binding shapes.
- `docs/hotkeys.md` — add Character Generator section.

**Don't touch:**
- `assets/bake.go` (palette-variant bake) — sharing the `ContentAddressedKey` helper only.
- `/play/asset-catalog` — its scope is unchanged.
- Existing `entity_types` schema — auto-minted templates use the existing columns and lifecycle.
- HUD widget definitions — only the binding parser changes; widgets compose unchanged.

---

## 9. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Bake inside publish tx is slow for many NPC templates published at once | Bakes are dedup'd by `recipe_hash`. The first publish per recipe is the only expensive one. If a single publish has >N bakes, run them concurrently with `errgroup` (matches `assets/bake.go` `sync.WaitGroup` pattern). |
| `frame_map_json` is the most under-specified part of the spec | Phase 1 includes a strict `Validate()` for the shape and a fixture-driven test set so the contract is locked before any UI is built on it. |
| Player catalog inventory view ships empty (every player gets every published part) | That's intentional for the first slice and matches spec §Deferred. The view is the seam; replacing the SQL is a single migration in a follow-up. |
| HUD binding parser change risks regressing existing bindings | The change is a single conditional widening (`len <= 4`) plus an additive `Detail` field. All existing tests must continue to pass; new tests cover the new shapes. Any picker UI mirror is ripgrep-able. |
| Auto-minted entity_types create publish-pipeline noise (NPC + entity_type both diff) | Acceptable — the publish-preview modal is designed to show many rows. Each row's diff is small. |
| Designer's source asset is deleted while a recipe references it | The `character_parts.asset_id` FK is `ON DELETE RESTRICT`; the asset can't be deleted while a part references it. Validation in `Bake` also checks for missing assets and fails the bake (not the publish) clearly. |

---

## 10. Out of scope (matches spec §Deferred)

- Full player onboarding flow.
- Equipment-driven runtime swaps.
- Direction-specific layer ordering at runtime.
- Complex masking and hiding rules.
- Advanced formula language (functions beyond clamp/min/max).
- Soft caps and diminishing returns.
- Complex talent effect runtime (the *registry* lands now; the *runtime* later).
- Party/realm character limit UI.
- Separate portrait/bust pipeline.
- Advanced randomized generation tables (the randomizer is uniform-random
  over slot vocabulary; loot-style tables come later).

---

## 11. Acceptance for this plan

I'll consider the plan delivered when, in order:

1. Migration `0034` applies cleanly on a fresh DB and rolls back cleanly.
2. `go test ./server/internal/characters/...` passes.
3. `go test ./server/internal/designer/...` passes.
4. `go test ./server/internal/playerweb/...` passes.
5. `go test ./server/internal/hud/...` passes (extended binding parser).
6. `go test ./server/internal/publishing/...` passes.
7. `cd web && npm test` passes (Vitest).
8. `cd web && npm run build` produces `entry-character-generator.js`.
9. Manual smoke (step-by-step in `_smoke_characters.ps1`):
   - Designer registers two parts (body + hair_front), opens generator on a
     new template, picks both, allocates stats, picks talents, saves draft,
     publishes.
   - The published NPC spawns in `/design/sandbox/launch/...` with the
     baked sprite at integer scale, nearest-neighbor.
   - A second designer edit changes the recipe; re-publish bumps
     `active_bake_id`; old bake stays referenced until cleaned up
     out-of-band (no GC in this slice).
   - A player route call to `/play/character-catalog` returns the part
     entries; a draft part is absent.

Each numbered phase ends with a commit boundary so we can pause cleanly.
