-- 0031_map_hud_layout
--
-- Per-realm HUD layout. One row per map. Lives on the existing maps row
-- (no new table) because there is exactly one HUD per realm, it always
-- loads with the realm, and the publish pipeline already snapshots maps.
-- Avoids a JOIN on the AOI hot path.
--
-- Shape (versioned, JSON Schema-ish):
--   {
--     "v": 1,
--     "anchors": {
--       "top-left":      { "dir": "vertical",   "gap": 4, "offsetX": 8, "offsetY": 8, "widgets": [...] },
--       "bottom-right":  { "dir": "horizontal", "gap": 4, "offsetX": 8, "offsetY": 8, "widgets": [...] }
--     }
--   }
--
-- See server/internal/hud/layout.go for the typed Configurable that
-- decodes/validates this column. Caps live in Validate(): max 32
-- widgets per anchor and 128 total per realm.

ALTER TABLE maps
  ADD COLUMN hud_layout_json JSONB NOT NULL DEFAULT '{"v":1,"anchors":{}}'::jsonb;
