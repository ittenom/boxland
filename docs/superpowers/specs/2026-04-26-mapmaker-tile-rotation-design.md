# Mapmaker Tile Rotation Design

## Goal

Enable Mapmaker designers to rotate individual tile placements and have that rotation respected everywhere the tile is used: Mapmaker rendering, persistence, sandbox/runtime rendering, collision behavior, collider geometry, and edge socket interpretation.

## Chosen approach

Use an explicit per-placement `rotation_degrees` column on `map_tiles`. The value is constrained to quarter turns: `0`, `90`, `180`, and `270`. Existing rows default to `0`, so current maps keep their behavior.

This is preferred over storing rotation in `custom_flags_json` because rotation is core tile behavior, needs validation, and is read on hot downstream paths. A separate transform table is unnecessary and would add joins to chunk loading.

## Authoring UX

Mapmaker gets one compact rotate control in the existing Tools panel. The button shows the current placement rotation, for example `⟳ 90°`, and has title text `Rotate tile (T)`. Clicking the button or pressing `T` cycles `0 → 90 → 180 → 270 → 0`.

Brush, rectangle, and fill stamp the active rotation onto placed cells. Eyedrop selects both the tile entity type and the clicked cell's rotation. The status bar shows the active rotation so designers can see what will be painted before clicking.

The Mapmaker canvas draws rotated tiles in place by rotating around the center of the 32×32 cell. Unloaded/placeholder cells are unchanged.

## Persistence and API

Add migration `rotation_degrees SMALLINT NOT NULL DEFAULT 0` to `map_tiles` with a check constraint for the four allowed values.

Extend:

- `maps.Tile` with `RotationDegrees int16`.
- `PlaceTiles` insert/update to persist rotation.
- `ChunkTiles` to read rotation.
- Mapmaker JSON wire format to accept and return `rotation_degrees`.

Server handlers normalize invalid or missing client values to safe behavior: missing means `0`; invalid values are rejected with `400 Bad Request` rather than silently writing corrupt data.

## Downstream transform semantics

A placed tile's rotation is a tile transform. Everything tied to the tile's orientation rotates with it.

### Visual rendering

Mapmaker canvas uses the per-cell rotation. Runtime/wire tile payloads gain a rotation field so downstream visual clients can render the same orientation from canonical persisted data.

### Collision shape and edge bits

Collision shape semantics rotate when a tile is materialized. Examples:

- `WallNorth` at `90°` behaves as `WallEast`.
- `DiagNE` at `90°` behaves as `DiagSE`.
- `HalfNorth` at `180°` behaves as `HalfSouth`.
- `OneWayN` at `270°` behaves as west-facing one-way behavior.

Edge-bit masks rotate by quarter turns as well. For example, an `EdgeN | EdgeE` mask rotated `90°` becomes `EdgeE | EdgeS`.

### Collider geometry

Collider width, height, and anchor transform with the tile rotation. For 90° and 270° turns, width and height swap. Anchors are remapped around the 32×32 tile cell so non-square or offset colliders stay aligned to rotated art. Square centered colliders remain unchanged.

### Edge sockets

Tile edge socket assignments are interpreted through rotation. A tile whose original north socket is painted with `90°` presents that socket on the east side. This keeps adjacency semantics correct for any downstream system that evaluates rotated tile edges.

Procedural generation and materialization can continue to write `0°` for now, but shared rotation helpers should be added so rotated procedural variants can be introduced without redefining socket logic.

### Overrides and flags

Per-tile `collision_shape_override`, `collision_mask_override`, `anim_override`, and `custom_flags_json` continue to persist. Rotation is independent of those fields. If a collision shape override exists, that overridden shape rotates just like the entity type's default shape would.

## Testing strategy

Use TDD.

Add Go tests for:

- `rotation_degrees` round-tripping through `PlaceTiles` and `ChunkTiles`.
- handler JSON accepting valid rotation and rejecting invalid rotation.
- runtime chunk loading carrying rotation to sprite/tile wire state.
- collision shape rotation for cardinal, diagonal, half-tile, and one-way shapes.
- edge-bit rotation.
- collider `W/H/AnchorX/AnchorY` rotation.
- socket edge rotation.

Add Mapmaker JavaScript tests if a JS test harness exists. If not, keep rotation helpers as named pure functions where possible and verify through build/smoke coverage plus manual browser checks.

## Non-goals

- Arbitrary-angle rotation.
- Flip/mirror transforms.
- Procedural generation choosing rotated variants in this pass.
- Reworking the palette or adding four rotation buttons.
