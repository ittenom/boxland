-- 0040_map_constraints.up.sql
--
-- Per-map non-local constraints for procedural generation.
--
-- Today the constraint kinds are 'border' and 'path' (see
-- server/internal/maps/wfc/constraints.go). The schema is forward-
-- compatible: adding a new kind only needs a new value in the CHECK
-- list and a service-side parser entry.
--
-- One row = one constraint. A map can have many. They run in id order
-- (so when border + path are both present the border pins fire first).
--
-- Storage strategy: kind-specific parameters live in a small JSONB
-- column. JSONB keeps the wire shape obvious (the JS panel sends the
-- same blob it gets back) while still letting us index on kind.
--
-- Tenant isolation: every row is scoped via map_id → maps.id, which
-- inherits the world/owner scoping already enforced on maps.

CREATE TABLE map_constraints (
    id          BIGSERIAL    PRIMARY KEY,
    map_id      BIGINT       NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
    kind        TEXT         NOT NULL CHECK (kind IN ('border', 'path')),
    params      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX map_constraints_map_idx ON map_constraints (map_id, id);
