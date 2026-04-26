-- 0034_characters.up.sql
--
-- Character generator + asset pipeline (PLAN.md companion: see
-- docs/superpowers/plans/2026-04-26-character-generator-plan.md).
--
-- Nine new tables form a definition -> recipe -> bake -> instance pipeline:
--
--   character_slots          designer-authored slot vocabulary (body, hair, ...)
--   character_parts          slot -> existing sprite asset, with frame mapping
--   character_recipes        editable layered selections (designer or player)
--   character_bakes          composed sprite outputs, content-addressed by recipe_hash
--   character_stat_sets      designer-defined stat models
--   character_talent_trees   designer-defined talent graphs (header)
--   character_talent_nodes   talent graph nodes (cost, prereqs, mutex_group, effect)
--   npc_templates            designer-authored reusable NPC definitions
--   player_characters        player-owned saved characters (scoped by player_id)
--
-- No per-row `published` columns: lifecycle flows through the existing
-- artifact pipeline (server/internal/publishing/artifact). Designer
-- mutations land in `drafts` rows of kind `character_slot`/`character_part`
-- /`character_stat_set`/`character_talent_tree`/`npc_template`; Push to Live
-- promotes them inside one transaction.

-- ---------------------------------------------------------------------------
-- character_slots
-- ---------------------------------------------------------------------------
CREATE TABLE character_slots (
    id                   BIGSERIAL    PRIMARY KEY,
    key                  TEXT         NOT NULL UNIQUE,
    label                TEXT         NOT NULL,
    required             BOOLEAN      NOT NULL DEFAULT FALSE,
    order_index          INTEGER      NOT NULL DEFAULT 0,
    default_layer_order  INTEGER      NOT NULL DEFAULT 0,
    allows_palette       BOOLEAN      NOT NULL DEFAULT FALSE,
    -- created_by is nullable so the migration can seed the 24 default
    -- slots without inventing a fake designer row. Designer-authored
    -- slots created via the admin UI populate this; downstream queries
    -- display NULL as "(system)".
    created_by           BIGINT       REFERENCES designers(id),
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX character_slots_order_idx ON character_slots (order_index, id);

-- Seed the 24 default slots. created_by IS NULL marks system-seeded rows;
-- the UI displays these as "(system)" without a designer attribution.
-- `default_layer_order` values are spaced by 10 so designers can wedge
-- custom slots between them without rewriting the column.
--
-- We use ON CONFLICT DO NOTHING so this migration is idempotent if a
-- developer manually re-applies it; the unique key on `key` makes the
-- conflict deterministic.
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
    ('aura',          'Aura / effect',  FALSE,  240,  600, FALSE, NULL)
ON CONFLICT (key) DO NOTHING;

-- ---------------------------------------------------------------------------
-- character_parts
-- ---------------------------------------------------------------------------
-- A part links a slot to an existing sprite asset, with a frame map
-- describing which source frame ranges cover each canonical animation.
-- `layer_order` is nullable: NULL means "inherit slot.default_layer_order"
-- so designers don't have to set per-part overrides for the common case.
--
-- ON DELETE RESTRICT on both FKs is the safe default — parts can't be
-- orphaned and source assets can't disappear from under recipes.
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

-- ---------------------------------------------------------------------------
-- character_recipes
-- ---------------------------------------------------------------------------
-- Editable selections. owner_kind+owner_id is a polymorphic owner
-- (designer or player); player rows are scoped by player_id from auth
-- context, never from the request body. recipe_hash is sha256 of the
-- normalized recipe content (see characters.Recipe.Normalize) and is
-- the dedup key for bakes.
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

-- ---------------------------------------------------------------------------
-- character_bakes
-- ---------------------------------------------------------------------------
-- Composed sprite outputs. `asset_id` points at the (kind=sprite) row
-- the bake produced; the renderer uses that id, not the bake row.
-- Status is a small enum: pending (placeholder), baked (success),
-- failed (try again next publish).
--
-- The partial unique on recipe_hash where status='baked' lets two bakes
-- with the same hash coexist as long as at most one is `baked`. This
-- keeps the dedup invariant ("one successful bake per recipe content")
-- without preventing retry of failed bakes.
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

-- ---------------------------------------------------------------------------
-- character_stat_sets
-- ---------------------------------------------------------------------------
-- A stat set is a designer-authored definition of stats and creation
-- rules. stats_json is an array of StatDef; creation_rules_json is the
-- point-buy / preset / freeform configuration.
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

-- ---------------------------------------------------------------------------
-- character_talent_trees
-- ---------------------------------------------------------------------------
-- Tree header. layout_mode tells the UI whether to render a tree, a
-- tiered list, a free pick-list, or a web. currency_key references a
-- stat key inside the linked stat set; the validator enforces that the
-- referenced stat exists and has kind='resource'.
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

-- ---------------------------------------------------------------------------
-- character_talent_nodes
-- ---------------------------------------------------------------------------
-- Nodes belong to one tree. mutex_group is empty by default (no group);
-- when non-empty, recipe validation enforces "at most one node in this
-- group has rank > 0". Effects are structured JSON only — no code.
CREATE TABLE character_talent_nodes (
    id                BIGSERIAL    PRIMARY KEY,
    tree_id           BIGINT       NOT NULL REFERENCES character_talent_trees(id) ON DELETE CASCADE,
    key               TEXT         NOT NULL,
    name              TEXT         NOT NULL,
    description       TEXT         NOT NULL DEFAULT '',
    icon_asset_id     BIGINT       REFERENCES assets(id) ON DELETE SET NULL,
    max_rank          INTEGER      NOT NULL DEFAULT 1 CHECK (max_rank > 0),
    cost_json         JSONB        NOT NULL DEFAULT '{}'::jsonb,
    prerequisites_json JSONB       NOT NULL DEFAULT '[]'::jsonb,
    effect_json       JSONB        NOT NULL DEFAULT '[]'::jsonb,
    layout_json       JSONB,
    mutex_group       TEXT         NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (tree_id, key)
);
CREATE INDEX character_talent_nodes_mutex_idx
    ON character_talent_nodes (tree_id, mutex_group)
    WHERE mutex_group <> '';

-- ---------------------------------------------------------------------------
-- npc_templates
-- ---------------------------------------------------------------------------
-- Reusable NPC definitions that pin a recipe + an active bake +
-- (optionally) an entity_type. The auto-mint flow (handler-side) keeps
-- entity_type_id non-NULL once the NPC has been published.
CREATE TABLE npc_templates (
    id              BIGSERIAL    PRIMARY KEY,
    name            TEXT         NOT NULL UNIQUE,
    recipe_id       BIGINT       REFERENCES character_recipes(id) ON DELETE SET NULL,
    active_bake_id  BIGINT       REFERENCES character_bakes(id) ON DELETE SET NULL,
    entity_type_id  BIGINT       REFERENCES entity_types(id) ON DELETE SET NULL,
    tags            TEXT[]       NOT NULL DEFAULT '{}',
    created_by      BIGINT       NOT NULL REFERENCES designers(id),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX npc_templates_tags_idx       ON npc_templates USING GIN (tags);
CREATE INDEX npc_templates_created_by_idx ON npc_templates (created_by);
CREATE INDEX npc_templates_entity_idx     ON npc_templates (entity_type_id);

-- ---------------------------------------------------------------------------
-- player_characters
-- ---------------------------------------------------------------------------
-- Player-owned saved characters. Always scoped by player_id. The
-- recipe is per-player so editing one player's appearance never
-- affects another player.
CREATE TABLE player_characters (
    id              BIGSERIAL    PRIMARY KEY,
    player_id       BIGINT       NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    recipe_id       BIGINT       REFERENCES character_recipes(id) ON DELETE SET NULL,
    active_bake_id  BIGINT       REFERENCES character_bakes(id) ON DELETE SET NULL,
    name            TEXT         NOT NULL,
    public_bio      TEXT         NOT NULL DEFAULT '',
    private_notes   TEXT         NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX player_characters_player_idx ON player_characters (player_id);
