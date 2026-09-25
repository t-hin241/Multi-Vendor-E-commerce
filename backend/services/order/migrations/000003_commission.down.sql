ALTER TABLE vendor_orders
    DROP COLUMN IF EXISTS commission_rate_bps,
    DROP COLUMN IF EXISTS commission_amount,
    DROP COLUMN IF EXISTS net_amount;

DROP TABLE IF EXISTS commission_rules;
