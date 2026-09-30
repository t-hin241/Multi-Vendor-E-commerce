-- Loses the settlement ledger. Only before any payout was made from it.
DROP TABLE IF EXISTS payout_item_entries;
ALTER TABLE payout_batches DROP COLUMN IF EXISTS currency;
ALTER TABLE payout_batches DROP CONSTRAINT IF EXISTS payout_batches_status_check;
ALTER TABLE payout_batches ADD CONSTRAINT payout_batches_status_check
    CHECK (status IN ('pending', 'submitted', 'succeeded', 'failed'));
ALTER TABLE payout_items DROP CONSTRAINT IF EXISTS payout_items_succeeded_needs_evidence;
ALTER TABLE payout_items
    DROP COLUMN IF EXISTS resolved_at,
    DROP COLUMN IF EXISTS resolved_by,
    DROP COLUMN IF EXISTS note,
    DROP COLUMN IF EXISTS evidence_reference;
DROP TABLE IF EXISTS settlement_entries;
DROP TABLE IF EXISTS settlement_vendor_orders;
DROP FUNCTION IF EXISTS settlement_append_only();
DROP INDEX IF EXISTS payment_refunds_vendor_order_idx;
ALTER TABLE payment_refunds DROP COLUMN IF EXISTS vendor_order_id;
