# Character Generator and Asset Pipeline Design

## Goal

Build Boxland's first player-character infrastructure as a complete vertical slice: designers can define character parts and rules, compose a test character or NPC, save it, bake it into a normal runtime sprite asset, and use the same foundation later for player character creation.

The feature uses **character** as the umbrella term, **player character** or **PC** for player-owned characters, and **NPC** or **non-player character** for designer-authored/world-controlled characters. UI copy, schema names, code, and docs must follow this terminology consistently.

## Research basis

The design follows the constraints documented in `C:\Users\cmone\indie-rpg-research.md` and additional RPG design research:

- Pixel RPG tools must preserve crisp 32x32 readability, integer scaling, nearest-neighbor rendering, and explicit animation metadata.
- Sprite-sheet rigidity is a known pain point. Character parts should rely on explicit frame metadata from Aseprite/TexturePacker when possible, not fragile grid inference alone.
- Layered paper-doll systems are the standard structure for 2D RPG customization.
- Layer order often needs to vary by facing direction for hair, capes, shields, backpacks, and weapons. The first slice may keep runtime ordering simple, but the data model should not block direction-specific ordering later.
- Strong character creators separate visual identity, mechanical identity, and narrative identity.
- RPG stat systems should be designer-defined, visible, and debuggable. Core stats should be few enough to understand and should feed multiple derived values.
- Talent systems should support more than strict trees. Designers will want trees, tiered lists, webs, free-pick feat lists, and class/archetype tracks.
- Character creation should support both guided player flow and advanced designer/testing flow.

## Chosen approach

Use a **definition -> recipe -> bake -> instance/template** model.

- **Definitions** are designer-authored rules and catalogs: slots, parts, stat definitions, creation rules, talent trees, and available templates.
- **Recipes** are editable layered selections: selected body base, hair, outfit, colors, stat allocations, and talent picks.
- **Bakes** are generated runtime sprite assets created from recipes. A bake produces a normal Boxland `sprite` asset, stored in content-addressed object storage and usable by the existing renderer and asset catalog.
- **Instances/templates** are usage contexts: player characters and NPC templates.

This approach prioritizes the user's selected requirement: **save should bake a composed runtime sprite immediately**. It keeps the live MMORPG renderer fast and compatible with existing sprite infrastructure while preserving enough recipe metadata for future editing.

## Scope of the first vertical slice

The first implementation should prove the whole pipeline narrowly:

1. A designer defines or uses default character slots.
2. A designer registers existing 32x32 sprite assets as character parts.
3. A designer opens the Character Generator in designer mode.
4. The generator composes a test character or NPC from parts.
5. The generator validates required slots and part compatibility.
6. The generator saves the editable recipe.
7. Boxland bakes a composed sprite sheet on save.
8. Boxland creates or updates an NPC template referencing the baked sprite asset.
9. The baked sprite can be previewed and later spawned through existing sprite/entity infrastructure.
10. The same service and validation boundaries support player character rows, even if full player onboarding is finished later.

Included in the first slice:

- Designer-facing Character Generator.
- Player-ready schema/service boundaries.
- Visual slots and parts.
- Save-and-bake flow.
- NPC template save.
- Simple configurable stats.
- Simple graph-based talent definitions.
- Reusable UI foundation for player mode.

Deferred:

- Full player onboarding flow.
- Equipment-driven runtime swaps.
- Direction-specific layer ordering in live runtime.
- Complex masking and hiding rules beyond simple slot replacement.
- Advanced formula language.
- Soft caps and diminishing returns.
- Complex talent effect runtime.
- Party/realm character limit UI.
- Separate portrait/bust art pipeline.
- Advanced randomized generation tables.

## Server architecture

Add a new server domain:

```text
server/internal/characters/
  definitions.go
  parts.go
  recipes.go
  bake.go
  stats.go
  talents.go
  repo.go
  validate.go
```

Responsibilities:

- `definitions.go`: shared types for slots, creation rules, ownership, publish status, and validation constants.
- `parts.go`: character part metadata, slot compatibility, asset references, frame coverage, and palette hooks.
- `recipes.go`: editable appearance/stat/talent selections and stable recipe hashing.
- `bake.go`: image composition, content-addressed output, bake deduplication, and asset creation/reuse.
- `stats.go`: stat definitions, point-buy validation, and small formula evaluation.
- `talents.go`: talent tree/node definitions and prerequisite validation.
- `repo.go`: owner-scoped persistence helpers.
- `validate.go`: cross-entity validation for recipes, bakes, player characters, and NPC templates.

The domain should follow existing Boxland patterns:

