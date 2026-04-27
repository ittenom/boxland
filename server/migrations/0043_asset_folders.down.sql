-- 0043_asset_folders.down.sql
ALTER TABLE assets DROP COLUMN IF EXISTS dominant_color;
ALTER TABLE assets DROP COLUMN IF EXISTS folder_id;
DROP TABLE IF EXISTS asset_folders;
