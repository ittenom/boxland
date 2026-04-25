-- 0014_tile_groups.up.sql
--
-- Tile groups are N x M meta-tiles composed of multiple tile entity types.
-- The Mapmaker treats them as one unit when painting; the runtime spawns
-- N x M individual tile entities at the final position. layout_json is a
-- 2D array of entity_type_ids (or 0 for "no tile in this slot"), aligned
-- to the group's grid.
--
-- See PLAN.md §4e and the Tile-Group composer surface in §5d.

CREATE TABLE tile_groups (
    id            BIGSERIAL    PRIMARY KEY,
    name          TEXT         NOT NULL UNIQUE,
    width         INTEGER      NOT NULL CHECK (width  >= 1 AND width  <= 16),
    height        INTEGER      NOT NULL CHECK (height >= 1 AND height <= 16),
    layout_json   JSONB        NOT NULL DEFAULT '[]'::jsonb,
    tags          TEXT[]       NOT NULL DEFAULT '{}',
    created_by    BIGINT       NOT NULL REFERENCES designers(id),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX tile_groups_tags_idx ON tile_groups USING GIN (tags);
