ALTER TABLE categories ADD COLUMN parent_id UUID REFERENCES categories (id);
ALTER TABLE categories ADD COLUMN level SMALLINT NOT NULL DEFAULT 1 CHECK (level IN (1, 2, 3));
ALTER TABLE categories ADD CONSTRAINT categories_level_parent_check CHECK ((level = 1) = (parent_id IS NULL));

CREATE INDEX categories_parent_id_idx ON categories (parent_id);
