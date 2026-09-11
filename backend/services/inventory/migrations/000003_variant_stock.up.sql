ALTER TABLE inventory_items ADD COLUMN variant_id UUID;

DROP INDEX inventory_items_product_id_key;
CREATE UNIQUE INDEX inventory_items_product_id_key ON inventory_items (product_id) WHERE variant_id IS NULL;
CREATE UNIQUE INDEX inventory_items_variant_id_key ON inventory_items (variant_id) WHERE variant_id IS NOT NULL;
