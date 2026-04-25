-- 0002_drafts.up.sql
--
-- The drafts table is the single home for in-progress edits across every
-- designer-managed artifact (assets, entity types, maps, palettes, edge
-- socket types, tile groups). The publish pipeline (internal/publishing/
-- artifact) walks rows here, validates each draft against the registered
-- handler, and applies them inside a single transaction. See PLAN.md §4o.
--
-- Multiple drafts per (kind, id) are NOT supported in v1 — last write wins,
-- enforced by the composite primary key.

CREATE TABLE drafts (
    artifact_kind TEXT        NOT NULL,
    artifact_id   BIGINT      NOT NULL,
    draft_json    JSONB       NOT NULL,
    created_by    BIGINT      NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (artifact_kind, artifact_id)
);

-- Index for "show me everything dirty by this designer" UI in the shell.
CREATE INDEX drafts_created_by_idx ON drafts (created_by);