- Go structs with explicit `Validate()` methods.
- `configurable.FieldDescriptor` for designer-editable structured fields where useful.
- Generic `Repo[T]` for design-tool CRUD where hot-path performance is not needed.
- Explicit owner-scoped queries for player characters and designer-authored drafts.
- Batched asset loading via existing `assets.Service.ListByIDs` and animation batching APIs to avoid N+1 patterns.

## Data model

Recommended initial tables:

```text
character_slots
character_parts
character_recipes
character_bakes
character_stat_sets
character_talent_trees
character_talent_nodes
player_characters
npc_templates
```

### `character_slots`

Designer-authored slot vocabulary.

Important fields:

- `id`
- `key`, e.g. `body`, `hair_front`, `shirt`, `main_hand`
- `label`
- `required`
- `order_index`
- `default_layer_order`
- `allows_palette`
- `created_by`
- timestamps

The default slot vocabulary should include body base, skin palette, face, eyes, eyebrows, mouth, hair back, hair front, facial hair, ears/horns, headwear, neck, torso underlayer, torso outerwear, arms/gloves, legs, boots, cloak/cape, backpack, accessory slots, main hand, off hand, and aura/effect.

### `character_parts`

Designer-authored references from character slots to existing sprite assets.

Important fields:

- `id`
- `slot_id`
- `asset_id`
- `name`
- `tags`
- `compatible_tags`
- `layer_order`
- `frame_map_json`
- `palette_regions_json`
- `published`
- `created_by`
- timestamps

A part must reference a `sprite` asset. The first slice validates that chosen parts have compatible frame dimensions and enough frame coverage to compose the selected animation set.

### `character_recipes`

Editable layered selections.

Important fields:

- `id`
- `owner_kind`, e.g. `designer`, `player`
- `owner_id`
- `name`
- `appearance_json`
- `stats_json`
- `talents_json`
- `recipe_hash`
- `created_by`
- timestamps

`appearance_json` stores selected part ids, slot keys, palette choices, and ordering. The stable `recipe_hash` is computed from normalized recipe content and is used to deduplicate bakes.

### `character_bakes`

Generated composed sprite outputs.

Important fields:

- `id`
- `recipe_id`
- `recipe_hash`
- `asset_id`
- `status`, one of `pending`, `baked`, `failed`
- `failure_reason`
- `baked_at`
- timestamps

The `(recipe_hash, asset_id)` relationship should allow reuse of identical bakes. Failed bakes keep enough error detail for designer diagnostics without leaking private asset data to players.

### `character_stat_sets`

Designer-defined stat models.

Important fields:

- `id`
- `key`
- `name`
- `stats_json`
- `creation_rules_json`
- `published`
- `created_by`
- timestamps

### `character_talent_trees` and `character_talent_nodes`

Designer-defined talent graphs.

Tree fields:

- `id`
- `key`
- `name`
- `description`
- `currency_key`
- `layout_mode`, e.g. `tree`, `tiered`, `free_list`, `web`
- `published`
- `created_by`
- timestamps

Node fields:

- `id`
- `tree_id`
- `key`
- `name`
- `description`
- `icon_asset_id`
- `max_rank`
- `cost_json`
- `prerequisites_json`
- `effect_json`
- `layout_json`
- timestamps

Prerequisites are graph edges and rules, not code. Effects are structured data for automations/game logic to inspect.

### `player_characters`

Player-owned saved characters.

Important fields:

- `id`
- `player_id`
- `recipe_id`
- `active_bake_id`
- `name`
- `public_bio`
- `private_notes`
- `created_at`
- `updated_at`

Every player route must scope by `player_id` from authenticated context, never by trusting request-body ownership.

### `npc_templates`

Designer-authored reusable NPC definitions.

Important fields:

- `id`
- `name`
- `recipe_id`
- `active_bake_id`
- `entity_type_id`
- `tags`
- `published`
- `created_by`
- timestamps

The table is called `npc_templates` to keep NPC terminology concise.

## Asset and bake pipeline

Character parts use the existing asset pipeline. They reference uploaded sprite assets and their animation metadata rather than inventing a separate blob store.

Preferred import path:

1. Designer uploads PNG and optional Aseprite/TexturePacker JSON through the existing Asset Manager.
2. Existing importers persist frame rectangles and animation tags.
3. Designer registers the uploaded sprite as one or more character parts.
4. Character part metadata records slot, layer order, compatibility tags, palette regions, and frame mapping.

Save-and-bake flow:

