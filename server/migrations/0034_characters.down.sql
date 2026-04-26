-- 0034_characters.down.sql
--
-- Drop in reverse FK order so the cascades don't surprise.

DROP TABLE IF EXISTS player_characters;
DROP TABLE IF EXISTS npc_templates;
DROP TABLE IF EXISTS character_talent_nodes;
DROP TABLE IF EXISTS character_talent_trees;
DROP TABLE IF EXISTS character_stat_sets;
DROP TABLE IF EXISTS character_bakes;
DROP TABLE IF EXISTS character_recipes;
DROP TABLE IF EXISTS character_parts;
DROP TABLE IF EXISTS character_slots;
