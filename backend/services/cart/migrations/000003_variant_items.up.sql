ALTER TABLE cart_items ADD COLUMN variant_id UUID;

DROP INDEX cart_items_cart_product_key;
CREATE UNIQUE INDEX cart_items_cart_product_key ON cart_items (cart_id, product_id) WHERE variant_id IS NULL;
CREATE UNIQUE INDEX cart_items_cart_variant_key ON cart_items (cart_id, variant_id) WHERE variant_id IS NOT NULL;
