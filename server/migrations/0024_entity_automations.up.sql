-- 0024_entity_automations.up.sql
--
-- Per-entity-type automation AST. PLAN.md §1 (Automations) + §125.
-- One row per entity_type carrying the full AutomationSet JSON; the
-- compiler at publish time (task #126) walks the AST and produces
-- pre-bound system functions for live execution.

CREATE TABLE entity_automations (
    entity_type_id      BIGINT       NOT NULL PRIMARY KEY REFERENCES entity_types(id) ON DELETE CASCADE,
    automation_ast_json JSONB        NOT NULL DEFAULT '{"automations":[]}'::jsonb,
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now()
);
