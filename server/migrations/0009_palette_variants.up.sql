-- 0009_palette_variants.up.sql
--
-- A *recipe* for re-coloring an asset. One row per (asset, recipe) pair.
-- The recipe is a JSON map of source_color -> dest_color (both 0xRRGGBBAA
-- as numeric strings to keep JSON safe). Optionally references a palette
-- preset so the picker UI can suggest matching colors.
--
-- Per PLAN.md §1 "Palette swap": variants are PRE-BAKED at publish time
-- into separate PNGs. This table is the *recipe*; asset_variants is the
-- *baked output*.

CREATE TABLE palette_variants (
    id                  BIGSERIAL   PRIMARY KEY,
    asset_id            BIGINT      NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    name                TEXT        NOT NULL,
    palette_id          BIGINT      REFERENCES palettes(id),
    source_to_dest_json JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_by          BIGINT      REFERENCES designers(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (asset_id, name)
);

CREATE INDEX palette_variants_asset_idx ON palette_variants (asset_id);
