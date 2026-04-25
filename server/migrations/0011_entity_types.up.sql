-- 0011_entity_types.up.sql
--
-- Entity types are the templates spawned into the live ECS. Each row
-- captures everything the simulation + renderer need without joining to
-- per-component config rows: sprite + animation, AABB collider, default
-- collision-layer mask. Components compose on top via the entity_components
-- table (next migration).
--
-- Collider fields are in *pixels*, not sub-pixels: the runtime multiplies
-- by 256 (sub_per_px) at load time. Anchor is offset from top-left of the
-- sprite cell, in pixels.
--
-- default_collision_mask is a uint32 bitmask; defaults to layer 1 ("land")
-- per PLAN.md §1.

CREATE TABLE entity_types (
    id                       BIGSERIAL    PRIMARY KEY,
    name                     TEXT         NOT NULL UNIQUE,
    sprite_asset_id          BIGINT       REFERENCES assets(id) ON DELETE SET NULL,
    default_animation_id     BIGINT       REFERENCES asset_animations(id) ON DELETE SET NULL,
    collider_w               INTEGER      NOT NULL DEFAULT 16 CHECK (collider_w  >= 0),
    collider_h               INTEGER      NOT NULL DEFAULT 16 CHECK (collider_h  >= 0),
    collider_anchor_x        INTEGER      NOT NULL DEFAULT 8  CHECK (collider_anchor_x >= 0),
    collider_anchor_y        INTEGER      NOT NULL DEFAULT 16 CHECK (collider_anchor_y >= 0),
    default_collision_mask   BIGINT       NOT NULL DEFAULT 1, -- layer "land"
    tags                     TEXT[]       NOT NULL DEFAULT '{}',
    created_by               BIGINT       NOT NULL REFERENCES designers(id),
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX entity_types_tags_idx ON entity_types USING GIN (tags);
CREATE INDEX entity_types_created_by_idx ON entity_types (created_by);
