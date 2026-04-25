-- 0003_publish_diffs.up.sql
--
-- Records every artifact published in a given changeset. Consumed by:
--   * the diff preview modal (task #134) before the user confirms a push
--   * the post-publish audit log
--   * the LivePublish broadcast payload (task #132)
--
-- One row per (changeset, artifact). changeset_id values come from
-- publish_changeset_seq and are allocated by the publish pipeline.
--
-- summary_line is the human-readable single-line description shown in the
-- diff preview UI; structured_diff_json is the full per-field delta
-- (configurable.StructuredDiff).

CREATE SEQUENCE publish_changeset_seq AS BIGINT MINVALUE 1 START 1;

CREATE TABLE publish_diffs (
    id                   BIGSERIAL   PRIMARY KEY,
    changeset_id         BIGINT      NOT NULL,
    artifact_kind        TEXT        NOT NULL,
    artifact_id          BIGINT      NOT NULL,
    op                   TEXT        NOT NULL CHECK (op IN ('created', 'updated', 'deleted')),
    summary_line         TEXT        NOT NULL,
    structured_diff_json JSONB       NOT NULL,
    published_by         BIGINT      NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX publish_diffs_changeset_idx ON publish_diffs (changeset_id);
CREATE INDEX publish_diffs_artifact_idx  ON publish_diffs (artifact_kind, artifact_id);
CREATE INDEX publish_diffs_published_by_idx ON publish_diffs (published_by);
