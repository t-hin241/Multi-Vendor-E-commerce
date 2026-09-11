DROP INDEX cart_items_cart_variant_key;
DROP INDEX cart_items_cart_product_key;
CREATE UNIQUE INDEX cart_items_cart_product_key ON cart_items (cart_id, product_id);
ALTER TABLE cart_items DROP COLUMN variant_id;
