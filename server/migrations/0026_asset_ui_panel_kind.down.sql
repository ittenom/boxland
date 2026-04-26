-- 0026_asset_ui_panel_kind.down.sql

-- Down requires no rows of the new kind exist. If any do, the migration
-- aborts -- safer than silently rewriting designer assets.
DELETE FROM assets WHERE kind = 'ui_panel';

ALTER TABLE assets DROP CONSTRAINT assets_kind_check;
ALTER TABLE assets ADD CONSTRAINT assets_kind_check
    CHECK (kind IN ('sprite', 'tile', 'audio'));
