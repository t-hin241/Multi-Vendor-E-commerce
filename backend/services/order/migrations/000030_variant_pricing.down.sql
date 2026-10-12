DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM order_items WHERE pricing_source='variant') THEN
  RAISE EXCEPTION 'Keep variant pricing snapshots and compatible readers';
 END IF;
END $$;
ALTER TABLE order_items DROP CONSTRAINT variant_pricing_snapshot;
DROP TRIGGER order_item_price_immutable ON order_items;
DROP FUNCTION order_item_price_immutable();
ALTER TABLE order_items DROP COLUMN pricing_source,DROP COLUMN catalog_product_version,DROP COLUMN catalog_variant_version;
