ALTER TABLE shipments DROP CONSTRAINT shipments_status_check;
ALTER TABLE shipments ADD CONSTRAINT shipments_status_check
    CHECK (status IN ('pending', 'ready_to_ship', 'shipped', 'delivered'));

DROP INDEX IF EXISTS shipments_buyer_id_idx;
ALTER TABLE shipments
    DROP COLUMN buyer_id,
    DROP COLUMN carrier_id,
    DROP COLUMN zone_id,
    DROP COLUMN zone_name,
    DROP COLUMN fee_rule_id,
    DROP COLUMN fee_amount,
    DROP COLUMN package_weight_grams,
    DROP COLUMN recipient_name,
    DROP COLUMN phone,
    DROP COLUMN province,
    DROP COLUMN district,
    DROP COLUMN ward,
    DROP COLUMN street_address;

DROP TABLE IF EXISTS shipment_tracking_events;
DROP TABLE IF EXISTS vendor_shipping_methods;
DROP TABLE IF EXISTS shipping_fee_rules;
DROP TABLE IF EXISTS shipping_zone_provinces;
DROP TABLE IF EXISTS shipping_zones;
DROP TABLE IF EXISTS carriers;
