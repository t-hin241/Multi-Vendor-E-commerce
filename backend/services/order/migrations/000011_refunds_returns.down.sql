-- Loses refund records and the return audit trail. Only for a rollback
-- before any refund was requested on the new flow.
DROP TABLE IF EXISTS return_request_events;
DROP TRIGGER IF EXISTS return_requests_fill_legacy ON return_requests;
DROP FUNCTION IF EXISTS return_requests_fill_legacy();
ALTER TABLE return_requests
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS restock,
    DROP COLUMN IF EXISTS inspection_note,
    DROP COLUMN IF EXISTS received_at,
    DROP COLUMN IF EXISTS received_by,
    DROP COLUMN IF EXISTS vendor_note,
    DROP COLUMN IF EXISTS evidence,
    DROP COLUMN IF EXISTS return_window_days,
    DROP COLUMN IF EXISTS policy_version,
    DROP COLUMN IF EXISTS refund_amount,
    DROP COLUMN IF EXISTS quantity;
-- Fails if an item already has several return requests; those must be
-- reviewed before rolling back.
DROP INDEX IF EXISTS return_requests_item_idx;
DROP INDEX IF EXISTS return_requests_open_item_key;
ALTER TABLE return_requests ADD CONSTRAINT return_requests_order_item_id_key UNIQUE (order_item_id);
ALTER TABLE return_requests DROP CONSTRAINT IF EXISTS return_requests_status_check;
UPDATE return_requests SET status = 'approved_awaiting_provider_refund'
WHERE status IN ('approved', 'received', 'refund_pending', 'refund_failed');
ALTER TABLE return_requests ADD CONSTRAINT return_requests_status_check CHECK (status IN (
    'requested', 'vendor_confirmed', 'rejected', 'approved_awaiting_provider_refund', 'refunded'));
DROP TABLE IF EXISTS order_refunds;
