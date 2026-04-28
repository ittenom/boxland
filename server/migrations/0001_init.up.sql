-- 0001_init.up.sql
--
-- Boxland schema, single canonical migration.
--
-- This file replaces the earlier 0001..0043 chain. The redesign that
-- spawned it introduced first-class TILEMAPs, a unified ENTITY hierarchy
-- (TILE | NPC | PC | LOGIC), and a clean MAP / LEVEL / WORLD split. We
-- chose to squash rather than chain because (a) the redesign breaks
-- back-compat anyway, (b) golang-migrate's version table just records
-- "we're at this revision" with no semantic dependency on the count, and
-- (c) future devs reading "what does the schema look like?" benefit from
-- one well-organized file over forty-three append-only deltas.
--
-- Section map:
--   1.  Extensions and helpers
--   2.  Boxland metadata
--   3.  Designer realm  (designers, sessions, ws tickets)
--   4.  Player realm    (players, oauth, sessions, email verification)
--   5.  Per-user settings
--   6.  Publishing pipeline (drafts, publish_diffs)
--   7.  Asset library   (assets, animations, folders, palettes, variants)
--   8.  Tilemaps        (tilemaps, tilemap_tiles)
--   9.  Entities        (entity_types + components + automations + edge sockets + tile groups)
--   10. Characters      (slots, parts, recipes, bakes, stat sets, talent trees)
--   11. Maps            (maps, layers, tiles, lighting, locked cells, sample patches, constraints)
--   12. Worlds & Levels (worlds, levels, level_entities, level_action_groups, level_flags, level_spectator_invites)
--   13. Runtime state   (level_state)
--
-- Conventions:
--   * Single tenant per deployment. Tenant isolation is structural: every
--     designer-authored row carries created_by; cross-tenant reads aren't
--     representable because there is no tenant column.
--   * BIGSERIAL primary keys throughout. We never expose ids to URLs that
--     need to be unguessable; designer auth gates the whole /design/ tree.
--   * JSONB for shape-flexible fields (component config, automation AST,
--     HUD layout, recipe appearance, constraint params). Validation lives
--     in Go, not in CHECK constraints, so adding a new component kind or
--     constraint type is code-only.
--   * 0xRRGGBBAA color values are stored as BIGINT. The dominant_color
--     column on assets stores 0xRRGGBB (no alpha — alpha is meaningless
--     for a sort key or swatch).

-- =========================================================================
-- 1. Extensions and helpers
-- =========================================================================

-- citext powers case-insensitive email columns on designers + players.
CREATE EXTENSION IF NOT EXISTS citext;

-- =========================================================================
-- 2. Boxland metadata
-- =========================================================================

