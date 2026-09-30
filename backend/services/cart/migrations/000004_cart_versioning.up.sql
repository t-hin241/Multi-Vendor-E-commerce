-- CRT-01/02/04: optimistic cart versions, per-line versions, a display-only
-- price reference, per-line quantity limit, and durable checkout
-- operations (snapshot + consume receipt) for Order.
--
-- The one-line-per-product/variant rule already exists since 000003 as two
-- partial unique indexes: (cart_id, product_id) WHERE variant_id IS NULL and
-- (cart_id, variant_id) WHERE variant_id IS NOT NULL. That gives the right
-- PostgreSQL semantics for a NULL variant (NULLs never collide in a plain
-- unique index), so they are kept as-is; deploy/cart-preflight.sql verifies
-- there are no duplicates before this migration runs.

ALTER TABLE carts ADD COLUMN version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE carts ADD CONSTRAINT carts_version_positive CHECK (version > 0);
CREATE INDEX carts_updated_at_idx ON carts (updated_at);

ALTER TABLE cart_items ADD COLUMN version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE cart_items ADD COLUMN seen_price_amount BIGINT;
ALTER TABLE cart_items ADD COLUMN seen_currency TEXT;
ALTER TABLE cart_items ADD CONSTRAINT cart_items_version_positive CHECK (version > 0);
ALTER TABLE cart_items ADD CONSTRAINT cart_items_seen_price_pair
    CHECK ((seen_price_amount IS NULL) = (seen_currency IS NULL) AND (seen_price_amount IS NULL OR seen_price_amount >= 0));
-- NOT VALID: enforced for every new write immediately; legacy rows are
-- checked by deploy/cart-validate.sql after the preflight is clean.
ALTER TABLE cart_items ADD CONSTRAINT cart_items_quantity_limit CHECK (quantity <= 999) NOT VALID;

-- No FK to carts on purpose: the receipt must outlive a purged idle cart so
-- a late consume retry still gets its original answer.
CREATE TABLE cart_checkout_operations (
    operation_id UUID PRIMARY KEY,
    buyer_id UUID NOT NULL,
    cart_id UUID NOT NULL,
    cart_version BIGINT NOT NULL CHECK (cart_version > 0),
    lines JSONB NOT NULL CHECK (jsonb_typeof(lines) = 'array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    consume_hash TEXT,
    receipt JSONB,
    consumed_at TIMESTAMPTZ,
    CONSTRAINT cart_checkout_operations_receipt_complete
        CHECK ((consumed_at IS NULL) = (receipt IS NULL) AND (receipt IS NULL) = (consume_hash IS NULL))
);

CREATE INDEX cart_checkout_operations_open_idx ON cart_checkout_operations (cart_id) WHERE consumed_at IS NULL;
CREATE INDEX cart_checkout_operations_created_idx ON cart_checkout_operations (created_at);
