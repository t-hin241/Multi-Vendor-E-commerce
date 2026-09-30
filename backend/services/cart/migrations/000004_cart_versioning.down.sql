-- Rolling back drops checkout receipts: only safe while no Order build that
-- calls the consume contract is running (see deploy/cart-runbook.md).
DROP TABLE IF EXISTS cart_checkout_operations;

ALTER TABLE cart_items DROP CONSTRAINT IF EXISTS cart_items_quantity_limit;
ALTER TABLE cart_items DROP CONSTRAINT IF EXISTS cart_items_seen_price_pair;
ALTER TABLE cart_items DROP CONSTRAINT IF EXISTS cart_items_version_positive;
ALTER TABLE cart_items DROP COLUMN IF EXISTS seen_currency;
ALTER TABLE cart_items DROP COLUMN IF EXISTS seen_price_amount;
ALTER TABLE cart_items DROP COLUMN IF EXISTS version;

DROP INDEX IF EXISTS carts_updated_at_idx;
ALTER TABLE carts DROP CONSTRAINT IF EXISTS carts_version_positive;
ALTER TABLE carts DROP COLUMN IF EXISTS version;
