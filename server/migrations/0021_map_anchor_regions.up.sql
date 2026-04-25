-- 0021_map_anchor_regions.up.sql
--
-- Anchor regions are designer-pinned tile clusters used by the procedural
-- Mapmaker (PLAN.md §4g). Each region holds an array of (x, y, entity_type_id)
-- triples that the WFC engine treats as already-collapsed cells before
-- search starts; the engine fills around them.
--
-- region_json shape:
--   { "name": "<optional>", "cells": [ {"x":1,"y":2,"entity_type_id":42}, ... ] }

CREATE TABLE map_anchor_regions (
    id           BIGSERIAL    PRIMARY KEY,
    map_id       BIGINT       NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    region_json  JSONB        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX map_anchor_regions_map_idx ON map_anchor_regions (map_id);
