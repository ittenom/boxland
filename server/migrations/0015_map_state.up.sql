-- 0015_map_state.up.sql
--
-- Canonical persisted state for live map instances. The runtime keeps
-- recent mutations in the Redis Streams WAL (PLAN.md §4k); every 20
-- ticks (~2s) the WAL is folded into Postgres here in a single transaction.
-- On recovery: load this row, replay the WAL since last_flushed_tick.
--
-- state_blob_fb is the FlatBuffers MapState encoding (schemas/world.fbs).
-- Storing as bytea (not jsonb) so we don't pay the schema-translation cost
-- on the flush path.

CREATE TABLE map_state (
    map_id              BIGINT       NOT NULL,
    instance_id         TEXT         NOT NULL,
    state_blob_fb       BYTEA        NOT NULL,
    last_flushed_tick   BIGINT       NOT NULL,
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (map_id, instance_id)
);

-- Recovery boot scans every row; small index helps the per-process owner
-- filter quickly.
CREATE INDEX map_state_updated_at_idx ON map_state (updated_at);
