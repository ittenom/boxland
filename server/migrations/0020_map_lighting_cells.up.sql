-- 0020_map_lighting_cells.up.sql
--
-- Lighting layer cells. Coordinates are tile-grid units (matching map_tiles)
-- so the renderer can blit them through the same chunked AOI window.
-- color is 0xRRGGBBAA; intensity 0..255 multiplies the layer's visibility.

CREATE TABLE map_lighting_cells (
    map_id      BIGINT       NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    layer_id    BIGINT       NOT NULL REFERENCES map_layers(id) ON DELETE CASCADE,
    x           INTEGER      NOT NULL,
    y           INTEGER      NOT NULL,
    color       BIGINT       NOT NULL,    -- 0xRRGGBBAA
    intensity   SMALLINT     NOT NULL CHECK (intensity BETWEEN 0 AND 255),
    PRIMARY KEY (map_id, layer_id, x, y)
);

CREATE INDEX map_lighting_cells_xy_idx ON map_lighting_cells (map_id, x, y);
