-- Loses the outcome queue and refund evidence. Only for a rollback before
-- any Order refund was requested.
DROP TABLE IF EXISTS payment_refund_sync;
DROP INDEX IF EXISTS payment_refunds_intent_idx;
DROP INDEX IF EXISTS payment_refunds_status_idx;
DROP INDEX IF EXISTS payment_refunds_order_refund_key;
ALTER TABLE payment_refunds
    DROP CONSTRAINT IF EXISTS payment_refunds_succeeded_needs_evidence,
    DROP CONSTRAINT IF EXISTS payment_refunds_currency_format,
    DROP COLUMN IF EXISTS note,
    DROP COLUMN IF EXISTS resolved_by,
    DROP COLUMN IF EXISTS evidence_reference,
    DROP COLUMN IF EXISTS order_refund_id,
    DROP COLUMN IF EXISTS currency;
