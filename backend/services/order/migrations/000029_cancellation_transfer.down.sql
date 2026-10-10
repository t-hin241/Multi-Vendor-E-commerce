-- Refuses once a request was intercepted or transferred: the link and
-- the status would be lost.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM cancellation_requests WHERE interception_requested_at IS NOT NULL OR status = 'transferred')
        OR EXISTS (SELECT 1 FROM order_effects WHERE kind = 'intercept_shipment') THEN
        RAISE EXCEPTION 'cancellations were intercepted or transferred; keep migration 000029';
    END IF;
END $$;
ALTER TABLE order_effects DROP CONSTRAINT IF EXISTS order_effects_kind_check;
ALTER TABLE order_effects ADD CONSTRAINT order_effects_kind_check CHECK (kind IN (
    'create_shipment', 'cancel_shipment', 'release_inventory', 'notify', 'request_refund', 'restock_return', 'settle_vendor_order',
    'report_rejected_outcome', 'acquire_settlement_hold', 'release_settlement_hold', 'stop_fulfillment', 'recover_cancelled_stock',
    'create_replacement_attempt', 'recover_delivery_stock', 'authorize_return_shipment', 'dispatch_return_shipment',
    'close_return_shipment', 'notify_vendor'));

CREATE OR REPLACE FUNCTION cancellation_request_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'cancellation requests are never deleted';
    END IF;
    IF NEW.vendor_order_id <> OLD.vendor_order_id OR NEW.origin <> OLD.origin OR NEW.requested_by <> OLD.requested_by THEN
        RAISE EXCEPTION 'a cancellation request keeps its vendor order and requester';
    END IF;
    IF OLD.status IN ('rejected', 'resolved') AND NEW.status <> OLD.status THEN
        RAISE EXCEPTION 'a % cancellation request is final', OLD.status;
    END IF;
    RETURN NEW;
END $$;

DROP INDEX cancellation_requests_open_idx;
CREATE UNIQUE INDEX cancellation_requests_open_idx ON cancellation_requests (vendor_order_id)
    WHERE status NOT IN ('rejected', 'resolved');
ALTER TABLE cancellation_requests DROP CONSTRAINT cancellation_requests_transfer_check;
ALTER TABLE cancellation_requests DROP CONSTRAINT cancellation_requests_status_check;
ALTER TABLE cancellation_requests ADD CONSTRAINT cancellation_requests_status_check CHECK (status IN ('preparing', 'requested',
    'stopping_fulfillment', 'approved', 'refund_pending', 'resolved', 'rejected', 'needs_review'));
ALTER TABLE cancellation_requests DROP COLUMN delivery_exception_id, DROP COLUMN interception_requested_at;
