-- Existing amounts remain untouched. Historical variant orders used product pricing.
ALTER TABLE order_items ADD COLUMN pricing_source text NOT NULL DEFAULT 'product' CHECK(pricing_source IN ('product','variant'));
ALTER TABLE order_items ADD COLUMN catalog_product_version bigint CHECK(catalog_product_version>0);
ALTER TABLE order_items ADD COLUMN catalog_variant_version bigint CHECK(catalog_variant_version>0);
ALTER TABLE order_items ADD CONSTRAINT variant_pricing_snapshot CHECK(pricing_source<>'variant' OR (variant_id IS NOT NULL AND catalog_product_version IS NOT NULL AND catalog_variant_version IS NOT NULL));
CREATE FUNCTION order_item_price_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.price_amount,NEW.quantity,NEW.subtotal_amount,NEW.variant_id,NEW.pricing_source,NEW.catalog_product_version,NEW.catalog_variant_version)
 IS DISTINCT FROM ROW(OLD.price_amount,OLD.quantity,OLD.subtotal_amount,OLD.variant_id,OLD.pricing_source,OLD.catalog_product_version,OLD.catalog_variant_version)
 THEN RAISE EXCEPTION 'Order item pricing snapshots are immutable'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER order_item_price_immutable BEFORE UPDATE ON order_items FOR EACH ROW EXECUTE FUNCTION order_item_price_immutable();
