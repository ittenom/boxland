-- 0039_map_sample_patches.up.sql
--
-- One sample patch per procedural map. The overlapping-model WFC reads
-- this rectangle of cells from the referenced layer and uses it as the
-- learning input — every NxN window in the patch becomes a legal
-- pattern in the output.
--
-- Storage choice: we store ONLY the (layer_id, x, y, w, h) reference,
-- not the cells themselves. Cells live in map_tiles + map_locked_cells
-- already; copying them here would (a) double-store, (b) drift when
-- the designer edits the source layer, and (c) require sync logic on
-- every paint stroke. The overlapping engine reads tiles via a
-- simple SELECT against map_tiles bounded by the patch rect (one
-- query per generation — tiny, indexed by map_id+layer_id).
--
-- One row per map (PRIMARY KEY map_id) — the designer picks ONE
-- sample area per map. If they want a different sample, they update
-- in place. Tenant isolation is inherited from maps.id.

CREATE TABLE map_sample_patches (
    map_id     BIGINT      PRIMARY KEY REFERENCES maps(id)        ON DELETE CASCADE,
    layer_id   BIGINT      NOT NULL    REFERENCES map_layers(id)  ON DELETE CASCADE,
    x          INTEGER     NOT NULL    CHECK (x >= 0),
    y          INTEGER     NOT NULL    CHECK (y >= 0),
    width      INTEGER     NOT NULL    CHECK (width  BETWEEN 2 AND 32),
    height     INTEGER     NOT NULL    CHECK (height BETWEEN 2 AND 32),
    pattern_n  SMALLINT    NOT NULL    DEFAULT 2 CHECK (pattern_n IN (2, 3)),
    updated_at TIMESTAMPTZ NOT NULL    DEFAULT now()
);
