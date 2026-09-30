-- Dropping these tables loses capture records and pending side effects.
-- Only for a rollback before any checkout ran on the new flow; see
-- deploy/order-runbook.md.
DROP TABLE IF EXISTS order_effects;
DROP TABLE IF EXISTS order_payments;
DROP TABLE IF EXISTS checkout_operations;

ALTER TABLE vendor_orders
    DROP COLUMN IF EXISTS completed_at,
    DROP COLUMN IF EXISTS refunded_amount,
    DROP COLUMN IF EXISTS commission_source,
    DROP COLUMN IF EXISTS commission_rounding,
    DROP COLUMN IF EXISTS commission_base_amount,
    DROP COLUMN IF EXISTS commission_rule_version,
    DROP COLUMN IF EXISTS commission_rule_id,
    DROP COLUMN IF EXISTS shipping_quoted_at,
    DROP COLUMN IF EXISTS package_weight_grams,
    DROP COLUMN IF EXISTS shipping_fee_rule_version,
    DROP COLUMN IF EXISTS shipping_fee_rule_id,
    DROP COLUMN IF EXISTS shipping_zone_id,
    DROP COLUMN IF EXISTS shipping_carrier_id,
    DROP COLUMN IF EXISTS version;

DROP TRIGGER IF EXISTS orders_fill_subtotal ON orders;
DROP FUNCTION IF EXISTS orders_fill_subtotal();
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_subtotal_non_negative;
ALTER TABLE orders
    DROP COLUMN IF EXISTS paid_at,
    DROP COLUMN IF EXISTS refunded_amount,
    DROP COLUMN IF EXISTS shipping_amount,
    DROP COLUMN IF EXISTS subtotal_amount,
    DROP COLUMN IF EXISTS checkout_state,
    DROP COLUMN IF EXISTS version;

DROP INDEX IF EXISTS commission_rules_version_key;
ALTER TABLE commission_rules DROP COLUMN IF EXISTS version;
