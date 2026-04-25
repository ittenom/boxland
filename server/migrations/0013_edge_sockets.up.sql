-- 0013_edge_sockets.up.sql
--
-- Edge sockets are the WFC vocabulary used by the procedural Mapmaker
-- (PLAN.md §4g). Each project defines a small set of socket types ("field",
-- "stone-cliff", "water"); each tile-kind entity declares which socket
-- type sits on each of its four edges. WFC uses the (entity_type, edge,
-- socket_type) graph to figure out which tiles can neighbour each other.
--
-- color is a 0xRRGGBBAA value the Mapmaker UI uses to draw the socket
-- badge on hovered tiles, so designers can see compatibility at a glance.

CREATE TABLE edge_socket_types (
    id          BIGSERIAL    PRIMARY KEY,
    name        TEXT         NOT NULL UNIQUE,
    color       BIGINT       NOT NULL DEFAULT x'ffd34aff'::bigint,  -- 0xRRGGBBAA accent
    created_by  BIGINT       REFERENCES designers(id),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE tile_edge_assignments (
    entity_type_id  BIGINT NOT NULL PRIMARY KEY REFERENCES entity_types(id) ON DELETE CASCADE,
    north_socket_id BIGINT REFERENCES edge_socket_types(id) ON DELETE SET NULL,
    east_socket_id  BIGINT REFERENCES edge_socket_types(id) ON DELETE SET NULL,
    south_socket_id BIGINT REFERENCES edge_socket_types(id) ON DELETE SET NULL,
    west_socket_id  BIGINT REFERENCES edge_socket_types(id) ON DELETE SET NULL
);