1. Receive recipe save from designer or player route.
2. Resolve the authenticated owner.
3. Normalize the recipe to canonical JSON.
4. Validate required slots, part ownership/visibility, frame compatibility, stat allocations, and talent selections.
5. Compute `recipe_hash`.
6. Check for an existing successful bake for the same hash.
7. If found, link the recipe to that bake.
8. If not found, load all source asset images in a batch.
9. Composite every output frame in layer order using nearest-neighbor-safe pixel operations.
10. Encode the composed sprite sheet as PNG.
11. Store it under a content-addressed object-store path.
12. Create or reuse an `assets` row with `kind = 'sprite'` and generated metadata.
13. Persist or update `character_bakes` with `status = 'baked'`.
14. Link the player character or NPC template to the active bake.

Failure behavior:

- Validation failures return actionable form errors before baking.
- Image load or encode failures mark the bake as `failed` and keep the previous active bake if one exists.
- Player-facing save failures show a simple retryable message. Designer mode shows detailed diagnostics.
- Missing or deleted source assets do not crash rendering; they produce validation errors and safe placeholders in previews.

## Character Generator UI

The Character Generator is one reusable surface with mode-specific controls:

```text
/design/characters/generator        designer mode
/play/characters/new                player mode later
/play/characters/{id}/edit          player edit mode later
```

### Designer mode

Designer mode is tested first and includes:

- 32x32 sprite preview.
- Zoomed integer-scale preview.
- Animation selector.
- Facing selector.
- Slot list.
- Part picker.
- Palette picker where available.
- Recipe validation panel.
- Stat preview.
- Talent preview.
- Bake status.
- Save as NPC template.
- Spawn in sandbox later.
- Randomize.
- Reset.
- Copy recipe JSON for debugging.

### Player mode

Player mode uses the same generator core but hides:

- raw ids;
- unpublished parts;
- debug validation internals;
- formula authoring;
- bake diagnostics unless save fails.

Player mode keeps:

- guided tabs;
- visual preview;
- stat allocation;
- talent selection;
- summary sheet;
- save/finalize.

### Layout and visual direction

Use a 90s RPG / paper character-sheet layout:

```text
+------------------------------------------------------------+
| CREATE A CHARACTER                                         |
| Look | Sheet | Talents | Story | Finish                    |
+-------------------+-----------------------+----------------+
| Animated Preview  | Active Editor Panel   | Sheet Summary  |
|                   |                       |                |
| Direction rose    | Slot/stat/talent form | Stats          |
| Animation buttons |                       | Resources      |
| Bake badge        |                       | Warnings       |
+-------------------+-----------------------+----------------+
| Randomize | Reset | Save Draft | Save & Bake               |
+------------------------------------------------------------+
```

Visual system:

- Dark, crisp pixel UI.
- Parchment/ledger cards.
- Jewel-tone slot tabs.
- 9-patch RPG panels for player-facing chrome.
- No blurred shadows.
- Integer scaling only.
- Visible focus states.
- Hotkeys suppressed while typing, matching `docs/hotkeys.md`.

Copy should say **Create a Character**, **Character Generator**, and **Character Sheet**.

## Stats model

The first slice includes a small configurable stat system, not a full rules engine.

A stat definition includes:

- key;
- label;
- kind: `core`, `derived`, `resource`, or `hidden`;
- default value;
- min/max;
- creation cost;
- display order;
- formula string for derived stats;
- optional cap.

Initial formulas support:

- numeric literals;
- stat references;
- `+`, `-`, `*`, `/`;
- parentheses;
- simple clamp helpers if practical.

Example formulas:

```text
HP = 10 + Grit * 2
Focus = 5 + Wit + Spirit
Carry = Might * 3
```

Creation methods in the first slice:

- fixed preset;
- freeform designer override;
- simple point-buy.

Later creation methods may include standard array, dice roll, class/background boosts, soft caps, and progression curves.

## Talents model

Talent definitions are graph-based, even if the first UI renders a simple tree.

A talent tree includes:

- key;
- name;
- description;
- currency key, e.g. `talent_points`;
- layout mode: `tree`, `tiered`, `free_list`, or `web`;
- nodes;
- prerequisite edges.

A talent node includes:

- key;
- name;
- short description;
- icon asset id;
- max rank;
- cost;
- required stat threshold;
- required character tags;
- mutually exclusive group;
- effect JSON.

Initial effects are structured JSON only:

- add flat stat modifier;
- add resource max;
- add character tag;
- set automation flag;
- unlock action key.

Designer test mode can grant and reset talent currency freely. Player mode can only spend available currency under published rules.

## Runtime integration

Because the first slice bakes a composed sprite on save, runtime integration can be narrow.

For an NPC:

