-- 0008_palettes.up.sql
--
-- Named, project-wide palette presets. The default retro palette baked
-- into pixel.css mirrors one of these rows so designers see the same
-- color tokens in both the UI chrome and the swatch picker.
--
-- colors is an ordered array of 0xRRGGBBAA integers. Order is meaningful
-- for ergonomic swatch selection (number keys 1..9 in the picker).

CREATE TABLE palettes (
    id          BIGSERIAL   PRIMARY KEY,
    name        TEXT        NOT NULL UNIQUE,
    colors      BIGINT[]    NOT NULL DEFAULT '{}',  -- 0xRRGGBBAA per slot
    created_by  BIGINT      REFERENCES designers(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the default retro palette so new projects have something to bind
-- to immediately. The colors mirror /server/static/css/pixel.css tokens
-- (--bx-bg-*, --bx-fg-*, --bx-accent, etc.) at 0xRRGGBBAA encoding.
INSERT INTO palettes (name, colors) VALUES (
    'Default retro',
    ARRAY[
        x'1a1733ff'::bigint,  -- bg-1
        x'241f4aff'::bigint,  -- bg-2
        x'2e2766ff'::bigint,  -- bg-3
        x'3a318aff'::bigint,  -- bg-4
        x'6a5fc0ff'::bigint,  -- line
        x'a99cffff'::bigint,  -- line-hi
        x'f4ecffff'::bigint,  -- fg-1
        x'c4b9eeff'::bigint,  -- fg-2
        x'8a7fbfff'::bigint,  -- fg-3
        x'ffd34aff'::bigint,  -- accent
        x'ffe79aff'::bigint,  -- accent-hi
        x'4ad7ffff'::bigint,  -- info
        x'5fe87bff'::bigint,  -- success
        x'ff9e3dff'::bigint,  -- warn
        x'ff5e7eff'::bigint   -- error
    ]
);
