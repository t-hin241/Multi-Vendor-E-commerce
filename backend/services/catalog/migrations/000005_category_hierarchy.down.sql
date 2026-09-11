DROP INDEX categories_parent_id_idx;
ALTER TABLE categories DROP CONSTRAINT categories_level_parent_check;
ALTER TABLE categories DROP COLUMN level;
ALTER TABLE categories DROP COLUMN parent_id;
