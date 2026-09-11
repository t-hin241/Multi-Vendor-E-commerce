DROP INDEX inventory_items_variant_id_key;
DROP INDEX inventory_items_product_id_key;
CREATE UNIQUE INDEX inventory_items_product_id_key ON inventory_items (product_id);
ALTER TABLE inventory_items DROP COLUMN variant_id;
