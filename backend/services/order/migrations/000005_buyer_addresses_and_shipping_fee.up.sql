-- A buyer may have one or more saved shipping addresses, mirroring the
-- vendor's own warehouse-address shape. Exactly one may be the default.
CREATE TABLE buyer_addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    buyer_id UUID NOT NULL,
    recipient_name TEXT NOT NULL,
    phone TEXT NOT NULL,
    province TEXT NOT NULL,
    district TEXT NOT NULL,
    ward TEXT NOT NULL,
    street_address TEXT NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX buyer_addresses_buyer_id_idx ON buyer_addresses (buyer_id);
CREATE UNIQUE INDEX buyer_addresses_one_default_idx ON buyer_addresses (buyer_id) WHERE is_default;

-- The buyer's chosen destination is snapshotted onto the order at checkout
-- time — never re-derived from a later address-book edit, same convention
-- as order_items.product_name.
ALTER TABLE orders
    ADD COLUMN recipient_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN phone TEXT NOT NULL DEFAULT '',
    ADD COLUMN province TEXT NOT NULL DEFAULT '',
    ADD COLUMN district TEXT NOT NULL DEFAULT '',
    ADD COLUMN ward TEXT NOT NULL DEFAULT '',
    ADD COLUMN street_address TEXT NOT NULL DEFAULT '';

ALTER TABLE orders ALTER COLUMN recipient_name DROP DEFAULT;
ALTER TABLE orders ALTER COLUMN phone DROP DEFAULT;
ALTER TABLE orders ALTER COLUMN province DROP DEFAULT;
ALTER TABLE orders ALTER COLUMN district DROP DEFAULT;
ALTER TABLE orders ALTER COLUMN ward DROP DEFAULT;
ALTER TABLE orders ALTER COLUMN street_address DROP DEFAULT;

-- Set once, right after checkout, by a follow-up update once Shipment has
-- quoted the fee — mirrors how commission_rate_bps/commission_amount are
-- snapshotted after the fact at payment time.
ALTER TABLE vendor_orders ADD COLUMN shipping_fee_amount BIGINT NOT NULL DEFAULT 0;
