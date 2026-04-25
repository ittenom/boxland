-- 0007_asset_animations.up.sql
--
-- Animation tags extracted from sprite/tile sheets at import time. Each
-- row is one named animation in the source sheet (e.g. "walk_north" frames
-- 0..3). FPS, loop direction, and frame range come from the importer
-- (Aseprite frameTags, TexturePacker frame names, or manual config).

CREATE TABLE asset_animations (
    id          BIGSERIAL   PRIMARY KEY,
    asset_id    BIGINT      NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    frame_from  INTEGER     NOT NULL CHECK (frame_from >= 0),
    frame_to    INTEGER     NOT NULL CHECK (frame_to >= frame_from),
    direction   TEXT        NOT NULL DEFAULT 'forward'
                            CHECK (direction IN ('forward', 'reverse', 'pingpong')),
    fps         INTEGER     NOT NULL DEFAULT 8 CHECK (fps > 0 AND fps <= 60),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (asset_id, name)
);

CREATE INDEX asset_animations_asset_idx ON asset_animations (asset_id);
