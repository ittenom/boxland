-- 0010_asset_variants.up.sql
--
-- Baked output of the palette-variant pipeline. Each row is one re-colored
-- PNG produced by the bake job, stored at a content-addressed path so
-- re-running the bake on identical inputs is a no-op.
--
-- status drives the UI: pending = bake job not yet run; baked = path
-- populated and CDN-ready; failed = surface to the designer with details.

CREATE TABLE asset_variants (
    id                       BIGSERIAL   PRIMARY KEY,
    asset_id                 BIGINT      NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    palette_variant_id       BIGINT      NOT NULL REFERENCES palette_variants(id) ON DELETE CASCADE,
    content_addressed_path   TEXT,
    status                   TEXT        NOT NULL DEFAULT 'pending'
                                          CHECK (status IN ('pending', 'baked', 'failed')),
    failure_reason           TEXT,
    baked_at                 TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (asset_id, palette_variant_id)
);

CREATE INDEX asset_variants_asset_idx ON asset_variants (asset_id);
CREATE INDEX asset_variants_status_idx ON asset_variants (status);
