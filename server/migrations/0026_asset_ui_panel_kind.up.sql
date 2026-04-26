-- 0026_asset_ui_panel_kind.up.sql
--
-- Adds 'ui_panel' to the assets.kind enum so designers can mark a PNG as
-- a 9-slice border source (consumed by the bx-9patch CSS utility and the
-- player-side HUD). Stored bytes are an ordinary PNG; the only difference
-- is intent + the metadata_json fields the asset carries (slice px,
-- repeat mode). Keeping this as a kind (instead of a tag) means:
--   * asset filters / pickers can show only valid panel sources
--   * the upload pipeline knows to skip tile-sheet auto-slicing for it
--   * thumbnail rendering can preview the border at the configured slice
--
-- Indie-RPG research §P1 #6 ("nine-slice is table stakes for every
-- chrome surface"). See docs/adding-a-component.md "Nine-slice panels".

ALTER TABLE assets DROP CONSTRAINT assets_kind_check;
ALTER TABLE assets ADD CONSTRAINT assets_kind_check
    CHECK (kind IN ('sprite', 'tile', 'audio', 'ui_panel'));
