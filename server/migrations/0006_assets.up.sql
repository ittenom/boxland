-- 0006_assets.up.sql
--
-- Single canonical assets table. One row per uploaded source file (sprite
-- sheet, tile sheet, audio clip). kind discriminates the type;
-- metadata_json carries kind-specific fields so we don't fan out into
-- separate tables per kind. See PLAN.md §1 "Asset model".
--
-- content_addressed_path is the sha256-shaped key in object storage from
-- internal/persistence/objectstore.ContentAddressedKey -- e.g.
-- "assets/aa/bb/<sha256>". Identical bytes uploaded twice yield the same
-- key (idempotent). Different filenames pointing at the same bytes are
-- reused.

CREATE TABLE assets (
    id                       BIGSERIAL   PRIMARY KEY,
    kind                     TEXT        NOT NULL CHECK (kind IN ('sprite', 'tile', 'audio')),
    name                     TEXT        NOT NULL,
    content_addressed_path   TEXT        NOT NULL,
    original_format          TEXT        NOT NULL, -- 'png', 'wav', 'ogg', 'mp3', etc.
    metadata_json            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    tags                     TEXT[]      NOT NULL DEFAULT '{}',
    created_by               BIGINT      NOT NULL REFERENCES designers(id),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Two assets are allowed to point at the same content (different names,
-- different kinds) but the (kind, name) pair must be unique within the
-- project so designers can rely on "the boss tile" referencing the same
-- thing every time.
CREATE UNIQUE INDEX assets_kind_name_idx ON assets (kind, name);

-- For filtering in the Asset Manager grid (PLAN.md §5c).
CREATE INDEX assets_tags_idx ON assets USING GIN (tags);
CREATE INDEX assets_kind_idx ON assets (kind);
CREATE INDEX assets_created_by_idx ON assets (created_by);