-- Tiny key/value bag for singleton operator-facing breadcrumbs (last
-- booted version, install id, etc.). The database is the one artifact
-- whose schema must move in lockstep with the code, so recording "this
-- DB was last touched at code version X" beside the schema itself is the
-- right pairing — backups carry it; dev-laptop JSON files would not.
CREATE TABLE boxland_meta (
    key        TEXT         PRIMARY KEY,
    value      TEXT         NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- =========================================================================
-- 3. Designer realm
-- =========================================================================

-- Designers are the people who author content. Strictly separated from
-- players; designer sessions can never authenticate a WS connection as a
-- player (and vice versa). Role gates write access in the handler layer.
CREATE TABLE designers (
    id            BIGSERIAL    PRIMARY KEY,
    email         CITEXT       UNIQUE NOT NULL,
    password_hash TEXT         NOT NULL,
    role          TEXT         NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- 30-day rolling cookie sessions for the design tools. The raw cookie
-- value is never stored; we keep its sha256 and verify by re-hashing.
CREATE TABLE designer_sessions (
    token_hash   BYTEA        PRIMARY KEY,
    designer_id  BIGINT       NOT NULL REFERENCES designers(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ  NOT NULL,
    user_agent   TEXT,
    ip           INET
);
CREATE INDEX designer_sessions_designer_idx ON designer_sessions (designer_id);
CREATE INDEX designer_sessions_expires_idx  ON designer_sessions (expires_at);

-- One-shot, ~30s tickets that bridge a designer's cookie session onto a
-- realm-tagged WebSocket. POST /design/ws-ticket mints; the WS gateway
-- redeems on the first FlatBuffers Auth message. Player tokens (JWTs)
-- don't touch this table.
CREATE TABLE designer_ws_tickets (
    ticket_hash  BYTEA        PRIMARY KEY,
    designer_id  BIGINT       NOT NULL REFERENCES designers(id) ON DELETE CASCADE,
    ip           INET         NOT NULL,
    expires_at   TIMESTAMPTZ  NOT NULL,
    consumed_at  TIMESTAMPTZ
);
CREATE INDEX designer_ws_tickets_expires_idx ON designer_ws_tickets (expires_at);

-- =========================================================================
-- 4. Player realm
-- =========================================================================

-- Players are end users of a published WORLD. password_hash NULL is
-- allowed for OAuth-only accounts; a deferred trigger below requires
-- either a password OR at least one OAuth link per row.
CREATE TABLE players (
    id              BIGSERIAL    PRIMARY KEY,
    email           CITEXT       UNIQUE NOT NULL,
    password_hash   TEXT,
    email_verified  BOOLEAN      NOT NULL DEFAULT false,
    display_name    TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE player_oauth_links (
    player_id          BIGINT       NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    provider           TEXT         NOT NULL CHECK (provider IN ('google', 'apple', 'discord')),
    provider_user_id   TEXT         NOT NULL,
    linked_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_user_id)
);
CREATE INDEX player_oauth_links_player_idx ON player_oauth_links (player_id);

-- Cross-table credential check (CHECK constraints can't span tables in
-- Postgres). Deferred to commit so a single transaction can INSERT a
-- player + INSERT an oauth link and have both visible to the predicate.
CREATE OR REPLACE FUNCTION players_require_credential()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.password_hash IS NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM player_oauth_links WHERE player_id = NEW.id
        ) THEN
            RAISE EXCEPTION 'players: row id=% has neither password nor OAuth link', NEW.id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER players_credential_check
    AFTER INSERT OR UPDATE OF password_hash ON players
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION players_require_credential();

-- Refresh tokens for the player JWT auth flow.
CREATE TABLE player_sessions (
    refresh_token_hash  BYTEA        PRIMARY KEY,
    player_id           BIGINT       NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ  NOT NULL,
    user_agent          TEXT,
    ip                  INET
);
CREATE INDEX player_sessions_player_idx  ON player_sessions (player_id);
CREATE INDEX player_sessions_expires_idx ON player_sessions (expires_at);

-- Email verification tokens (consumed by /auth/player/verify).
CREATE TABLE player_email_verifications (
    token_hash   BYTEA        PRIMARY KEY,
    player_id    BIGINT       NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    expires_at   TIMESTAMPTZ  NOT NULL,
    consumed_at  TIMESTAMPTZ
);
CREATE INDEX player_email_verifications_player_idx ON player_email_verifications (player_id);

-- =========================================================================
-- 5. Per-user settings
-- =========================================================================

-- Realm-tagged so the same physical user can have distinct designer +
-- player settings (different fonts, different audio levels). subject_id
-- resolves to either designers.id or players.id depending on realm.
CREATE TABLE user_settings (
    realm        TEXT         NOT NULL CHECK (realm IN ('designer', 'player')),
    subject_id   BIGINT       NOT NULL,
    payload_json JSONB        NOT NULL DEFAULT '{}'::jsonb,
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (realm, subject_id)
);

-- =========================================================================
-- 6. Publishing pipeline
-- =========================================================================

-- The drafts table is the single home for in-progress edits across every
-- designer-managed artifact (assets, entity types, maps, tilemaps,
-- levels, worlds, palettes, edge socket types, tile groups, character
-- definitions). The publish pipeline (internal/publishing/artifact)
-- walks rows here, validates each draft against its registered handler,
-- and applies them inside a single transaction. Last-write-wins per
-- (kind, id) — multiple concurrent drafts for the same artifact are
-- not a v1 feature.
CREATE TABLE drafts (
    artifact_kind TEXT         NOT NULL,
    artifact_id   BIGINT       NOT NULL,
    draft_json    JSONB        NOT NULL,
    created_by    BIGINT       NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (artifact_kind, artifact_id)
);
CREATE INDEX drafts_created_by_idx ON drafts (created_by);

-- Audit log + diff preview source. One row per (changeset, artifact);
-- changeset_id values come from publish_changeset_seq and are allocated
-- by the publish pipeline at commit time.
CREATE SEQUENCE publish_changeset_seq AS BIGINT MINVALUE 1 START 1;

CREATE TABLE publish_diffs (
    id                   BIGSERIAL    PRIMARY KEY,
    changeset_id         BIGINT       NOT NULL,
    artifact_kind        TEXT         NOT NULL,
    artifact_id          BIGINT       NOT NULL,
    op                   TEXT         NOT NULL CHECK (op IN ('created', 'updated', 'deleted')),
    summary_line         TEXT         NOT NULL,
    structured_diff_json JSONB        NOT NULL,
    published_by         BIGINT       NOT NULL,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX publish_diffs_changeset_idx    ON publish_diffs (changeset_id);
CREATE INDEX publish_diffs_artifact_idx     ON publish_diffs (artifact_kind, artifact_id);
CREATE INDEX publish_diffs_published_by_idx ON publish_diffs (published_by);

-- =========================================================================
-- 7. Asset library
-- =========================================================================

-- The asset library stores raw uploaded files plus a small amount of
-- structured metadata. Bytes are content-addressed in object storage
-- under "assets/aa/bb/<sha256>"; the same bytes uploaded twice (under
-- different names or kinds) reuse the same key. The (kind, name) pair
-- is the human-facing identifier; the path is the storage identifier.
--
-- Kind values:
--   sprite           single 32x32 image (the simplest case)
--   sprite_animated  multi-frame strip; rows of 32x32 cells form an
--                    animation. Tilemaps reference assets of this kind
--                    as their backing PNG (a tilemap is "an animated
--                    sprite plus structured adjacency").
--   audio            wav/ogg/mp3
--   ui_panel         9-slice border PNG for chrome
--
-- folder_id NULL = "lives at the kind root"; the four virtual roots in
-- the asset folder tree (sprite, sprite_animated, audio, ui_panel) have
-- no rows of their own.
--
-- dominant_color is packed 0xRRGGBB (no alpha). Computed lazily from the
-- backing PNG when sort=color is first requested in a folder.
CREATE TABLE assets (
    id                       BIGSERIAL    PRIMARY KEY,
    kind                     TEXT         NOT NULL CHECK (kind IN ('sprite', 'sprite_animated', 'audio', 'ui_panel')),
    name                     TEXT         NOT NULL,
    content_addressed_path   TEXT         NOT NULL,
    original_format          TEXT         NOT NULL,
    metadata_json            JSONB        NOT NULL DEFAULT '{}'::jsonb,
    tags                     TEXT[]       NOT NULL DEFAULT '{}',
    folder_id                BIGINT,
    dominant_color           BIGINT,
    created_by               BIGINT       NOT NULL REFERENCES designers(id),
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- (kind, name) is unique so designers can rely on "the boss sprite"
-- always referring to the same row. Two assets with different names
-- pointing at the same content path are allowed.
CREATE UNIQUE INDEX assets_kind_name_idx ON assets (kind, name);
CREATE INDEX assets_tags_idx       ON assets USING GIN (tags);
CREATE INDEX assets_kind_idx       ON assets (kind);
CREATE INDEX assets_created_by_idx ON assets (created_by);
CREATE INDEX assets_folder_id_idx  ON assets (folder_id);

-- Animation tags extracted from sprite_animated sheets at import time.
-- Each row = one named animation in the source sheet (e.g. "walk_north"
-- frames 0..3). FPS, loop direction, and frame range come from the
-- importer (Aseprite frameTags, manual config, or auto-walk seeding).
CREATE TABLE asset_animations (
    id          BIGSERIAL    PRIMARY KEY,
    asset_id    BIGINT       NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    name        TEXT         NOT NULL,
    frame_from  INTEGER      NOT NULL CHECK (frame_from >= 0),
    frame_to    INTEGER      NOT NULL CHECK (frame_to >= frame_from),
    direction   TEXT         NOT NULL DEFAULT 'forward'
                              CHECK (direction IN ('forward', 'reverse', 'pingpong')),
    fps         INTEGER      NOT NULL DEFAULT 8 CHECK (fps > 0 AND fps <= 60),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (asset_id, name)
);
CREATE INDEX asset_animations_asset_idx ON asset_animations (asset_id);

-- Asset folders: an IDE-style filesystem laid over the flat asset table.
-- Six virtual top-level roots (one per kind plus level + world for the
-- non-asset object types that also live in a folder tree) — these roots
-- are NOT real rows. A folder's kind_root says which root it belongs to;
-- assets/levels/worlds with folder_id NULL live in the kind root.
--
-- Folder delete cascades to child folders but DOES NOT delete the
-- contents — they bubble up to the kind root via ON DELETE SET NULL on
-- the back-references.
CREATE TABLE asset_folders (
    id           BIGSERIAL    PRIMARY KEY,
    parent_id    BIGINT       REFERENCES asset_folders(id) ON DELETE CASCADE,
    -- The folder tree spans more than just assets — tilemaps, levels, and
    -- worlds also live in folder hierarchies. The "asset_folders" table
    -- name is a slight misnomer kept for migration economy; kind_root
    -- discriminates which back-reference column points at this folder
    -- (assets.folder_id, tilemaps.folder_id, levels.folder_id,
    --  worlds.folder_id).
    --
    -- Note: animated-sprite assets that are NOT tilemaps (notably
    -- character bakes) live under folder_id = NULL — they're subordinate
    -- to whichever NPC/PC owns them and don't get their own library
    -- folder root. So `sprite_animated` is not a valid kind_root.
    kind_root    TEXT         NOT NULL CHECK (kind_root IN (
                                  'sprite', 'tilemap', 'audio', 'ui_panel',
                                  'level', 'world'
                              )),
    name         TEXT         NOT NULL,
    sort_mode    TEXT         NOT NULL DEFAULT 'alpha'
                              CHECK (sort_mode IN ('alpha', 'date', 'type', 'color', 'length')),
    created_by   BIGINT       NOT NULL REFERENCES designers(id),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- Case-insensitive uniqueness within a parent + kind_root. The COALESCE
-- lets multiple roots have a "Forest" folder without collision when
-- parent_id is NULL.
CREATE UNIQUE INDEX asset_folders_parent_name_idx
    ON asset_folders (COALESCE(parent_id, 0), kind_root, lower(name));
CREATE INDEX asset_folders_kind_root_idx ON asset_folders (kind_root, parent_id);

-- Wire the back-reference now that asset_folders exists.
ALTER TABLE assets
    ADD CONSTRAINT assets_folder_id_fkey
    FOREIGN KEY (folder_id) REFERENCES asset_folders(id) ON DELETE SET NULL;

-- Project-wide named palette presets. The default retro palette mirrors
-- the tokens baked into pixel.css so chrome and swatch picker stay in
-- sync. colors is an ordered array of 0xRRGGBBAA values; order is
-- meaningful for ergonomic 1..9 swatch selection.
CREATE TABLE palettes (
    id          BIGSERIAL    PRIMARY KEY,
    name        TEXT         NOT NULL UNIQUE,
    colors      BIGINT[]     NOT NULL DEFAULT '{}',
    created_by  BIGINT       REFERENCES designers(id),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
INSERT INTO palettes (name, colors) VALUES (
    'Default retro',
    ARRAY[
        x'1a1733ff'::bigint,  -- bg-1
        x'241f4aff'::bigint,  -- bg-2
        x'2e2766ff'::bigint,  -- bg-3
        x'3a318aff'::bigint,  -- bg-4
        x'6a5fc0ff'::bigint,  -- line
        x'a99cffff'::bigint,  -- line-hi
        x'f4ecffff'::bigint,  -- fg-1
        x'c4b9eeff'::bigint,  -- fg-2
        x'8a7fbfff'::bigint,  -- fg-3
        x'ffd34aff'::bigint,  -- accent
        x'ffe79aff'::bigint,  -- accent-hi
        x'4ad7ffff'::bigint,  -- info
        x'5fe87bff'::bigint,  -- success
        x'ff9e3dff'::bigint,  -- warn
        x'ff5e7eff'::bigint   -- error
    ]
);

-- Recipe for re-coloring an asset. source_to_dest_json is a JSON map of
-- "0xRRGGBBAA" -> "0xRRGGBBAA" (numeric strings to keep JSON safe).
-- Optionally bound to a palette so the picker UI can suggest matching
-- destination colors.
CREATE TABLE palette_variants (
    id                  BIGSERIAL    PRIMARY KEY,
    asset_id            BIGINT       NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    name                TEXT         NOT NULL,
    palette_id          BIGINT       REFERENCES palettes(id),
    source_to_dest_json JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_by          BIGINT       REFERENCES designers(id),
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (asset_id, name)
);
CREATE INDEX palette_variants_asset_idx ON palette_variants (asset_id);

-- Baked output of the palette-variant pipeline. One row per re-colored
-- PNG. Stored at a content-addressed path so re-running the bake on
-- identical inputs is a no-op.
CREATE TABLE asset_variants (
    id                       BIGSERIAL    PRIMARY KEY,
    asset_id                 BIGINT       NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    palette_variant_id       BIGINT       NOT NULL REFERENCES palette_variants(id) ON DELETE CASCADE,
    content_addressed_path   TEXT,
    status                   TEXT         NOT NULL DEFAULT 'pending'
                                          CHECK (status IN ('pending', 'baked', 'failed')),
    failure_reason           TEXT,
    baked_at                 TIMESTAMPTZ,
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (asset_id, palette_variant_id)
);
CREATE INDEX asset_variants_asset_idx  ON asset_variants (asset_id);
CREATE INDEX asset_variants_status_idx ON asset_variants (status);

-- =========================================================================
-- 8. Tilemaps
-- =========================================================================

-- A tilemap is a structured object built on top of a sprite_animated
-- asset. It carries the *grid* (cols x rows of 32x32 cells), the *tile
-- entities* sliced from those cells (one entity_type per non-empty
-- cell), and the *adjacency graph* implied by the source sheet's
-- layout. The grid is semantically significant in both axes — adjacent
-- cells are likely connecting tiles — so we keep that information
-- machine-readable instead of throwing it away at slice time.
--
-- One tilemap per backing asset (UNIQUE asset_id). Re-uploading the
-- same logical tilemap with edits goes through tilemaps.Service.Replace,
-- which diffs by per-cell pixel hash so unchanged cells keep their
-- entity_type id (preserving every map_tiles reference).
CREATE TABLE tilemaps (
    id              BIGSERIAL    PRIMARY KEY,
    asset_id        BIGINT       NOT NULL UNIQUE REFERENCES assets(id) ON DELETE RESTRICT,
    name            TEXT         NOT NULL UNIQUE,
    cols            INTEGER      NOT NULL CHECK (cols > 0),
    rows            INTEGER      NOT NULL CHECK (rows > 0),
    tile_size       INTEGER      NOT NULL DEFAULT 32 CHECK (tile_size = 32),
    non_empty_count INTEGER      NOT NULL DEFAULT 0 CHECK (non_empty_count >= 0),
    folder_id       BIGINT       REFERENCES asset_folders(id) ON DELETE SET NULL,
    created_by      BIGINT       NOT NULL REFERENCES designers(id),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX tilemaps_folder_idx     ON tilemaps (folder_id);
CREATE INDEX tilemaps_created_by_idx ON tilemaps (created_by);

-- One row per non-empty cell of a tilemap. (entity_type_id is wired in
-- the entity_types section below; see tilemap_tiles_entity_type_fkey.)
--
-- pixel_hash is sha256 of the cell's 32x32 RGBA bytes. Used by Replace
-- to detect "this cell is identical to what was there before" and skip
-- it. edge_hash_n/e/s/w are sha256 of the cell's edge strips (32 px x
-- 1 px each); used by the auto-socket extractor to match cells whose
-- north edge equals another cell's south edge.
CREATE TABLE tilemap_tiles (
    tilemap_id       BIGINT       NOT NULL REFERENCES tilemaps(id) ON DELETE CASCADE,
    cell_col         INTEGER      NOT NULL CHECK (cell_col >= 0),
    cell_row         INTEGER      NOT NULL CHECK (cell_row >= 0),
    entity_type_id   BIGINT       NOT NULL,
    pixel_hash       BYTEA        NOT NULL,
    edge_hash_n      BYTEA        NOT NULL,
    edge_hash_e      BYTEA        NOT NULL,
    edge_hash_s      BYTEA        NOT NULL,
    edge_hash_w      BYTEA        NOT NULL,
    PRIMARY KEY (tilemap_id, cell_col, cell_row),
    UNIQUE (tilemap_id, entity_type_id)
);
CREATE INDEX tilemap_tiles_entity_idx ON tilemap_tiles (entity_type_id);

-- =========================================================================
-- 9. Entities
-- =========================================================================

-- Entity types are the templates spawned into the live ECS. Per the
-- holistic redesign, ENTITY is the broad parent class with four
-- subtypes:
--
--   tile   — a sprite designed to stamp into a map's tile grid.
--            tilemap_id + cell_col + cell_row link back to the source
--            tilemap; sprite_asset_id (= the tilemap's backing PNG)
--            and atlas_index are derived but kept on the row so the
--            renderer doesn't have to JOIN to draw.
--   npc    — a non-player character. recipe_id + active_bake_id link
--            into the character pipeline; sprite_asset_id is the
--            current bake.
--   pc     — a player-controllable character template. Same shape as
--            npc but the runtime spawns one per logged-in player
--            instead of per placement.
--   logic  — an invisible game-logic entity (spawn point, region
--            trigger, item consumer, level transition). May or may
--            not have a sprite.
--
-- Collider fields are in pixels (renderer multiplies by 256 sub-pixels
-- at load time). Anchor is offset from top-left of the sprite cell,
-- in pixels. default_collision_mask is a uint32 layer bitmask;
-- defaults to layer 1 ("land").
--
-- y_sort_anchor + draw_above_player are the standard pixel-RPG depth
-- controls (walk-behind props, HUD overlays). procedural_include lets
-- a designer remove a specific tile entity from the procedural fill
-- candidate pool without retagging.
CREATE TABLE entity_types (
    id                       BIGSERIAL    PRIMARY KEY,
    name                     TEXT         NOT NULL UNIQUE,
    entity_class             TEXT         NOT NULL DEFAULT 'logic'
                                          CHECK (entity_class IN ('tile', 'npc', 'pc', 'logic')),

    -- Render link. For tiles, sprite_asset_id is the tilemap's backing
    -- PNG and atlas_index is (cell_row * tilemap.cols + cell_col).
    sprite_asset_id          BIGINT       REFERENCES assets(id) ON DELETE SET NULL,
    atlas_index              INTEGER      NOT NULL DEFAULT 0 CHECK (atlas_index >= 0),
    default_animation_id     BIGINT       REFERENCES asset_animations(id) ON DELETE SET NULL,

    -- Tile-class linkage. Populated only when entity_class='tile';
    -- enforced as a soft invariant in the entities service (a tile
    -- entity always knows which tilemap cell produced it).
    tilemap_id               BIGINT       REFERENCES tilemaps(id) ON DELETE CASCADE,
    cell_col                 INTEGER,
    cell_row                 INTEGER,

    -- Character linkage. Populated only when entity_class IN ('npc','pc').
    -- recipe_id is the editing handle; active_bake_id points at the most
    -- recently published bake. Both null on first save; the publish
    -- pipeline fills active_bake_id and updates sprite_asset_id.
    recipe_id                BIGINT,      -- FK wired below after character_recipes exists
    active_bake_id           BIGINT,      -- FK wired below after character_bakes exists

    -- Collider + render flags.
    collider_w               INTEGER      NOT NULL DEFAULT 16 CHECK (collider_w >= 0),
    collider_h               INTEGER      NOT NULL DEFAULT 16 CHECK (collider_h >= 0),
    collider_anchor_x        INTEGER      NOT NULL DEFAULT 8  CHECK (collider_anchor_x >= 0),
    collider_anchor_y        INTEGER      NOT NULL DEFAULT 16 CHECK (collider_anchor_y >= 0),
    default_collision_mask   BIGINT       NOT NULL DEFAULT 1,
    y_sort_anchor            BOOLEAN      NOT NULL DEFAULT false,
    draw_above_player        BOOLEAN      NOT NULL DEFAULT false,
    procedural_include       BOOLEAN      NOT NULL DEFAULT true,

    tags                     TEXT[]       NOT NULL DEFAULT '{}',
    created_by               BIGINT       NOT NULL REFERENCES designers(id),
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX entity_types_class_idx       ON entity_types (entity_class, lower(name));
CREATE INDEX entity_types_tags_idx        ON entity_types USING GIN (tags);
CREATE INDEX entity_types_created_by_idx  ON entity_types (created_by);
-- Idempotency lookup for the tilemap auto-slice pipeline ("do we
-- already have an entity for cell N of this sheet?").
CREATE INDEX entity_types_sprite_atlas_idx
    ON entity_types (sprite_asset_id, atlas_index)
    WHERE sprite_asset_id IS NOT NULL;
CREATE INDEX entity_types_tilemap_idx ON entity_types (tilemap_id) WHERE tilemap_id IS NOT NULL;

-- Wire the deferred FKs from tilemap_tiles -> entity_types now that
-- entity_types exists.
ALTER TABLE tilemap_tiles
    ADD CONSTRAINT tilemap_tiles_entity_type_fkey
    FOREIGN KEY (entity_type_id) REFERENCES entity_types(id) ON DELETE CASCADE;

-- Compositional component table: each row attaches one named component
-- (Position, Velocity, Sprite, Collider, Tile, Static, ...) to an
-- entity type with a typed JSON config. component_kind matches the
-- keys registered in internal/entities/components/registry.go. New
-- kinds added there must NOT require a migration here — the registry
-- is open-ended and config_json drives the per-kind shape.
CREATE TABLE entity_components (
    entity_type_id  BIGINT       NOT NULL REFERENCES entity_types(id) ON DELETE CASCADE,
    component_kind  TEXT         NOT NULL,
    config_json     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_type_id, component_kind)
);
CREATE INDEX entity_components_kind_idx ON entity_components (component_kind);

-- Per-entity-type automation AST (triggers + conditions + actions).
-- The compiler at publish time walks the AST and produces pre-bound
-- system functions for live execution. One row per entity_type; the
-- AST is always full-replaced on save.
CREATE TABLE entity_automations (
    entity_type_id      BIGINT       NOT NULL PRIMARY KEY REFERENCES entity_types(id) ON DELETE CASCADE,
    automation_ast_json JSONB        NOT NULL DEFAULT '{"automations":[]}'::jsonb,
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Edge sockets are the WFC vocabulary for procedural map generation.
-- Each project defines a small set of socket types ("field", "stone-cliff",
-- "water"); each tile-class entity declares which socket sits on each
-- of its four edges. WFC uses the (entity_type, edge, socket_type)
-- graph to decide which tiles can neighbour each other.
--
-- color is 0xRRGGBBAA; the Mapmaker draws the socket badge on hovered
-- tiles so designers can see compatibility at a glance.
CREATE TABLE edge_socket_types (
    id          BIGSERIAL    PRIMARY KEY,
    name        TEXT         NOT NULL UNIQUE,
    color       BIGINT       NOT NULL DEFAULT x'ffd34aff'::bigint,
    created_by  BIGINT       REFERENCES designers(id),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- One row per tile-class entity carrying its four-edge socket
-- assignment. The tilemap auto-slicer seeds these by matching edge
-- pixel hashes; designers can override per-cell from the tilemap viewer.
CREATE TABLE tile_edge_assignments (
    entity_type_id  BIGINT       NOT NULL PRIMARY KEY REFERENCES entity_types(id) ON DELETE CASCADE,
    north_socket_id BIGINT       REFERENCES edge_socket_types(id) ON DELETE SET NULL,
    east_socket_id  BIGINT       REFERENCES edge_socket_types(id) ON DELETE SET NULL,
    south_socket_id BIGINT       REFERENCES edge_socket_types(id) ON DELETE SET NULL,
    west_socket_id  BIGINT       REFERENCES edge_socket_types(id) ON DELETE SET NULL
);

-- Tile groups are NxM meta-tiles composed of multiple tile entity types.
-- The Mapmaker treats them as one unit when painting; the runtime spawns
-- N x M individual tile entities at the final position. layout_json is
-- a 2D array of entity_type_ids (or 0 for "empty cell").
--
-- exclude_members_from_procedural keeps a member tile out of the single-
-- tile candidate pool; use_group_in_procedural lets the group itself
-- be placed atomically by procedural fill.
CREATE TABLE tile_groups (
    id                                 BIGSERIAL    PRIMARY KEY,
    name                               TEXT         NOT NULL UNIQUE,
    width                              INTEGER      NOT NULL CHECK (width  >= 1 AND width  <= 16),
    height                             INTEGER      NOT NULL CHECK (height >= 1 AND height <= 16),
    layout_json                        JSONB        NOT NULL DEFAULT '[]'::jsonb,
    tags                               TEXT[]       NOT NULL DEFAULT '{}',
    exclude_members_from_procedural    BOOLEAN      NOT NULL DEFAULT false,
    use_group_in_procedural            BOOLEAN      NOT NULL DEFAULT true,
    created_by                         BIGINT       NOT NULL REFERENCES designers(id),
    created_at                         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at                         TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX tile_groups_tags_idx ON tile_groups USING GIN (tags);

-- =========================================================================
-- 10. Characters (NPC + PC production pipeline)
-- =========================================================================

-- The character system is a definition -> recipe -> bake -> entity
-- pipeline. Designers (or players, for player-owned characters)
-- compose appearance + stats + talents into a *recipe*; the bake job
-- composes that recipe's parts into a single sprite-sheet asset; the
-- bake's asset becomes the sprite_asset_id on an NPC- or PC-class
-- entity_type. Recipes/bakes/parts are subordinate to entity_types in
-- the IDE — they show up inside the character generator editor mode,
-- not as top-level objects.

-- Designer-authored slot vocabulary. Seeded with 24 default slots; the
-- created_by-NULL rows mark them as "(system)" in the UI.
CREATE TABLE character_slots (
    id                   BIGSERIAL    PRIMARY KEY,
    key                  TEXT         NOT NULL UNIQUE,
    label                TEXT         NOT NULL,
    required             BOOLEAN      NOT NULL DEFAULT FALSE,
    order_index          INTEGER      NOT NULL DEFAULT 0,
    default_layer_order  INTEGER      NOT NULL DEFAULT 0,
    allows_palette       BOOLEAN      NOT NULL DEFAULT FALSE,
    created_by           BIGINT       REFERENCES designers(id),
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX character_slots_order_idx ON character_slots (order_index, id);
INSERT INTO character_slots
    (key, label, required, order_index, default_layer_order, allows_palette, created_by)
VALUES
    ('body',          'Body',           TRUE,    10,  100, TRUE,  NULL),
    ('skin',          'Skin tone',      FALSE,   20,  110, TRUE,  NULL),
    ('eyes',          'Eyes',           FALSE,   30,  200, TRUE,  NULL),
    ('eyebrows',      'Eyebrows',       FALSE,   40,  210, TRUE,  NULL),
    ('mouth',         'Mouth',          FALSE,   50,  220, FALSE, NULL),
    ('face',          'Face details',   FALSE,   60,  230, FALSE, NULL),
    ('hair_back',     'Hair (back)',    FALSE,   70,   90, TRUE,  NULL),
    ('hair_front',    'Hair (front)',   FALSE,   80,  300, TRUE,  NULL),
    ('facial_hair',   'Facial hair',    FALSE,   90,  310, TRUE,  NULL),
    ('ears_horns',    'Ears / horns',   FALSE,  100,  320, FALSE, NULL),
    ('headwear',      'Headwear',       FALSE,  110,  400, TRUE,  NULL),
    ('neck',          'Neck',           FALSE,  120,  150, TRUE,  NULL),
    ('torso_under',   'Torso (under)',  FALSE,  130,  140, TRUE,  NULL),
    ('torso_outer',   'Torso (outer)',  FALSE,  140,  160, TRUE,  NULL),
    ('arms_gloves',   'Arms / gloves',  FALSE,  150,  170, TRUE,  NULL),
    ('legs',          'Legs',           FALSE,  160,  130, TRUE,  NULL),
    ('boots',         'Boots',          FALSE,  170,  120, TRUE,  NULL),
    ('cloak',         'Cloak / cape',   FALSE,  180,   80, TRUE,  NULL),
    ('backpack',      'Backpack',       FALSE,  190,  410, FALSE, NULL),
    ('accessory_a',   'Accessory A',    FALSE,  200,  420, TRUE,  NULL),
    ('accessory_b',   'Accessory B',    FALSE,  210,  430, TRUE,  NULL),
    ('main_hand',     'Main hand',      FALSE,  220,  500, FALSE, NULL),
    ('off_hand',      'Off hand',       FALSE,  230,  510, FALSE, NULL),
    ('aura',          'Aura / effect',  FALSE,  240,  600, FALSE, NULL);

-- Per-slot parts: a part links a slot to an existing sprite asset with
-- a frame map describing which source frames cover each canonical
-- animation. layer_order NULL inherits slot.default_layer_order.
-- ON DELETE RESTRICT keeps parts and source assets pinned: parts can't
-- be orphaned and source assets can't disappear from under recipes.
CREATE TABLE character_parts (
    id                   BIGSERIAL    PRIMARY KEY,
    slot_id              BIGINT       NOT NULL REFERENCES character_slots(id) ON DELETE RESTRICT,
    asset_id             BIGINT       NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
    name                 TEXT         NOT NULL,
    tags                 TEXT[]       NOT NULL DEFAULT '{}',
    compatible_tags      TEXT[]       NOT NULL DEFAULT '{}',
    layer_order          INTEGER,
    frame_map_json       JSONB        NOT NULL DEFAULT '{}'::jsonb,
    palette_regions_json JSONB,
    created_by           BIGINT       NOT NULL REFERENCES designers(id),
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (slot_id, asset_id)
);
CREATE INDEX character_parts_slot_idx       ON character_parts (slot_id);
CREATE INDEX character_parts_tags_idx       ON character_parts USING GIN (tags);
CREATE INDEX character_parts_created_by_idx ON character_parts (created_by);

-- Editable selections. owner_kind+owner_id is a polymorphic owner
-- (designer or player); player rows are scoped by player_id from auth
-- context, never from the request body. recipe_hash is sha256 of the
-- normalized recipe content and is the dedup key for bakes.
CREATE TABLE character_recipes (
    id              BIGSERIAL    PRIMARY KEY,
    owner_kind      TEXT         NOT NULL CHECK (owner_kind IN ('designer', 'player')),
    owner_id        BIGINT       NOT NULL,
    name            TEXT         NOT NULL,
    appearance_json JSONB        NOT NULL DEFAULT '{}'::jsonb,
    stats_json      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    talents_json    JSONB        NOT NULL DEFAULT '{}'::jsonb,
    recipe_hash     BYTEA        NOT NULL,
    created_by      BIGINT       NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX character_recipes_owner_idx ON character_recipes (owner_kind, owner_id);
CREATE INDEX character_recipes_hash_idx  ON character_recipes (recipe_hash);

-- Composed sprite outputs. asset_id points at the resulting (kind=
-- sprite_animated) row; the renderer uses that id, not the bake row.
-- Partial unique on recipe_hash WHERE status='baked' lets two bakes
-- with the same hash coexist as long as at most one is baked
-- successfully (allowing retry of failed bakes without dropping the
-- successful row).
CREATE TABLE character_bakes (
    id              BIGSERIAL    PRIMARY KEY,
    recipe_id       BIGINT       NOT NULL REFERENCES character_recipes(id) ON DELETE CASCADE,
    recipe_hash     BYTEA        NOT NULL,
    asset_id        BIGINT       REFERENCES assets(id) ON DELETE SET NULL,
    status          TEXT         NOT NULL CHECK (status IN ('pending', 'baked', 'failed')),
    failure_reason  TEXT         NOT NULL DEFAULT '',
    baked_at        TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX character_bakes_recipe_idx ON character_bakes (recipe_id);
CREATE UNIQUE INDEX character_bakes_hash_baked_uniq
    ON character_bakes (recipe_hash) WHERE status = 'baked';

-- Wire the deferred FKs from entity_types -> character_recipes/bakes
-- now that those tables exist.
ALTER TABLE entity_types
    ADD CONSTRAINT entity_types_recipe_fkey
        FOREIGN KEY (recipe_id)      REFERENCES character_recipes(id) ON DELETE SET NULL,
    ADD CONSTRAINT entity_types_active_bake_fkey
        FOREIGN KEY (active_bake_id) REFERENCES character_bakes(id)   ON DELETE SET NULL;

-- Designer-authored stat models. stats_json is an array of StatDef;
-- creation_rules_json carries point-buy / preset / freeform config.
CREATE TABLE character_stat_sets (
    id                   BIGSERIAL    PRIMARY KEY,
    key                  TEXT         NOT NULL UNIQUE,
    name                 TEXT         NOT NULL,
    stats_json           JSONB        NOT NULL DEFAULT '[]'::jsonb,
    creation_rules_json  JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_by           BIGINT       NOT NULL REFERENCES designers(id),
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Talent tree header. layout_mode tells the UI whether to render a
-- tree, a tiered list, a free pick-list, or a web. currency_key
-- references a stat key inside the linked stat set; the validator
-- enforces that the referenced stat exists and has kind='resource'.
CREATE TABLE character_talent_trees (
    id            BIGSERIAL    PRIMARY KEY,
    key           TEXT         NOT NULL UNIQUE,
    name          TEXT         NOT NULL,
    description   TEXT         NOT NULL DEFAULT '',
    currency_key  TEXT         NOT NULL DEFAULT 'talent_points',
    layout_mode   TEXT         NOT NULL DEFAULT 'tree'
                                CHECK (layout_mode IN ('tree','tiered','free_list','web')),
    created_by    BIGINT       NOT NULL REFERENCES designers(id),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Talent nodes belong to one tree. mutex_group is empty by default
-- (no group); when non-empty, recipe validation enforces "at most one
-- node in this group has rank > 0". Effects are structured JSON only
-- — no embedded code.
CREATE TABLE character_talent_nodes (
    id                 BIGSERIAL    PRIMARY KEY,
    tree_id            BIGINT       NOT NULL REFERENCES character_talent_trees(id) ON DELETE CASCADE,
    key                TEXT         NOT NULL,
    name               TEXT         NOT NULL,
    description        TEXT         NOT NULL DEFAULT '',
    icon_asset_id      BIGINT       REFERENCES assets(id) ON DELETE SET NULL,
    max_rank           INTEGER      NOT NULL DEFAULT 1 CHECK (max_rank > 0),
    cost_json          JSONB        NOT NULL DEFAULT '{}'::jsonb,
    prerequisites_json JSONB        NOT NULL DEFAULT '[]'::jsonb,
    effect_json        JSONB        NOT NULL DEFAULT '[]'::jsonb,
    layout_json        JSONB,
    mutex_group        TEXT         NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (tree_id, key)
);
CREATE INDEX character_talent_nodes_mutex_idx
    ON character_talent_nodes (tree_id, mutex_group)
    WHERE mutex_group <> '';

-- Player-owned saved characters. Always scoped by player_id. The
-- recipe is per-player so editing one player's appearance never
-- affects another player. entity_type_id is the runtime spawn target
-- (a pc-class entity_type minted at first save).
CREATE TABLE player_characters (
    id              BIGSERIAL    PRIMARY KEY,
    player_id       BIGINT       NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    recipe_id       BIGINT       REFERENCES character_recipes(id) ON DELETE SET NULL,
    active_bake_id  BIGINT       REFERENCES character_bakes(id)   ON DELETE SET NULL,
    entity_type_id  BIGINT       REFERENCES entity_types(id)      ON DELETE SET NULL,
    name            TEXT         NOT NULL,
    public_bio      TEXT         NOT NULL DEFAULT '',
    private_notes   TEXT         NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX player_characters_player_idx ON player_characters (player_id);
CREATE INDEX player_characters_entity_idx ON player_characters (entity_type_id);

-- =========================================================================
-- 11. Maps
-- =========================================================================

-- A MAP is pure tile geometry — width, height, layered tile placements,
-- lighting cells, and the procedural-generation machinery (locked
-- cells, sample patches, constraints). Maps own no entity placements,
-- no HUD, no instancing settings; those concerns live on a LEVEL that
-- references this map. One map can back many levels (e.g. day/night
-- variants of a town square share geometry but layer different NPCs
-- and HUD).
--
-- mode discriminates the editing surface:
--   authored     hand-painted; map_tiles is the source of truth
--   procedural   tiles are materialized from map_locked_cells +
--                map_sample_patches + map_constraints + WFC
CREATE TABLE maps (
    id           BIGSERIAL    PRIMARY KEY,
    name         TEXT         NOT NULL UNIQUE,
    width        INTEGER      NOT NULL CHECK (width  >= 1),
    height       INTEGER      NOT NULL CHECK (height >= 1),
    mode         TEXT         NOT NULL DEFAULT 'authored'
                              CHECK (mode IN ('authored', 'procedural')),
    seed         BIGINT,
    created_by   BIGINT       NOT NULL REFERENCES designers(id),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX maps_created_by_idx ON maps (created_by);

-- Per-map ordered layers. kind = 'tile' for terrain/decoration layers
-- and 'lighting' for the dedicated lighting layer. ord controls draw
-- order; lower ord renders first. y_sort_entities flips the layer
-- between "deterministic draw order" and "compare entity y-anchors"
-- (the standard pixel-RPG walk-behind trick).
CREATE TABLE map_layers (
    id                 BIGSERIAL    PRIMARY KEY,
    map_id             BIGINT       NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    name               TEXT         NOT NULL,
    kind               TEXT         NOT NULL CHECK (kind IN ('tile', 'lighting')),
    ord                INTEGER      NOT NULL DEFAULT 0,
    y_sort_entities    BOOLEAN      NOT NULL DEFAULT false,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (map_id, name)
);
CREATE INDEX map_layers_map_idx ON map_layers (map_id, ord);

-- Per-tile placements. (map_id, layer_id, x, y) uniquely identifies
-- one cell. The override columns let designers tweak per-cell
-- collision without forking a whole entity_type. rotation_degrees
-- stores quarter-turn rotation (0/90/180/270).
CREATE TABLE map_tiles (
    map_id                    BIGINT       NOT NULL REFERENCES maps(id)        ON DELETE CASCADE,
    layer_id                  BIGINT       NOT NULL REFERENCES map_layers(id)  ON DELETE CASCADE,
    x                         INTEGER      NOT NULL,
    y                         INTEGER      NOT NULL,
    entity_type_id            BIGINT       NOT NULL REFERENCES entity_types(id) ON DELETE RESTRICT,
    rotation_degrees          SMALLINT     NOT NULL DEFAULT 0
                                            CHECK (rotation_degrees IN (0, 90, 180, 270)),
    anim_override             SMALLINT,
    collision_shape_override  SMALLINT,
    collision_mask_override   BIGINT,
    custom_flags_json         JSONB,
    PRIMARY KEY (map_id, layer_id, x, y)
);
-- Cross-layer chunk loads: fetch every layer's tile at (x, y) in one
-- query.
CREATE INDEX map_tiles_xy_idx ON map_tiles (map_id, x, y);

-- Lighting cells live on lighting-kind layers only. Coordinates are
-- tile-grid units (matching map_tiles); color is 0xRRGGBBAA;
-- intensity 0..255 multiplies the layer's visibility.
CREATE TABLE map_lighting_cells (
    map_id      BIGINT       NOT NULL REFERENCES maps(id)        ON DELETE CASCADE,
    layer_id    BIGINT       NOT NULL REFERENCES map_layers(id)  ON DELETE CASCADE,
    x           INTEGER      NOT NULL,
    y           INTEGER      NOT NULL,
    color       BIGINT       NOT NULL,
    intensity   SMALLINT     NOT NULL CHECK (intensity BETWEEN 0 AND 255),
    PRIMARY KEY (map_id, layer_id, x, y)
);
CREATE INDEX map_lighting_cells_xy_idx ON map_lighting_cells (map_id, x, y);

-- Designer-painted cells that survive procedural generation. Acts as
-- the "Lock brush" channel: any cell here is fed to the generator as
-- an anchor and re-asserted into map_tiles after a materialize so
-- the persistent copy is self-sufficient.
CREATE TABLE map_locked_cells (
    map_id           BIGINT       NOT NULL REFERENCES maps(id)         ON DELETE CASCADE,
    layer_id         BIGINT       NOT NULL REFERENCES map_layers(id)   ON DELETE CASCADE,
    x                INTEGER      NOT NULL,
    y                INTEGER      NOT NULL,
    entity_type_id   BIGINT       NOT NULL REFERENCES entity_types(id) ON DELETE CASCADE,
    rotation_degrees SMALLINT     NOT NULL DEFAULT 0
                                  CHECK (rotation_degrees IN (0, 90, 180, 270)),
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (map_id, layer_id, x, y)
);
CREATE INDEX map_locked_cells_map_idx ON map_locked_cells (map_id);

-- One sample patch per procedural map. The overlapping-model WFC
-- reads this rectangle of cells from the referenced layer and uses
-- it as the learning input — every NxN window in the patch becomes a
-- legal pattern in the output. Cells aren't copied; the engine reads
-- them live from map_tiles bounded by the patch rect.
CREATE TABLE map_sample_patches (
    map_id      BIGINT       PRIMARY KEY REFERENCES maps(id)       ON DELETE CASCADE,
    layer_id    BIGINT       NOT NULL    REFERENCES map_layers(id) ON DELETE CASCADE,
    x           INTEGER      NOT NULL    CHECK (x >= 0),
    y           INTEGER      NOT NULL    CHECK (y >= 0),
    width       INTEGER      NOT NULL    CHECK (width  BETWEEN 2 AND 32),
    height      INTEGER      NOT NULL    CHECK (height BETWEEN 2 AND 32),
    pattern_n   SMALLINT     NOT NULL    DEFAULT 2 CHECK (pattern_n IN (2, 3)),
    updated_at  TIMESTAMPTZ  NOT NULL    DEFAULT now()
);

-- Per-map non-local procedural constraints (border, path, etc.). Kind
-- is a small enum; params is a JSONB blob whose shape is parsed by
-- the corresponding constraint handler. Rows run in id order so when
-- a border + path are both present the border pins fire first.
CREATE TABLE map_constraints (
    id          BIGSERIAL    PRIMARY KEY,
    map_id      BIGINT       NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    kind        TEXT         NOT NULL CHECK (kind IN ('border', 'path')),
    params      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX map_constraints_map_idx ON map_constraints (map_id, id);

-- =========================================================================
-- 12. Worlds & Levels
-- =========================================================================

-- A WORLD is a graph of LEVELs connected by transition entities (e.g.
-- a door entity placed in a level points at another level via a
-- level_transition action). The graph is *implicit*: levels carry a
-- world_id back-pointer; the transitions live in level_entities; there
-- is no separate world_edges table because that would be denormalized
-- against the door entities' real configuration.
--
-- Worlds are optional. A level can exist without a world (for solo
-- iteration / sandbox testing); only levels reachable from a world's
-- start_level participate in a published world's playable graph.
CREATE TABLE worlds (
    id              BIGSERIAL    PRIMARY KEY,
    name            TEXT         NOT NULL UNIQUE,
    start_level_id  BIGINT,                 -- FK wired below after levels exists
    settings_json   JSONB        NOT NULL DEFAULT '{}'::jsonb,
    folder_id       BIGINT       REFERENCES asset_folders(id) ON DELETE SET NULL,
    created_by      BIGINT       NOT NULL REFERENCES designers(id),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX worlds_folder_idx ON worlds (folder_id);

-- A LEVEL = a MAP + entity placements + level-scoped automations + HUD
-- + instancing/persistence settings. Each level references one map;
-- multiple levels may reference the same map. The level is the unit of
-- "play" — sandbox launches a level, the published runtime spawns a
-- level instance per AOI namespace, the HUD belongs to the level not
-- the map.
--
-- public            level is reachable in the published runtime
-- instancing_mode   shared / per_user / per_party
-- persistence_mode  persistent / transient
-- spectator_policy  public / private / invite
-- hud_layout_json   the HUD definition (anchors + widgets) for this
--                   level; one HUD per level per design
CREATE TABLE levels (
    id                       BIGSERIAL    PRIMARY KEY,
    name                     TEXT         NOT NULL UNIQUE,
    map_id                   BIGINT       NOT NULL REFERENCES maps(id) ON DELETE RESTRICT,
    world_id                 BIGINT       REFERENCES worlds(id) ON DELETE SET NULL,
    public                   BOOLEAN      NOT NULL DEFAULT false,
    instancing_mode          TEXT         NOT NULL DEFAULT 'shared'
                                          CHECK (instancing_mode IN ('shared', 'per_user', 'per_party')),
    persistence_mode         TEXT         NOT NULL DEFAULT 'persistent'
                                          CHECK (persistence_mode IN ('persistent', 'transient')),
    refresh_window_seconds   INTEGER,
    reset_rules_json         JSONB        NOT NULL DEFAULT '{}'::jsonb,
    spectator_policy         TEXT         NOT NULL DEFAULT 'public'
                                          CHECK (spectator_policy IN ('public', 'private', 'invite')),
    hud_layout_json          JSONB        NOT NULL DEFAULT '{"v":1,"anchors":{}}'::jsonb,
    folder_id                BIGINT       REFERENCES asset_folders(id) ON DELETE SET NULL,
    created_by               BIGINT       NOT NULL REFERENCES designers(id),
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX levels_map_idx        ON levels (map_id);
CREATE INDEX levels_world_idx      ON levels (world_id) WHERE world_id IS NOT NULL;
CREATE INDEX levels_folder_idx     ON levels (folder_id);
CREATE INDEX levels_created_by_idx ON levels (created_by);

-- Wire the deferred FK from worlds.start_level_id -> levels.id.
ALTER TABLE worlds
    ADD CONSTRAINT worlds_start_level_fkey
    FOREIGN KEY (start_level_id) REFERENCES levels(id) ON DELETE SET NULL;

-- Non-tile entity placements on a level. NPCs, doors, spawn points,
-- region triggers, item consumers — anything with coordinates that
-- isn't part of the tile grid lives here. Tile placements stay in
-- map_tiles (they belong to the map's geometry, not the level's
-- staging).
--
-- instance_overrides_json is a small bag of per-placement overrides
-- (component config tweaks, custom name/dialog, transition target).
CREATE TABLE level_entities (
    id                       BIGSERIAL    PRIMARY KEY,
    level_id                 BIGINT       NOT NULL REFERENCES levels(id)       ON DELETE CASCADE,
    entity_type_id           BIGINT       NOT NULL REFERENCES entity_types(id) ON DELETE RESTRICT,
    x                        INTEGER      NOT NULL,
    y                        INTEGER      NOT NULL,
    rotation_degrees         SMALLINT     NOT NULL DEFAULT 0
                                          CHECK (rotation_degrees IN (0, 90, 180, 270)),
    instance_overrides_json  JSONB        NOT NULL DEFAULT '{}'::jsonb,
    tags                     TEXT[]       NOT NULL DEFAULT '{}',
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX level_entities_level_idx       ON level_entities (level_id);
CREATE INDEX level_entities_entity_type_idx ON level_entities (entity_type_id);
CREATE INDEX level_entities_xy_idx          ON level_entities (level_id, x, y);
CREATE INDEX level_entities_tags_idx        ON level_entities USING GIN (tags);

-- "Common events": named action groups callable from any automation
-- on the same level. Solves the indie-RPG copy-paste-the-action-list
-- problem (award xp + play fanfare + flash screen → call once by
-- name). name is the lookup key (not the surrogate id) so a designer
-- can rename a group at publish time and the call sites pick up the
-- new definition without an FK migration.
CREATE TABLE level_action_groups (
    id            BIGSERIAL    PRIMARY KEY,
    level_id      BIGINT       NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    name          TEXT         NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    actions_json  JSONB        NOT NULL DEFAULT '[]'::jsonb,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (level_id, name)
);
CREATE INDEX level_action_groups_level_idx ON level_action_groups (level_id);

-- Per-level scratch space for the no-code event system: switches
-- (booleans) and variables (ints) shared across automations. value_json
-- keeps the column polymorphic without a UNION table:
--   kind = 'bool' → JSON true / false
--   kind = 'int'  → JSON number (int32 range enforced in Go)
CREATE TABLE level_flags (
    level_id   BIGINT       NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    key        TEXT         NOT NULL CHECK (length(key) BETWEEN 1 AND 64),
    kind       TEXT         NOT NULL CHECK (kind IN ('bool', 'int')),
    value_json JSONB        NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (level_id, key)
);
CREATE INDEX level_flags_level_idx ON level_flags (level_id);

-- Per-level spectator allowlist used when levels.spectator_policy =
-- 'invite'. Public levels ignore this table; private levels reject all
-- player-realm spectators outright.
CREATE TABLE level_spectator_invites (
    level_id    BIGINT       NOT NULL REFERENCES levels(id)  ON DELETE CASCADE,
    player_id   BIGINT       NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    granted_by  BIGINT       NOT NULL REFERENCES designers(id),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (level_id, player_id)
);
CREATE INDEX level_spectator_invites_player_idx ON level_spectator_invites (player_id);

-- =========================================================================
-- 13. Runtime state
-- =========================================================================

-- Canonical persisted state for live LEVEL instances. The runtime keeps
-- recent mutations in the Redis Streams WAL; every 20 ticks (~2s) the
-- WAL is folded into Postgres here in a single transaction. On
-- recovery: load this row, replay the WAL since last_flushed_tick.
--
-- Keyed by (level_id, instance_id) — instance_id namespaces designer
-- sandboxes ("sandbox:<designer_id>:<level_id>") from played sessions
-- ("play:<world_id>:<level_id>") so a designer testing a level never
-- collides with players on the published copy.
--
-- state_blob_fb is the FlatBuffers encoding of the level's ECS world;
-- BYTEA (not JSONB) so we don't pay schema-translation cost on flush.
CREATE TABLE level_state (
    level_id            BIGINT       NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    instance_id         TEXT         NOT NULL,
    state_blob_fb       BYTEA        NOT NULL,
    last_flushed_tick   BIGINT       NOT NULL,
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (level_id, instance_id)
);
CREATE INDEX level_state_updated_at_idx ON level_state (updated_at);