1. Save recipe.
2. Bake sprite asset.
3. Create or update an `npc_templates` row.
4. Link the template to an existing or generated `entity_type`.
5. Set the entity type's `sprite_asset_id` to the baked asset id.
6. Set the default animation from the baked asset when available.
7. Add tags such as `character` and `npc`.

For a player character later:

1. Save `player_characters` row owned by `player_id`.
2. Link to the active bake.
3. On entering a map, spawn or load that character's entity state.
4. Use existing HUD bindings where possible:
   - `entity:host:hp_pct`
   - `entity:host:nameplate`
   - `entity:host:variant_id`
   - `entity:host:resource:<name>`

Add a lightweight ECS/config component so automations and HUDs can identify character-backed entities without forcing the whole character system into hot-path ECS immediately:

```json
{
  "character_id": 123,
  "character_kind": "npc_template",
  "stat_set_id": 4,
  "talent_tree_ids": [1, 2],
  "resource_bindings": ["hp", "focus", "talent_points"]
}
```

Allowed `character_kind` values are `npc_template` and `player_character`.

## HUD integration

The first slice should reuse existing HUD concepts rather than adding a separate UI framework:

- `portrait` can show the baked sprite asset or a selected frame.
- `resource_bar` can show HP/focus/stamina-like resources.
- `icon_counter` can show currencies such as talent points.
- `text_label` can show nameplate, class/archetype, or sheet summary values.
- Future bindings can expose `character:<id>:stat:<key>` if entity-scoped bindings are not enough.

Designer mode should include a small HUD-preview handoff: after baking an NPC, the designer can see which bindings the character exposes.

## Security and isolation

Designer side:

- All definition/template mutations require designer auth.
- Designer-owned rows include `created_by`.
- Unpublished character parts are only visible in designer routes.
- Designer previews use protected asset routes for source assets.
- Draft/private generated bakes are not exposed to players until linked to published content.
- Asset and animation lookups are batched.

Player side:

- Player character rows include `player_id`.
- All player routes use the authenticated player from context.
- Player routes never trust request-body `player_id`.
- Players can only select published character options.
- Players cannot request unpublished recipe or part metadata.
- Character names, bios, notes, selected part count, and recipe JSON size are capped.
- Invalid baked assets fall back to placeholders and log server-side.

Asset catalog:

- `/play/asset-catalog` currently allows any authenticated player to request asset ids. Character work must not rely on secrecy of unpublished asset ids.
- Player-visible character options should either reference published assets only or be served by a scoped character catalog endpoint.
- If a generated bake is private or draft-only, it must not be exposed through player routes.

## Performance considerations

- Runtime map play should render the composed baked sprite, not many separate paper-doll layers.
- Editor preview can render layers for instant feedback.
- Bake input assets should be loaded in batches.
- Animations should be fetched via `ListAnimationsByAssetIDs`, not one query per part.
- Recipe hashes and content-addressed object keys make repeated bakes idempotent.
- Generated sprite assets should reuse existing content paths when possible.
- Any future player list screen must batch active bake and asset lookups.

## Accessibility, focus, and hotkeys

The generator follows existing Boxland hotkey conventions:

- Visible focus at all times.
- DOM tab order matches visual workflow.
- Modals trap and restore focus.
- Hotkeys are suppressed while typing unless explicitly marked safe.
- Designer mode may add shortcuts for animation/facing/slot traversal, but they must be documented with existing hotkey docs.
- Player mode should not require keyboard shortcuts to complete creation.

## Testing strategy

Use TDD when implementing.

Go tests:

- Character slot validation.
- Part compatibility validation.
- Recipe validation.
- Recipe hash stability.
- Bake deduplication.
- Owner-scoped repo behavior.
- Player cannot mutate another player's character.
- Designer unpublished options do not appear in player catalogs.
- Formula evaluation.
- Point-buy validation.
- Talent prerequisite validation.
- Talent mutually exclusive group validation.
- Save designer test character creates recipe, bake, sprite asset, and NPC template.

TypeScript tests:

- Generator state reducer.
- Slot/part selection behavior.
- Preview layer ordering.
- Keyboard/focus behavior.
- Stat point-buy validation.
- Talent graph selection validation.
- Designer-mode controls hidden in player mode.

Integration tests:

- Save and bake a designer-created NPC.
- Baked asset resolves through the appropriate catalog when published/allowed.
- Failed bake preserves previous active bake.
- NPC template references the expected baked asset.

## Non-goals

- Arbitrary sprite sizes beyond the existing 32x32-compatible sprite pipeline.
- Runtime rendering of every paper-doll layer in map play.
- Full equipment system.
- Full character progression system.
- General scripting or arbitrary code execution in formulas/talent effects.
- Removing or replacing existing asset, entity, or HUD infrastructure.
