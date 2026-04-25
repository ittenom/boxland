-- 0018_map_layers.up.sql
--
-- Per-map ordered layers. kind = 'tile' for terrain/decoration layers and
-- 'lighting' for the dedicated lighting layer (PLAN.md §1 "Map layers").
-- ord controls draw order; lower ord renders first. The Mapmaker UI
-- enforces unique ord per map; the schema is permissive in case future
-- features need swap-by-rewrite semantics.

CREATE TABLE map_layers (
    id          BIGSERIAL    PRIMARY KEY,
    map_id      BIGINT       NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    name        TEXT         NOT NULL,
    kind        TEXT         NOT NULL CHECK (kind IN ('tile', 'lighting')),
    ord         INTEGER      NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (map_id, name)
);

CREATE INDEX map_layers_map_idx ON map_layers (map_id, ord);
