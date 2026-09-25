ALTER TABLE vendor_orders DROP COLUMN shipping_fee_amount;

ALTER TABLE orders
    DROP COLUMN recipient_name,
    DROP COLUMN phone,
    DROP COLUMN province,
    DROP COLUMN district,
    DROP COLUMN ward,
    DROP COLUMN street_address;

DROP TABLE IF EXISTS buyer_addresses;
