-- 0037_map_locked_cells.up.sql
--
-- Designer-painted cells that survive procedural generation. Acts as
-- the "Lock brush" channel: any cell present here is fed to the
-- generator as an anchor (so the procedural fill flows around it) and
-- re-asserted into map_tiles after a materialize so the persistent
-- copy is self-sufficient (the runtime loader doesn't have to join).
--
-- Composite primary key matches map_tiles' shape so brushing/erasing
-- is O(touched cells) and never rewrites the whole map.
--
-- ON DELETE CASCADE on layer_id so dropping a layer takes its locks
-- with it; on map_id so deleting a map cleans up cleanly.

CREATE TABLE map_locked_cells (
    map_id           BIGINT      NOT NULL REFERENCES maps(id)        ON DELETE CASCADE,
    layer_id         BIGINT      NOT NULL REFERENCES map_layers(id)  ON DELETE CASCADE,
    x                INTEGER     NOT NULL,
    y                INTEGER     NOT NULL,
    entity_type_id   BIGINT      NOT NULL REFERENCES entity_types(id) ON DELETE CASCADE,
    rotation_degrees SMALLINT    NOT NULL DEFAULT 0
                                  CHECK (rotation_degrees IN (0, 90, 180, 270)),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (map_id, layer_id, x, y)
);

CREATE INDEX map_locked_cells_map_idx ON map_locked_cells (map_id);
