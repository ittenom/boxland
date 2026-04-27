-- 0043_asset_folders.up.sql
--
-- Asset folder system. Replaces the flat Asset Manager UI and
-- the flat tile palette in Mapmaker with an IDE-style filesystem.
--
-- Design notes (see plan in chat history):
--
--   * Four virtual top-level roots — sprite / tile / audio / ui_panel —
--     are NOT real rows. A folder's `kind_root` says which root it
--     belongs to; an asset whose folder_id is NULL lives in the kind
--     root directly. Keeps the tree model simple (no special-row
--     casing on the four "system" folders).
--
--   * Sort mode is per-folder. Five modes ship: alpha, date, type,
--     color (dominant_color column on assets), length (audio
--     metadata.duration_ms). UI hides modes that don't apply to the
--     folder's kind_root.
--
--   * Folder delete cascades to child folders but DOES NOT delete
--     assets — those bubble back up to the kind root via the
--     ON DELETE SET NULL on assets.folder_id. Designers do not lose
--     work by accidentally deleting a folder.
--
--   * Uniqueness on (parent, kind_root, lower(name)) makes folder
--     rename + create rules predictable: case-insensitive collision,
--     scoped to one parent, scoped to one kind_root. The COALESCE in
--     the index lets multiple roots have a "Forest" folder without
--     collision (parent_id NULL is shared otherwise).

CREATE TABLE asset_folders (
    id           BIGSERIAL    PRIMARY KEY,
    parent_id    BIGINT       REFERENCES asset_folders(id) ON DELETE CASCADE,
    kind_root    TEXT         NOT NULL CHECK (kind_root IN ('sprite','tile','audio','ui_panel')),
    name         TEXT         NOT NULL,
    sort_mode    TEXT         NOT NULL DEFAULT 'alpha'
                              CHECK (sort_mode IN ('alpha','date','type','color','length')),
    created_by   BIGINT       NOT NULL REFERENCES designers(id),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX asset_folders_parent_name_idx
    ON asset_folders (COALESCE(parent_id, 0), kind_root, lower(name));
CREATE INDEX asset_folders_kind_root_idx
    ON asset_folders (kind_root, parent_id);

-- Per-asset folder pointer. NULL = "lives in the kind root."
ALTER TABLE assets
    ADD COLUMN folder_id BIGINT REFERENCES asset_folders(id) ON DELETE SET NULL;
CREATE INDEX assets_folder_id_idx ON assets (folder_id);

-- Dominant color, packed as 0xRRGGBB (no alpha — alpha is meaningless
-- for the sort and the strip swatch). NULL = not yet computed; the
-- folders service backfills lazily when sort=color is requested.
ALTER TABLE assets
    ADD COLUMN dominant_color BIGINT;
