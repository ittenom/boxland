-- 0012_entity_components.up.sql
--
-- Compositional component table: each row attaches one named component
-- (Position, Velocity, Sprite, Collider, Health, Spawner, ...) to an
-- entity type with a typed configuration payload.
--
-- component_kind matches the keys registered in internal/sim/components.
-- New kinds added there must NOT require a migration here -- the catalog
-- is open-ended, and config_json drives the per-kind shape.
--
-- (entity_type_id, component_kind) is unique: a single entity type can't
-- have two of the same component (multiple Spawners, etc. are modeled by
-- richer Spawner config, not by row duplication).

CREATE TABLE entity_components (
    entity_type_id  BIGINT      NOT NULL REFERENCES entity_types(id) ON DELETE CASCADE,
    component_kind  TEXT        NOT NULL,
    config_json     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_type_id, component_kind)
);

CREATE INDEX entity_components_kind_idx ON entity_components (component_kind);
